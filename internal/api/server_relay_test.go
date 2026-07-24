package api

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestServerRoutesRelayFramesToHandler(t *testing.T) {
	srv := NewServer()
	srv.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	got := make(chan RelayFrame, 1)
	gotPeer := make(chan *Peer, 1)
	srv.SetRelayFrameHandler(func(p *Peer, f RelayFrame) { gotPeer <- p; got <- f })

	gwConn, clConn := net.Pipe()
	go srv.ServeConnContext(context.Background(), gwConn)
	client := NewPeer(clConn, PeerOptions{})
	defer client.Close()

	// A normal request still dispatches.
	var out string
	if err := client.Call("ping", nil, &out); err != nil || out != "pong" {
		t.Fatalf("ping = %q err=%v", out, err)
	}

	// A relay frame reaches the handler.
	id := json.RawMessage("7")
	if err := client.send(message{
		JSONRPC: jsonrpcVersion, ID: &id, Method: "sessions.input",
		Route: &RouteHeader{ChanID: "c1", NodeID: "n1"}, Body: json.RawMessage(`"c2VhbGVk"`),
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case f := <-got:
		if f.Route.ChanID != "c1" || f.Route.NodeID != "n1" || string(f.Body) != `"c2VhbGVk"` {
			t.Errorf("relay frame = %+v body=%s", f.Route, f.Body)
		}
		if p := <-gotPeer; p == nil {
			t.Error("relay handler must receive the non-nil source peer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay frame not routed to handler")
	}
}
