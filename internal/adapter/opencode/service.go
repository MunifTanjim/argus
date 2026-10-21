// Package opencode adapts OpenCode's live HTTP API (base path /api, Basic auth user "opencode").
// Verified against v2.0.8: session list, message list, permission reply, and SSE event stream.
// Empirical tool and message shapes are documented in tool-shapes.md alongside this file.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

const Agent = "opencode"

var serviceInfoPath = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "opencode", "service.json")
}()

type serviceInfo struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	URL      string `json:"url"`
	PID      int    `json:"pid"`
	Password string `json:"password"`
}

func readServiceInfo() (serviceInfo, bool) {
	data, err := os.ReadFile(serviceInfoPath)
	if err != nil {
		return serviceInfo{}, false
	}
	var info serviceInfo
	if err := json.Unmarshal(data, &info); err != nil || info.URL == "" {
		return serviceInfo{}, false
	}
	if info.PID > 0 && syscall.Kill(info.PID, 0) != nil {
		return serviceInfo{}, false
	}
	return info, true
}

type ocEnvelope[T any] struct {
	Data   []T      `json:"data"`
	Cursor ocCursor `json:"cursor"`
}

// ocCursor is the opaque pagination cursor returned by list endpoints.
// Fields are null when a page has no neighbor in that direction.
type ocCursor struct {
	Previous string `json:"previous"`
	Next     string `json:"next"`
}

type ocSession struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"projectID"`
	Agent     string     `json:"agent"`
	Title     string     `json:"title"`
	Model     ocModelRef `json:"model"`
	Tokens    ocTokens   `json:"tokens"`
	Time      struct {
		Created  int64 `json:"created"`
		Updated  int64 `json:"updated"`
		Idle     int64 `json:"idle"`
		Viewed   int64 `json:"viewed"`
		Archived int64 `json:"archived"`
	} `json:"time"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

type ocModelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant"`
}

// ocModelEntry is one /api/model entry: a model as offered by one provider.
type ocModelEntry struct {
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
}

type ocProviderEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ocTokens is a session's cumulative token usage. Cache tokens are excluded from
// the history card count: they dwarf the worked tokens and misrepresent size.
type ocTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
}

type ocMessage struct {
	Type    string     `json:"type"`
	ID      string     `json:"id"`
	Text    string     `json:"text,omitempty"`
	Content []ocPart   `json:"content,omitempty"`
	Agent   string     `json:"agent,omitempty"`
	Model   ocModelRef `json:"model,omitempty"`
	Time    struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed,omitempty"` // assistant: when the response finished
	} `json:"time"`
	Error  *ocMessageError  `json:"error,omitempty"`  // assistant: set when the response failed
	Tokens *ocMessageTokens `json:"tokens,omitempty"` // assistant: usage of this response
}

// ocMessageTokens is one assistant response's usage. Output excludes reasoning.
type ocMessageTokens struct {
	Input     int          `json:"input"`
	Output    int          `json:"output"`
	Reasoning int          `json:"reasoning"`
	Cache     ocTokenCache `json:"cache"`
}

type ocTokenCache struct {
	Read  int `json:"read"`
	Write int `json:"write"`
}

// ocMessageError is why an assistant message failed (e.g. provider.quota).
type ocMessageError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
}

type ocPart struct {
	Type  string       `json:"type"`
	ID    string       `json:"id"`
	Name  string       `json:"name,omitempty"`
	Text  string       `json:"text,omitempty"`
	State *ocToolState `json:"state,omitempty"`
}

