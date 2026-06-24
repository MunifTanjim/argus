package api

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// makePeers wires two peers over an in-memory pipe. Each may register a dispatch
// and a notification sink.
func makePeers(a, b PeerOptions) (*Peer, *Peer, func()) {
	ca, cb := net.Pipe()
	pa := NewPeer(ca, a)
	pb := NewPeer(cb, b)
	return pa, pb, func() { pa.Close(); pb.Close() }
}

func echoDispatch(_ context.Context, method string, params json.RawMessage) (any, error) {
	var in struct{ Msg string }
	_ = json.Unmarshal(params, &in)
	return map[string]string{"got": in.Msg, "by": method}, nil
}

// A symmetric peer can be called by its remote end.
func TestPeerCallEitherDirection(t *testing.T) {
	// Only B serves "echo"; A calls it.
	pa, _, done := makePeers(
		PeerOptions{},
		PeerOptions{Dispatch: echoDispatch},
	)
	defer done()

	var out struct{ Got, By string }
	if err := pa.Call("echo", map[string]string{"Msg": "hi"}, &out); err != nil {
		t.Fatalf("A→B call: %v", err)
	}
	if out.Got != "hi" || out.By != "echo" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

// Both ends can serve requests on the same connection (true symmetry): B calls a
// handler registered on A while A could also call B.
func TestPeerBothEndsServe(t *testing.T) {
	pa, pb, done := makePeers(
		PeerOptions{Dispatch: func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
			return map[string]int{"v": 1}, nil
		}},
		PeerOptions{Dispatch: func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
			return map[string]int{"v": 2}, nil
		}},
	)
	defer done()

	var fromB struct{ V int }
	if err := pa.Call("x", nil, &fromB); err != nil { // A→B
		t.Fatalf("A→B: %v", err)
	}
	var fromA struct{ V int }
	if err := pb.Call("y", nil, &fromA); err != nil { // B→A, same conn
		t.Fatalf("B→A: %v", err)
	}
	if fromB.V != 2 || fromA.V != 1 {
		t.Fatalf("crossed wires: fromB=%d fromA=%d", fromB.V, fromA.V)
	}
}

// Unknown methods surface as a method-not-found RPCError.
func TestPeerMethodNotFound(t *testing.T) {
	pa, _, done := makePeers(PeerOptions{}, PeerOptions{}) // B has no dispatch
	defer done()

	err := pa.Call("nope", nil, nil)
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("want method-not-found, got %v", err)
	}
}

// Notifications flow to the peer's OnNotify sink, in both directions.
func TestPeerNotifyBothDirections(t *testing.T) {
	aGot := make(chan Notification, 1)
	bGot := make(chan Notification, 1)
	pa, pb, done := makePeers(
		PeerOptions{OnNotify: func(n Notification) { aGot <- n }},
		PeerOptions{OnNotify: func(n Notification) { bGot <- n }},
	)
	defer done()

	if err := pa.Notify("ping", map[string]int{"n": 7}); err != nil {
		t.Fatalf("A notify: %v", err)
	}
	select {
	case n := <-bGot:
		if n.Method != "ping" {
			t.Fatalf("B want ping, got %q", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("B never received notification")
	}

	if err := pb.Notify("pong", nil); err != nil {
		t.Fatalf("B notify: %v", err)
	}
	select {
	case n := <-aGot:
		if n.Method != "pong" {
			t.Fatalf("A want pong, got %q", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("A never received notification")
	}
}

// A pending call returns an error when the connection closes.
func TestPeerCallUnblocksOnClose(t *testing.T) {
	block := make(chan struct{})
	pa, _, done := makePeers(
		PeerOptions{},
		PeerOptions{Dispatch: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			<-block // never returns until test ends
			return nil, nil
		}},
	)
	defer done()
	defer close(block)

	errCh := make(chan error, 1)
	go func() { errCh <- pa.Call("hang", nil, nil) }()

	time.Sleep(20 * time.Millisecond)
	pa.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want error after close")
		}
	case <-time.After(time.Second):
		t.Fatal("Call did not unblock on close")
	}
}
