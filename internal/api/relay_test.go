package api

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"

	"github.com/MunifTanjim/argus/internal/e2e"
)

func TestMessageIsRelay(t *testing.T) {
	withRoute := message{
		JSONRPC: jsonrpcVersion,
		Method:  "some.method",
		Route:   &RouteHeader{ChanID: "chan-1"},
		Body:    json.RawMessage(`"abc"`),
	}
	if !withRoute.isRelay() {
		t.Error("message with Route should isRelay() == true")
	}

	plain := message{
		JSONRPC: jsonrpcVersion,
		Method:  "some.method",
		Params:  json.RawMessage(`{}`),
	}
	if plain.isRelay() {
		t.Error("message without Route should isRelay() == false")
	}
}

// TestPeerNilOnRelayFrameDropsSilently verifies that a Peer with nil OnRelayFrame
// drops relay frames: they do not reach Dispatch or OnNotify and no error is returned.
func TestPeerNilOnRelayFrameDropsSilently(t *testing.T) {
	var dispatched atomic.Bool
	probed := make(chan struct{})

	ca, cb := net.Pipe()
	// receiver: nil OnRelayFrame; dispatch/OnNotify track unexpected delivery.
	receiver := NewPeer(ca, PeerOptions{
		Dispatch: func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
			dispatched.Store(true)
			return nil, nil
		},
		OnNotify: func(n Notification) {
			if n.Method == "probe" {
				close(probed)
			} else {
				dispatched.Store(true) // any unexpected non-probe notification
			}
		},
	})
	defer receiver.Close()

	sender := NewPeer(cb, PeerOptions{})
	defer sender.Close()

	// Build a relay frame and send it.
	relayFrame := message{
		JSONRPC: jsonrpcVersion,
		Method:  "e2e.handshake",
		Route:   &RouteHeader{ChanID: "chan-x"},
		Body:    json.RawMessage(`"aGVsbG8="`),
	}
	raw, err := json.Marshal(relayFrame)
	if err != nil {
		t.Fatalf("marshal relay frame: %v", err)
	}
	if err := sender.SendRawFrame(raw); err != nil {
		t.Fatalf("SendRawFrame: %v", err)
	}

	// Send a probe notification after the relay frame; the probe arriving at OnNotify
	// proves the relay frame was processed first (frames are read in order).
	if err := sender.Notify("probe", nil); err != nil {
		t.Fatalf("Notify probe: %v", err)
	}
	<-probed

	if dispatched.Load() {
		t.Error("relay frame must not reach Dispatch or OnNotify")
	}
}

// newE2ESessionPair runs a complete Noise IK handshake and returns the paired
// client and node Sessions using the channel prologue from ChannelPrologue.
func newE2ESessionPair(t *testing.T, nodeID, chanID string) (clientSess, nodeSess *e2e.Session) {
	t.Helper()
	nodeKey, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair node: %v", err)
	}
	clientKey, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair client: %v", err)
	}
	prologue := ChannelPrologue(nodeID, chanID)
	init, msg1, err := e2e.NewInitiator(clientKey, nodeKey.Public, prologue)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	nodeSess, _, msg2, err := e2e.Respond(nodeKey, prologue, msg1)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	clientSess, err = init.Finish(msg2)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return clientSess, nodeSess
}

func TestChannelSealOpenRoundTrip(t *testing.T) {
	const nodeID = "node-1"
	const chanID = "chan-7"

	clientSess, nodeSess := newE2ESessionPair(t, nodeID, chanID)
	clientCh := NewChannel(chanID, clientSess)
	nodeCh := NewChannel(chanID, nodeSess)

	t.Run("request SealRequestFrame→OpenParams", func(t *testing.T) {
		idRaw := json.RawMessage(`1`)
		params := json.RawMessage(`{"session_id":"s1"}`)

		raw, err := clientCh.SealRequestFrame(&idRaw, "sessions.list", nodeID, params)
		if err != nil {
			t.Fatalf("SealRequestFrame: %v", err)
		}

		// Parse raw back into a message to extract a RelayFrame.
		var m message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		if !m.isRelay() {
			t.Fatal("sealed request frame should be a relay frame")
		}
		rf := RelayFrame{
			Method: m.Method,
			ID:     m.ID,
			Route:  *m.Route,
			Body:   m.Body,
			Raw:    raw,
		}

		got, err := nodeCh.OpenParams(rf)
		if err != nil {
			t.Fatalf("OpenParams: %v", err)
		}
		if string(got) != string(params) {
			t.Errorf("OpenParams = %s, want %s", got, params)
		}
	})

	t.Run("response SealResponseFrame→OpenResponse", func(t *testing.T) {
		idRaw := json.RawMessage(`2`)
		result := json.RawMessage(`{"sessions":[]}`)

		raw, err := nodeCh.SealResponseFrame(&idRaw, result, nil)
		if err != nil {
			t.Fatalf("SealResponseFrame: %v", err)
		}

		var m message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		if !m.isRelay() {
			t.Fatal("sealed response frame should be a relay frame")
		}
		rf := RelayFrame{
			Method: m.Method,
			ID:     m.ID,
			Route:  *m.Route,
			Body:   m.Body,
			Raw:    raw,
		}

		gotResult, gotErr, err := clientCh.OpenResponse(rf)
		if err != nil {
			t.Fatalf("OpenResponse: %v", err)
		}
		if gotErr != nil {
			t.Fatalf("OpenResponse rpcErr = %v, want nil", gotErr)
		}
		if string(gotResult) != string(result) {
			t.Errorf("OpenResponse result = %s, want %s", gotResult, result)
		}
	})
}
