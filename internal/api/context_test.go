package api

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestHandlerCanNotifyOverConnection(t *testing.T) {
	srv := NewServer()
	srv.Handle("push.me", func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, ok := NotifierFrom(ctx)
		if !ok {
			t.Error("no notifier in ctx")
			return nil, nil
		}
		_ = n.Notify("pong", map[string]string{"hi": "there"})
		return "ok", nil
	})

	c1, c2 := net.Pipe()
	go srv.ServeConn(c1)
	client := NewClient(c2) // wraps an established conn as a Peer-based Client
	defer client.Close()

	got := make(chan Notification, 1)
	go func() {
		for n := range client.Events() {
			got <- n
		}
	}()

	if err := client.Call("push.me", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-got:
		if n.Method != "pong" {
			t.Fatalf("notification method = %q, want pong", n.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification received")
	}
}
