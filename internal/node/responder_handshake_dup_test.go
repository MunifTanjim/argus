package node

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
)

// mkHandshakeFrame builds a fresh client Noise msg1 wrapped as an e2e.handshake
// relay frame for chanID.
func mkHandshakeFrame(t *testing.T, d *Node, chanID string) api.RelayFrame {
	t.Helper()
	kp, _ := e2e.GenerateKeyPair()
	_, msg1, err := e2e.NewInitiator(kp, d.identity.Public, api.ChannelPrologue(d.id, chanID))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	body, _ := json.Marshal(base64.StdEncoding.EncodeToString(msg1))
	return api.RelayFrame{Method: api.MethodE2EHandshake, Route: api.RouteHeader{ChanID: chanID}, Body: body}
}

// TestResponderDuplicateHandshakeKeepsFirstChannel verifies that a second
// handshake for a chan_id that already has a live channel does NOT replace it.
// The insert must re-check under the lock (two frames could both observe no
// existing channel before either inserts); otherwise the second overwrites the
// first's chanState without cancelling it, leaking the first channel's context
// until the uplink closes.
func TestResponderDuplicateHandshakeKeepsFirstChannel(t *testing.T) {
	d := newE2ETestNode(t)
	clientConn, nodeConn := net.Pipe()
	resp := d.newRelayResponder()
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{OnRelayFrame: resp.onFrame})
	resp.peer.Store(nodePeer)
	// Drain msg2 frames the node writes back so SendRawFrame doesn't block on the pipe.
	drain := api.NewPeer(clientConn, api.PeerOptions{OnRelayFrame: func(_ *api.Peer, _ api.RelayFrame) {}})
	defer func() { drain.Close(); nodePeer.Close(); resp.closeAll() }()

	resp.handshake(nodePeer, mkHandshakeFrame(t, d, "c1"))
	first := resp.lookup("c1")
	if first == nil {
		t.Fatal("first handshake must establish the channel")
	}

	resp.handshake(nodePeer, mkHandshakeFrame(t, d, "c1"))
	if got := resp.lookup("c1"); got != first {
		t.Fatal("a duplicate handshake must not replace (leak) the existing channel")
	}
}
