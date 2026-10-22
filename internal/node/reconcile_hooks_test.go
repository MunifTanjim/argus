package node

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// fakeCodexDaemon answers every request on the Codex app-server control socket
// under codexHome and reports each method it receives.
func fakeCodexDaemon(t *testing.T, codexHome string) <-chan string {
	t.Helper()
	dir := filepath.Join(codexHome, "app-server-control")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "app-server-control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	methods := make(chan string, 256)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(b, &m) != nil || m.Method == "" {
				continue
			}
			methods <- m.Method
			if len(m.ID) > 0 {
				resp, _ := json.Marshal(map[string]any{"id": m.ID, "result": map[string]any{}})
				_ = c.Write(context.Background(), websocket.MessageText, resp)
			}
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return methods
}

func TestReconcileHooksReloadsCodexMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	// The control socket path must fit the unix socket length limit.
	codexHome, err := os.MkdirTemp("/tmp", "cx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(codexHome) })
	t.Setenv("CODEX_HOME", codexHome)
	methods := fakeCodexDaemon(t, codexHome)

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.ReconcileHooks(slog.New(slog.NewTextHandler(io.Discard, nil)), adapter.InstallOptions{
		MCPURL: "http://127.0.0.1:7777/mcp", MCPToken: "tok",
	})
	b, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil || !strings.Contains(string(b), "[mcp_servers.argus]") {
		t.Fatalf("config.toml = %q, err = %v", b, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx, shortSocket(t)) }()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-methods:
			if m == "config/mcpServer/reload" {
				return
			}
		case <-timeout:
			t.Fatal("the node's codex discoverer never sent config/mcpServer/reload")
		}
	}
}
