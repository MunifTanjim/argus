package gateway

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// TestRelayFrameFromNonOwningClientDropped verifies that a relay frame from a
// client that does NOT own the target channel is dropped, not forwarded. Without
// the ownership check, any authenticated client could inject frames into (or, by
// overflowing the queue, tear down) another client's channel just by guessing the
// sequential chan_id.
func TestRelayFrameFromNonOwningClientDropped(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	nodePeer, nodeFrames := adoptFakeNode(t, srv, "n1", "PUB1")
	defer nodePeer.Close()

	clientA, _ := connectFakeClient(t, srv)
	defer clientA.Close()
	clientB, _ := connectFakeClient(t, srv)
	defer clientB.Close()

	var res api.RelayOpenResult
	if err := clientA.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err != nil {
		t.Fatalf("relay.open: %v", err)
	}

	// Client B does not own the channel; its frame to A's chan_id must be dropped.
	if err := clientB.SendRawFrame(rawRelayFrame(t, res.ChanID, "sessions.input", "YXR0YWNrZXI=")); err != nil {
		t.Fatalf("clientB SendRawFrame: %v", err)
	}
	select {
	case f := <-nodeFrames:
		t.Fatalf("node received a frame from a non-owning client: chan=%q body=%s", f.Route.ChanID, f.Body)
	case <-time.After(500 * time.Millisecond):
		// good: dropped
	}

	// Sanity: the owning client A can still send on its channel.
	if err := clientA.SendRawFrame(rawRelayFrame(t, res.ChanID, "sessions.input", "b3duZXI=")); err != nil {
		t.Fatalf("clientA SendRawFrame: %v", err)
	}
	select {
	case f := <-nodeFrames:
		if string(f.Body) != `"b3duZXI="` {
			t.Errorf("node got unexpected body %s", f.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node did not receive the owning client's frame")
	}
}

// TestRelayFrameFromNonOwningNodeDropped verifies the symmetric guard: a relay
// frame from a node that does NOT own the target channel is dropped, so a
// malicious node cannot inject into or tear down another node's client channel.
func TestRelayFrameFromNonOwningNodeDropped(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	node1, _ := adoptFakeNode(t, srv, "n1", "PUB1")
	defer node1.Close()
	node2, _ := adoptFakeNode(t, srv, "n2", "PUB2")
	defer node2.Close()
	client, clientFrames := connectFakeClient(t, srv)
	defer client.Close()

	var res api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err != nil {
		t.Fatalf("relay.open: %v", err)
	}

	// node2 does not own the channel; its frame to the chan_id must be dropped.
	if err := node2.SendRawFrame(rawRelayFrame(t, res.ChanID, "transcript.delta", "YXR0YWNrZXI=")); err != nil {
		t.Fatalf("node2 SendRawFrame: %v", err)
	}
	select {
	case f := <-clientFrames:
		t.Fatalf("client received a frame from a non-owning node: body=%s", f.Body)
	case <-time.After(500 * time.Millisecond):
		// good: dropped
	}

	// Sanity: the owning node1 still reaches the client.
	if err := node1.SendRawFrame(rawRelayFrame(t, res.ChanID, "transcript.delta", "b3duZXI=")); err != nil {
		t.Fatalf("node1 SendRawFrame: %v", err)
	}
	select {
	case f := <-clientFrames:
		if string(f.Body) != `"b3duZXI="` {
			t.Errorf("client got unexpected body %s", f.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive the owning node's frame")
	}
}
