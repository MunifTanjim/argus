package api

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/e2e"
)

func TestPeerRoutesRelayFrameToHook(t *testing.T) {
	ca, cb := net.Pipe()
	got := make(chan RelayFrame, 1)
	a := NewPeer(ca, PeerOptions{})
	b := NewPeer(cb, PeerOptions{OnRelayFrame: func(_ *Peer, f RelayFrame) { got <- f }})
	defer a.Close()
	defer b.Close()

	id := json.RawMessage("5")
	frame := message{
		JSONRPC: jsonrpcVersion,
		ID:      &id,
		Method:  "sessions.input",
		Route:   &RouteHeader{ChanID: "c7", NodeID: "home", SubID: "s-1"},
		Body:    json.RawMessage(`"c2VhbGVkYm9keQ=="`),
	}
	if err := a.send(frame); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case f := <-got:
		if f.Method != "sessions.input" || f.Route.ChanID != "c7" ||
			f.Route.NodeID != "home" || f.Route.SubID != "s-1" {
			t.Errorf("relay frame header wrong: method=%q route=%+v", f.Method, f.Route)
		}
		if f.ID == nil || string(*f.ID) != "5" {
			t.Errorf("relay frame id = %v, want 5", f.ID)
		}
		if string(f.Body) != `"c2VhbGVkYm9keQ=="` {
			t.Errorf("relay frame body = %s", f.Body)
		}
		if len(f.Raw) == 0 {
			t.Error("relay frame Raw is empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay frame never reached the hook")
	}
}

func TestPeerNonRelayStillDispatches(t *testing.T) {
	ca, cb := net.Pipe()
	relayHits := int32(0)
	a := NewPeer(ca, PeerOptions{})
	b := NewPeer(cb, PeerOptions{
		OnRelayFrame: func(*Peer, RelayFrame) { atomic.AddInt32(&relayHits, 1) },
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			return "pong-" + method, nil
		},
	})
	defer a.Close()
	defer b.Close()

	var out string
	if err := a.Call("ping", nil, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "pong-ping" {
		t.Errorf("dispatch result = %q, want pong-ping", out)
	}
	if atomic.LoadInt32(&relayHits) != 0 {
		t.Error("a non-relay request must not reach OnRelayFrame")
	}
}

func TestPeerRelayFrameDroppedWhenNoHook(t *testing.T) {
	ca, cb := net.Pipe()
	dispatched := int32(0)
	a := NewPeer(ca, PeerOptions{})
	// No OnRelayFrame; Dispatch would be hit only if a relay frame leaked into it.
	b := NewPeer(cb, PeerOptions{
		Dispatch: func(context.Context, string, json.RawMessage) (any, error) {
			atomic.AddInt32(&dispatched, 1)
			return nil, nil
		},
	})
	defer a.Close()
	defer b.Close()

	id := json.RawMessage("9")
	err := a.send(message{
		JSONRPC: jsonrpcVersion, ID: &id, Method: "sessions.kill",
		Route: &RouteHeader{ChanID: "c1"}, Body: json.RawMessage(`"x"`),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&dispatched) != 0 {
		t.Error("a relay frame must not reach Dispatch when OnRelayFrame is nil")
	}
}

// A gateway peer forwards a client's relayed frame verbatim to the node peer via
// SendRawFrame; the node opens the sealed Body with its Channel — end-to-end relay.
func TestSendRawFrameRelaysVerbatimAndOpens(t *testing.T) {
	// Establish one e2e session shared by a client Channel and a node Channel.
	nodeKey, _ := e2e.GenerateKeyPair()
	clientKey, _ := e2e.GenerateKeyPair()
	prologue := []byte("argus-e2e/v1|c7")
	initr, msg1, err := e2e.NewInitiator(clientKey, nodeKey.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	nodeSess, _, msg2, err := e2e.Respond(nodeKey, prologue, msg1)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	clientSess, err := initr.Finish(msg2)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	clientCh := NewChannel("c7", clientSess)
	nodeCh := NewChannel("c7", nodeSess)

	// Wire: client <-> gateway, gateway <-> node.
	clConn, gwClConn := net.Pipe()
	gwNodeConn, ndConn := net.Pipe()

	nodeGot := make(chan RelayFrame, 1)
	node := NewPeer(ndConn, PeerOptions{OnRelayFrame: func(_ *Peer, f RelayFrame) { nodeGot <- f }})
	defer node.Close()

	// The gateway holds both peer legs; forward client relay frames to the node verbatim.
	var gwToNode *Peer
	gwToClient := NewPeer(gwClConn, PeerOptions{OnRelayFrame: func(_ *Peer, f RelayFrame) {
		_ = gwToNode.SendRawFrame(f.Raw)
	}})
	defer gwToClient.Close()
	gwToNode = NewPeer(gwNodeConn, PeerOptions{})
	defer gwToNode.Close()

	client := NewPeer(clConn, PeerOptions{})
	defer client.Close()

	// Client seals a request and sends it toward the gateway.
	id := json.RawMessage("11")
	params := json.RawMessage(`{"session_id":"default:%3","text":"top-secret"}`)
	frame, err := clientCh.sealRequest(&id, "sessions.input", "home", params)
	if err != nil {
		t.Fatalf("sealRequest: %v", err)
	}
	if err := client.send(frame); err != nil {
		t.Fatalf("client send: %v", err)
	}

	select {
	case f := <-nodeGot:
		if f.Route.ChanID != "c7" || f.Method != "sessions.input" {
			t.Errorf("node got wrong header: method=%q route=%+v", f.Method, f.Route)
		}
		got, err := nodeCh.OpenParams(RelayFrame{Body: f.Body})
		if err != nil {
			t.Fatalf("node OpenParams: %v", err)
		}
		if string(got) != string(params) {
			t.Errorf("node opened %s, want %s", got, params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relayed frame never reached the node")
	}
}