type ocToolState struct {
	Status   string          `json:"status"`
	Input    json.RawMessage `json:"input,omitempty"`
	Content  []ocToolContent `json:"content,omitempty"`
	Error    *ocToolError    `json:"error,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type ocToolError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type ocToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

var restHTTPClient = &http.Client{Timeout: 10 * time.Second}
var sseHTTPClient = &http.Client{}

type client struct {
	base string
	pass string
	hc   *http.Client
}

func newClient(info serviceInfo) *client {
	return &client{
		base: strings.TrimRight(info.URL, "/"),
		pass: info.Password,
		hc:   restHTTPClient,
	}
}

func (c *client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", c.pass)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.hc.Do(req)
}

// maxListPages bounds cursor pagination so a misbehaving server cannot loop
// forever. It is far above any realistic session or transcript length.
const maxListPages = 100

// listEnvelope fetches every page of a cursor-paginated list endpoint.
// initialQuery is applied to the first request only; subsequent pages use the
// server-issued cursor, which the server rejects if combined with any other
// query parameter (e.g. order). Pages accumulate in server order.
func listEnvelope[T any](ctx context.Context, c *client, path, initialQuery, label string) ([]T, error) {
	var all []T
	query := initialQuery
	for page := 0; page < maxListPages; page++ {
		p := path
		if query != "" {
			p += "?" + query
		}
		resp, err := c.do(ctx, http.MethodGet, p, nil)
		if err != nil {
			return nil, err
		}
		var env ocEnvelope[T]
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("%s: %s", label, resp.Status)
		}
		err = json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, env.Data...)
		if env.Cursor.Next == "" || len(env.Data) == 0 {
			break
		}
		query = "cursor=" + url.QueryEscape(env.Cursor.Next)
	}
	return all, nil
}

func (c *client) listActive(ctx context.Context) (map[string]bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/session/active", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list active: %s", resp.Status)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(env.Data))
	for id := range env.Data {
		out[id] = true
	}
	return out, nil
}

// listForms returns the forms still awaiting an answer on the session. OpenCode
// drops a form from this list once it is answered or cancelled, so the list is the
// authoritative "what is pending now" for a session.
func (c *client) listForms(ctx context.Context, sessionID string) ([]formInfo, error) {
	var env struct {
		Data []formInfo `json:"data"`
	}
	if err := c.getPending(ctx, "/api/session/"+sessionID+"/form", "list forms", &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

func (c *client) listPermissions(ctx context.Context, sessionID string) ([]ocPermission, error) {
	var env struct {
		Data []ocPermission `json:"data"`
	}
	if err := c.getPending(ctx, "/api/session/"+sessionID+"/permission", "list permissions", &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// getPending GETs path and decodes the JSON body into env. A 404 (the session or
// resource is gone) leaves env at its zero value and returns nil, so a caller
// treats it as "nothing pending". Any other non-200 returns an error so the caller
// keeps its state rather than clearing on a transient failure.
func (c *client) getPending(ctx context.Context, path, label string, env any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", label, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(env)
}

func (c *client) getSession(ctx context.Context, id string) (ocSession, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/session/"+id, nil)
	if err != nil {
		return ocSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ocSession{}, fmt.Errorf("get session: %s", resp.Status)
	}
	var env struct {
		Data ocSession `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return ocSession{}, err
	}
	return env.Data, nil
}

func (c *client) listSessions(ctx context.Context) ([]ocSession, error) {
	return listEnvelope[ocSession](ctx, c, "/api/session", "", "list sessions")
}

