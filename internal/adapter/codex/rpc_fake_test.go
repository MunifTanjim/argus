package codex

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeDaemon is a scripted Codex app-server: WebSocket over a Unix socket. handle
// answers client requests; nil result and nil error reply {"result":{}}.
type fakeDaemon struct {
	sock   string
	handle func(method string, params json.RawMessage) (any, *rpcError)
	got    chan wireMsg

	mu    sync.Mutex
	conns []*websocket.Conn
}

func newFakeDaemon(t *testing.T, handle func(string, json.RawMessage) (any, *rpcError)) *fakeDaemon {
	t.Helper()
	// Unix socket paths are length-limited; t.TempDir() can be too long on macOS.
	dir, err := os.MkdirTemp("", "cxd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeDaemon{sock: filepath.Join(dir, "d.sock"), handle: handle, got: make(chan wireMsg, 1024)}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go srv.Serve(ln)
	t.Cleanup(func() { f.closeConns(); srv.Close() })
	return f
}

func (f *fakeDaemon) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(rpcReadLimit)
	f.mu.Lock()
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	ctx := context.Background()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m wireMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		f.got <- m
		if m.Method == "" || m.ID == nil {
			continue
		}
		var res any = struct{}{}
		var rerr *rpcError
		if f.handle != nil {
			if r, e := f.handle(m.Method, m.Params); e != nil {
				rerr = e
			} else if r != nil {
				res = r
			}
		}
		out := map[string]any{"id": m.ID}
		if rerr != nil {
			out["error"] = rerr
		} else {
			out["result"] = res
		}
		b, _ = json.Marshal(out)
		_ = c.Write(ctx, websocket.MessageText, b)
	}
}

func (f *fakeDaemon) send(v any) {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		_ = c.Write(context.Background(), websocket.MessageText, b)
	}
}

func (f *fakeDaemon) push(method string, params any) {
	f.send(map[string]any{"method": method, "params": params})
}

func (f *fakeDaemon) request(id int, method string, params any) {
	f.send(map[string]any{"id": id, "method": method, "params": params})
}

// expect waits for the next client message with the given method (or, for "",
// the next client reply to a server request), skipping others.
func (f *fakeDaemon) expect(t *testing.T, method string) wireMsg {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-f.got:
			if m.Method == method && (method != "" || m.ID != nil) {
				return m
			}
		case <-deadline:
			t.Fatalf("timed out waiting for client message %q", method)
		}
	}
}

func (f *fakeDaemon) closeConns() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		_ = c.Close(websocket.StatusGoingAway, "")
	}
	f.conns = nil
}

func (f *fakeDaemon) waitConns(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.conns)
		f.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d daemon connections", n)
}
