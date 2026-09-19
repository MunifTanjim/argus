// Package opencode adapts OpenCode's live HTTP API (base path /api, Basic auth user "opencode").
// Verified against v2.0.8: session list, message list, permission reply, and SSE event stream.
// Empirical tool and message shapes are documented in tool-shapes.md alongside this file.
package opencode

import (
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
	ID        string `json:"id"`
	ProjectID string `json:"projectID"`
	Agent     string `json:"agent"`
	Title     string `json:"title"`
	Time      struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
		Idle    int64 `json:"idle"`
		Viewed  int64 `json:"viewed"`
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

type ocMessage struct {
	Type    string     `json:"type"`
	ID      string     `json:"id"`
	Text    string     `json:"text,omitempty"`
	Content []ocPart   `json:"content,omitempty"`
	Agent   string     `json:"agent,omitempty"`
	Model   ocModelRef `json:"model,omitempty"`
	Time    struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

type ocPart struct {
	Type  string       `json:"type"`
	ID    string       `json:"id"`
	Name  string       `json:"name,omitempty"`
	Text  string       `json:"text,omitempty"`
	State *ocToolState `json:"state,omitempty"`
}

type ocToolState struct {
	Status  string          `json:"status"`
	Input   json.RawMessage `json:"input,omitempty"`
	Content []ocToolContent `json:"content,omitempty"`
	Error   *ocToolError    `json:"error,omitempty"`
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

func (c *client) readMessages(ctx context.Context, sessionID string) ([]ocMessage, error) {
	return listEnvelope[ocMessage](ctx, c, "/api/session/"+sessionID+"/message", "order=asc", "read messages")
}

// readMessagesRaw fetches every message page without decoding into ocMessage,
// preserving the server's exact JSON for a lossless export bundle.
func (c *client) readMessagesRaw(ctx context.Context, sessionID string) ([]json.RawMessage, error) {
	return listEnvelope[json.RawMessage](ctx, c, "/api/session/"+sessionID+"/message", "order=asc", "read messages")
}

func (c *client) respondPermission(ctx context.Context, sessionID, requestID, decision, message string) error {
	payload, err := json.Marshal(struct {
		Decision string  `json:"decision"`
		Message  *string `json:"message,omitempty"`
	}{Decision: decision, Message: nilIfEmpty(message)})
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/session/"+sessionID+"/permission/"+requestID+"/reply", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("respond permission: %s", resp.Status)
	}
	return nil
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