// A non-empty dir sets the session's working directory, so discovery hydrates the
// right cwd and repo. The server may ignore the requested model, so the caller
// checks the returned session's Model.
func (c *client) createSession(ctx context.Context, dir string, model ocModelRef) (ocSession, error) {
	body := map[string]any{}
	if dir != "" {
		body["location"] = map[string]string{"directory": dir}
	}
	if model.ID != "" {
		body["model"] = map[string]string{"id": model.ID, "providerID": model.ProviderID}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ocSession{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/session", strings.NewReader(string(payload)))
	if err != nil {
		return ocSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ocSession{}, fmt.Errorf("create session: %s", resp.Status)
	}
	var env struct {
		Data ocSession `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return ocSession{}, err
	}
	if env.Data.ID == "" {
		return ocSession{}, fmt.Errorf("create session: empty id in response")
	}
	return env.Data, nil
}

// deleteSession removes a session and its child sessions.
func (c *client) deleteSession(ctx context.Context, sessionID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/session/"+sessionID, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete session: %s", resp.Status)
	}
	return nil
}

// switchModel sets the model used by the session's subsequent turns.
func (c *client) switchModel(ctx context.Context, sessionID string, model ocModelRef) error {
	return c.send(ctx, http.MethodPost, "/api/session/"+sessionID+"/model", "switch model", map[string]any{
		"model": map[string]string{"id": model.ID, "providerID": model.ProviderID},
	})
}

// listModels returns the enabled models in server order. A model id is
// providerID + "/" + modelID, because the same model can be offered by several
// providers; the label names the provider to tell those apart.
func (c *client) listModels(ctx context.Context) ([]api.ModelInfo, error) {
	models, err := listEnvelope[ocModelEntry](ctx, c, "/api/model", "", "list models")
	if err != nil {
		return nil, err
	}
	// Without the provider list the labels fall back to provider ids.
	providers, _ := listEnvelope[ocProviderEntry](ctx, c, "/api/provider", "", "list providers")
	names := make(map[string]string, len(providers))
	for _, p := range providers {
		names[p.ID] = p.Name
	}
	var out []api.ModelInfo
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		provider := names[m.ProviderID]
		if provider == "" {
			provider = m.ProviderID
		}
		out = append(out, api.ModelInfo{ID: m.ProviderID + "/" + m.ModelID, Name: m.Name + " · " + provider})
	}
	return out, nil
}

func (c *client) readMessages(ctx context.Context, sessionID string) ([]ocMessage, error) {
	return listEnvelope[ocMessage](ctx, c, "/api/session/"+sessionID+"/message", "order=asc", "read messages")
}

// readMessagesRaw fetches every message page without decoding into ocMessage,
// preserving the server's exact JSON for a lossless export bundle.
func (c *client) readMessagesRaw(ctx context.Context, sessionID string) ([]json.RawMessage, error) {
	return listEnvelope[json.RawMessage](ctx, c, "/api/session/"+sessionID+"/message", "order=asc", "read messages")
}

func (c *client) respondPermission(ctx context.Context, sessionID, requestID, decision, message string) error {
	return c.send(ctx, http.MethodPost, "/api/session/"+sessionID+"/permission/"+requestID+"/reply", "respond permission", struct {
		Decision string  `json:"decision"`
		Message  *string `json:"message,omitempty"`
	}{Decision: decision, Message: nilIfEmpty(message)})
}

type ocCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ocSkill struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// ocSkillMention marks where the prompt text names a skill. Offsets are UTF-16
// code units, as the OpenCode TUI sends them.
type ocSkillMention struct {
	ID      string    `json:"id"`
	Mention ocMention `json:"mention"`
}

type ocMention struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

func locationQuery(dir string) string {
	return url.Values{"location[directory]": {dir}}.Encode()
}

func (c *client) listCommands(ctx context.Context, dir string) ([]ocCommand, error) {
	return listEnvelope[ocCommand](ctx, c, "/api/command", locationQuery(dir), "list commands")
}

func (c *client) listSkills(ctx context.Context, dir string) ([]ocSkill, error) {
	return listEnvelope[ocSkill](ctx, c, "/api/skill", locationQuery(dir), "list skills")
}

// send treats a 200 or a 204 with no body as success.
func (c *client) send(ctx context.Context, method, path, label string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, method, path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s: %s", label, resp.Status)
	}
	return nil
}

func (c *client) sendPrompt(ctx context.Context, sessionID, text string, skills []ocSkillMention) error {
	return c.send(ctx, http.MethodPost, "/api/session/"+sessionID+"/prompt", "send prompt", struct {
		Text   string           `json:"text"`
		Skills []ocSkillMention `json:"skills,omitempty"`
	}{text, skills})
}

func (c *client) runCommand(ctx context.Context, sessionID, name, args string) error {
	return c.send(ctx, http.MethodPost, "/api/session/"+sessionID+"/command", "run command", map[string]string{"name": name, "text": args})
}

func (c *client) compact(ctx context.Context, sessionID string) error {
	return c.send(ctx, http.MethodPost, "/api/session/"+sessionID+"/compact", "compact", struct{}{})
}

// updateTitle PATCHes a session's title.
func (c *client) updateTitle(ctx context.Context, sessionID, title string) error {
	return c.send(ctx, http.MethodPatch, "/api/session/"+sessionID, "update title", map[string]string{"title": title})
}

func (c *client) replyForm(ctx context.Context, sessionID, formID string, answer map[string]any) error {
	return c.send(ctx, http.MethodPost, "/api/session/"+sessionID+"/form/"+formID+"/reply", "reply form", map[string]any{"answer": answer})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (c *client) openEvents(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/event", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", c.pass)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := sseHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("open events: %s", resp.Status)
	}
	return resp.Body, nil
}
