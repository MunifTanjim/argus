package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func postToolsList(port int, token string) (*http.Response, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp/claude", port)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

func TestMCPListenerServesTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	if err := d.ListenMCP(0, "tok"); err != nil {
		t.Fatal(err)
	}
	port := d.mcpListener.Addr().(*net.TCPAddr).Port
	socket := shortSocket(t)
	go func() { _ = d.Run(ctx, socket) }()
	dialWithRetry(t, socket).Close()

	resp, err := postToolsList(port, "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Tools) != 10 {
		t.Fatalf("tools = %d, want 10", len(out.Result.Tools))
	}

	bad, err := postToolsList(port, "wrong")
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", bad.StatusCode)
	}
}

func TestMCPPortInUseKeepsNodeRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	if err := d.ListenMCP(port, "tok"); err == nil || !strings.Contains(err.Error(), fmt.Sprintf(":%d", port)) {
		t.Fatalf("ListenMCP on a busy port: err = %v", err)
	}
	if d.mcpListener != nil {
		t.Fatal("a failed bind left a listener")
	}
	socket := shortSocket(t)
	go func() { _ = d.Run(ctx, socket) }()
	c := dialWithRetry(t, socket)
	defer c.Close()
}
