package node

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// buildReevalNode returns a Node whose trust store authorizes clientPub.
// signer is returned so the caller can append further entries (e.g. RevokeDevice).
func buildReevalNode(t *testing.T, clientPub []byte) (*Node, trustlog.SignerKey, *trustlog.SyncStore) {
	t.Helper()
	d := newE2ETestNode(t)
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	lg, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := lg.Tip()
	if err := lg.AuthorizeDevice(clientPub, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(lg.Entries())
	ss := trustlog.NewSyncStore(genesisHash)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	d.trust.Store(ss)
	return d, signer, ss
}

// reevalHandshake creates a responder wired to d, registers it as d.activeResponder,
// drives the Noise handshake for clientKP, and returns the responder, the sealed
// client Channel, and cleanup. chanID is fixed to "reeval-test-chan".
func reevalHandshake(t *testing.T, d *Node, clientKP e2e.KeyPair) (*relayResponder, *api.Channel, *api.Peer, chan api.RelayFrame) {
	t.Helper()
	const chanID = "reeval-test-chan"

	resp := d.newRelayResponder()
	d.activeResponder.Store(resp)

	clientConn, nodeConn := net.Pipe()
	fromNode := make(chan api.RelayFrame, 8)
	clientPeer := api.NewPeer(clientConn, api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) {
			select {
			case fromNode <- f:
			default:
			}
		},
	})
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{OnRelayFrame: resp.onFrame})
	resp.peer.Store(nodePeer)
	t.Cleanup(func() { clientPeer.Close(); nodePeer.Close(); resp.closeAll() })

	init, msg1, err := e2e.NewInitiator(clientKP, resp.static.Public, api.ChannelPrologue(d.id, chanID))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame(chanID, msg1)
	if err := clientPeer.SendRawFrame(hf); err != nil {
		t.Fatalf("send handshake: %v", err)
	}

	var cch *api.Channel
	select {
	case f := <-fromNode:
		msg2, err := api.HandshakeFromFrame(f)
		if err != nil {
			t.Fatalf("HandshakeFromFrame: %v", err)
		}
		sess, err := init.Finish(msg2)
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}
		cch = api.NewChannel(chanID, sess)
	case <-time.After(2 * time.Second):
		t.Fatal("no handshake reply from node")
	}

	return resp, cch, clientPeer, fromNode
}

func TestReevaluateTrustChannels(t *testing.T) {
	const chanID = "reeval-test-chan"

	clientKP, _ := e2e.GenerateKeyPair()
	d, signer, ss := buildReevalNode(t, clientKP.Public)

	resp, cch, clientPeer, fromNode := reevalHandshake(t, d, clientKP)

	// Channel must exist immediately after handshake.
	if resp.lookup(chanID) == nil {
		t.Fatal("channel should exist after handshake")
	}

	// Revoke client X in the store.
	if _, err := ss.RevokeDevice(clientKP.Public, signer); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	// reevaluateTrustChannels should detect the revocation and close the channel.
	d.reevaluateTrustChannels()

	if resp.lookup(chanID) != nil {
		t.Fatal("revoked client's channel should be closed after reevaluate")
	}

	// Subsequent request on the closed channel must be dropped (no response).
	id := json.RawMessage("99")
	req, _ := cch.SealRequestFrame(&id, "test.echo", "home", nil)
	_ = clientPeer.SendRawFrame(req)
	select {
	case f := <-fromNode:
		t.Fatalf("response received for request on closed channel (method=%s)", f.Method)
	case <-time.After(100 * time.Millisecond):
		// dropped — correct
	}
}

func TestReevaluateDisabledStoreClosesNothing(t *testing.T) {
	const chanID = "reeval-test-chan"

	// Build a node whose trust store has clientKP revoked then disabled.
	// The Disabled() guard must short-circuit before the authorization check,
	// so even a revoked client's channel must survive reevaluate.
	clientKP, _ := e2e.GenerateKeyPair()
	d := newE2ETestNode(t)
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	secret, err := trustlog.GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("GenerateDisablementSecret: %v", err)
	}
	commitment := trustlog.DisablementCommitment(secret)
	lg, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, [][]byte{commitment})
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := lg.Tip()
	if err := lg.AuthorizeDevice(clientKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(lg.Entries())
	ss := trustlog.NewSyncStore(genesisHash)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	// Revoke before disable: a disabled log rejects further entries.
	if _, err := ss.RevokeDevice(clientKP.Public, signer); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	// Disable the store; enforcement is now off.
	if _, err := ss.Disable(secret, signer); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	d.trust.Store(ss)

	// Handshake succeeds because enforcement is off (store is Disabled).
	resp, _, _, _ := reevalHandshake(t, d, clientKP)

	if resp.lookup(chanID) == nil {
		t.Fatal("channel should exist after handshake with disabled store")
	}

	// reevaluate must be a no-op when the store is Disabled, even though the
	// client is revoked — the Disabled() guard must fire before the auth check.
	d.reevaluateTrustChannels()

	if resp.lookup(chanID) == nil {
		t.Fatal("disabled store: revoked client's channel must not be closed by reevaluate")
	}
}

func TestReevaluateLocalDisabledClosesNothing(t *testing.T) {
	const chanID = "reeval-test-chan"

	clientKP, _ := e2e.GenerateKeyPair()
	d, signer, ss := buildReevalNode(t, clientKP.Public)

	resp, _, _, _ := reevalHandshake(t, d, clientKP)

	if resp.lookup(chanID) == nil {
		t.Fatal("channel should exist after handshake")
	}

	// Revoke the client so reevaluate would close the channel without the guard.
	if _, err := ss.RevokeDevice(clientKP.Public, signer); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	// Set the local-disable flag directly (no disk path in test nodes).
	d.localDisabledFlag.Store(true)

	// reevaluate must be a no-op when local-disabled, even for a revoked client.
	d.reevaluateTrustChannels()

	if resp.lookup(chanID) == nil {
		t.Fatal("local-disabled node: revoked client's channel must not be closed by reevaluate")
	}
}
