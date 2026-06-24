package api

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func startServer(t *testing.T, s *Server) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "t.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go s.Serve(l)
	t.Cleanup(func() { l.Close() })
	return socket
}

func TestCallReturnsResult(t *testing.T) {
	s := NewServer()
	s.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ Msg string }
		_ = json.Unmarshal(params, &in)
		return map[string]string{"got": in.Msg}, nil
	})
	socket := startServer(t, s)

	c, err := Dial(socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	var out struct{ Got string }
	if err := c.Call("echo", map[string]string{"Msg": "hi"}, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Got != "hi" {
		t.Fatalf("want got=hi, got %q", out.Got)
	}
}

func TestCallMethodNotFound(t *testing.T) {
	socket := startServer(t, NewServer())
	c, _ := Dial(socket)
	defer c.Close()

	err := c.Call("nope", nil, nil)
	if err == nil {
		t.Fatal("want error for unknown method")
	}
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("want method-not-found RPCError, got %v", err)
	}
}

func TestServerNotifiesClient(t *testing.T) {
	s := NewServer()
	notifiers := make(chan Notifier, 1)
	s.OnConnect(func(n Notifier) func() {
		notifiers <- n
		return nil
	})
	socket := startServer(t, s)

	c, _ := Dial(socket)
	defer c.Close()

	var n Notifier
	select {
	case n = <-notifiers:
	case <-time.After(time.Second):
		t.Fatal("server never saw the connection")
	}

	if err := n.Notify("ping", map[string]int{"n": 7}); err != nil {
		t.Fatalf("notify: %v", err)
	}

	select {
	case ev := <-c.Events():
		if ev.Method != "ping" {
			t.Fatalf("want ping, got %q", ev.Method)
		}
		var p struct{ N int }
		_ = json.Unmarshal(ev.Params, &p)
		if p.N != 7 {
			t.Fatalf("want n=7, got %d", p.N)
		}
	case <-time.After(time.Second):
		t.Fatal("client never received notification")
	}
}
