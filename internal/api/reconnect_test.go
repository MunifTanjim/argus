package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// flapSrv is a minimal server that can be stopped and restarted on the same unix
// socket, to exercise the client's reconnect behavior. Each accepted connection greets
// the client with a "hello" notification and answers "echo".
type flapSrv struct {
	socket string
	mu     sync.Mutex
	l      net.Listener
	peers  []*Peer
}

func newFlapSrv(t *testing.T) *flapSrv {
	t.Helper()
	s := &flapSrv{socket: filepath.Join(t.TempDir(), "r.sock")}
	s.start(t)
	t.Cleanup(s.stop)
	return s
}

func (s *flapSrv) start(t *testing.T) {
	t.Helper()
	l, err := net.Listen("unix", s.socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.mu.Lock()
	s.l = l
	s.mu.Unlock()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			p := NewPeer(conn, PeerOptions{
				Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
					if method == "echo" {
						return "pong", nil
					}
					return nil, &RPCError{Code: CodeMethodNotFound, Message: method}
				},
			})
			_ = p.Notify("hello", nil)
			s.mu.Lock()
			s.peers = append(s.peers, p)
			s.mu.Unlock()
		}
	}()
}

func (s *flapSrv) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l != nil {
		s.l.Close() // Go unlinks the unix socket file on close, freeing it for restart
		s.l = nil
	}
	for _, p := range s.peers {
		p.Close()
	}
	s.peers = nil
}

func recvState(t *testing.T, c *ReconnectingClient) bool {
	t.Helper()
	select {
	case s := <-c.States():
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a connection-state transition")
		return false
	}
}

func waitNotify(t *testing.T, c *ReconnectingClient, method string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case n := <-c.Events():
			if n.Method == method {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q notification", method)
		}
	}
}

func TestReconnectingClientRecovers(t *testing.T) {
	srv := newFlapSrv(t)
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", srv.socket)
	}

	c, err := NewReconnectingClient(context.Background(), dial)
	if err != nil {
		t.Fatalf("NewReconnectingClient: %v", err)
	}
	defer c.Close()

	waitNotify(t, c, "hello") // greeting from the first connection
	var out string
	if err := c.Call("echo", nil, &out); err != nil || out != "pong" {
		t.Fatalf("echo before drop = (%q, %v)", out, err)
	}

	// Drop the server: the client should report disconnected and fail calls.
	srv.stop()
	if recvState(t, c) {
		t.Fatal("expected disconnected (false) state after server stop")
	}
	if err := c.Call("echo", nil, &out); err == nil {
		t.Fatal("expected Call to error while disconnected")
	}

	// Bring the server back and kick an immediate reconnect.
	srv.start(t)
	c.Reconnect()
	if !recvState(t, c) {
		t.Fatal("expected reconnected (true) state after server restart")
	}
	waitNotify(t, c, "hello") // greeting from the new connection
	if err := c.Call("echo", nil, &out); err != nil || out != "pong" {
		t.Fatalf("echo after reconnect = (%q, %v)", out, err)
	}
}

func TestReconnectingClientFirstDialError(t *testing.T) {
	dial := func(context.Context) (net.Conn, error) { return nil, errors.New("nope") }
	if _, err := NewReconnectingClient(context.Background(), dial); err == nil {
		t.Fatal("expected first-dial failure to be returned")
	}
}
