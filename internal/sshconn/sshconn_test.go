package sshconn

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// noDeadlineConn makes deadlines no-ops, matching execConn, to assert
// coder/websocket tolerates that.
type noDeadlineConn struct{ net.Conn }

func (noDeadlineConn) SetDeadline(time.Time) error      { return nil }
func (noDeadlineConn) SetReadDeadline(time.Time) error  { return nil }
func (noDeadlineConn) SetWriteDeadline(time.Time) error { return nil }

// TestWebSocketOverCustomDialerNoDeadlines proves api.DialWS works through a
// DialContext returning a conn with no-op deadlines — how the ssh transport plugs in.
func TestWebSocketOverCustomDialerNoDeadlines(t *testing.T) {
	srv := api.NewServer()
	srv.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var n int
		if err := json.Unmarshal(params, &n); err != nil {
			return nil, err
		}
		return n, nil
	})

	hs := httptest.NewServer(srv.WSHandler(nil))
	defer hs.Close()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return noDeadlineConn{c}, nil // exercise no-op deadlines
		},
	}}

	wsURL := "ws://" + hs.Listener.Addr().String() + "/"
	c, err := api.DialWS(context.Background(), wsURL, "", client)
	if err != nil {
		t.Fatalf("DialWS: %v", err)
	}
	defer c.Close()

	var out int
	if err := c.Call("echo", 42, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != 42 {
		t.Errorf("echo = %d, want 42", out)
	}
}

// TestDialRoundTrip drives Dial against a fake "ssh" that just cats stdin to stdout,
// verifying the execConn carries bytes both ways and that Close tears the process down.
func TestDialRoundTrip(t *testing.T) {
	restore := command
	t.Cleanup(func() { command = restore })

	// Ignore ssh args; behave as a bidirectional pipe.
	bin := writeFakeBin(t, "#!/bin/sh\nexec cat\n")
	command = func(_, _, _ string) *exec.Cmd { return exec.Command(bin) }

	conn, err := Dial("user@host", "127.0.0.1:8443", "", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	msg := []byte("hello over ssh\n")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("round-trip = %q, want %q", buf, msg)
	}

	// Close kills `cat`; a killed process yields a non-nil wait error, which is fine.
	_ = conn.Close()
	// A second Close must not panic or block.
	_ = conn.Close()
}

// TestDialThreadsSSHPort asserts the SSH port reaches the ssh invocation as -p only
// when set, by capturing the args the command builder is asked to produce.
func TestDialThreadsSSHPort(t *testing.T) {
	restore := command
	t.Cleanup(func() { command = restore })

	bin := writeFakeBin(t, "#!/bin/sh\nexec cat\n")
	var gotPort string
	command = func(_, _, sshPort string) *exec.Cmd {
		gotPort = sshPort
		return exec.Command(bin)
	}

	for _, port := range []string{"", "2222"} {
		gotPort = "<unset>"
		conn, err := Dial("user@host", "127.0.0.1:8443", port, nil)
		if err != nil {
			t.Fatalf("Dial(%q): %v", port, err)
		}
		_ = conn.Close()
		if gotPort != port {
			t.Errorf("ssh port = %q, want %q", gotPort, port)
		}
	}
}

func TestExecConnDeadlinesAreNoops(t *testing.T) {
	c := &execConn{}
	if c.SetDeadline(time.Now()) != nil || c.SetReadDeadline(time.Now()) != nil || c.SetWriteDeadline(time.Now()) != nil {
		t.Error("deadlines should be no-ops returning nil")
	}
	if c.LocalAddr().Network() != "ssh" || c.RemoteAddr().String() != "ssh" {
		t.Error("stub addrs should report ssh")
	}
}

func writeFakeBin(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ssh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	return path
}
