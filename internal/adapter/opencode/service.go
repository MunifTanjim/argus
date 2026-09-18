package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Data []T `json:"data"`
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
}

type ocToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type client struct {
	base string
	pass string
	hc   *http.Client
}

func newClient(info serviceInfo) *client {
	return &client{
		base: strings.TrimRight(info.URL, "/"),
		pass: info.Password,
		hc:   &http.Client{Timeout: 10 * time.Second},
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

func listEnvelope[T any](ctx context.Context, c *client, path, label string) ([]T, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", label, resp.Status)
	}
	var env ocEnvelope[T]
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

func (c *client) listSessions(ctx context.Context) ([]ocSession, error) {
	return listEnvelope[ocSession](ctx, c, "/api/session", "list sessions")
}

func (c *client) readMessages(ctx context.Context, sessionID string) ([]ocMessage, error) {
	return listEnvelope[ocMessage](ctx, c, "/api/session/"+sessionID+"/message", "read messages")
}

func (c *client) respondPermission(ctx context.Context, sessionID, requestID, decision, message string) error {
	payload, err := json.Marshal(struct {
		Decision string  `json:"decision"`
		Message  *string `json:"message"`
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
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("open events: %s", resp.Status)
	}
	return resp.Body, nil
}
