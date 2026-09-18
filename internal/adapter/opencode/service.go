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

type ocSession struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectID"`
	Directory string `json:"directory"`
	ParentID  string `json:"parentID,omitempty"`
	Title     string `json:"title"`
	Time      struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
}

type ocMessageItem struct {
	Info  ocMessage `json:"info"`
	Parts []ocPart  `json:"parts"`
}

type ocMessage struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Role      string `json:"role"`
	ParentID  string `json:"parentID,omitempty"`
	Mode      string `json:"mode,omitempty"`
	ModelID   string `json:"modelID,omitempty"`
	Time      struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed,omitempty"`
	} `json:"time"`
}

type ocPart struct {
	ID          string       `json:"id"`
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	CallID      string       `json:"callID,omitempty"`
	Tool        string       `json:"tool,omitempty"`
	State       *ocToolState `json:"state,omitempty"`
	Agent       string       `json:"agent,omitempty"`
	Prompt      string       `json:"prompt,omitempty"`
	Description string       `json:"description,omitempty"`
}

type ocToolState struct {
	Status   string          `json:"status"`
	Input    json.RawMessage `json:"input,omitempty"`
	Output   string          `json:"output,omitempty"`
	Title    string          `json:"title,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
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

func (c *client) listSessions(ctx context.Context) ([]ocSession, error) {
	resp, err := c.do(ctx, http.MethodGet, "/session", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list sessions: %s", resp.Status)
	}
	var out []ocSession
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *client) readMessages(ctx context.Context, sessionID string) ([]ocMessageItem, error) {
	resp, err := c.do(ctx, http.MethodGet, "/session/"+sessionID+"/message", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read messages: %s", resp.Status)
	}
	var out []ocMessageItem
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *client) respondPermission(ctx context.Context, sessionID, permissionID, response string) error {
	body := strings.NewReader(fmt.Sprintf(`{"response":%q}`, response))
	resp, err := c.do(ctx, http.MethodPost, "/session/"+sessionID+"/permissions/"+permissionID, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("respond permission: %s", resp.Status)
	}
	return nil
}

func (c *client) openEvents(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/global/event", nil)
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
