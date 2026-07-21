package node

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func newE2ETestNode(t *testing.T) *Node {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("home", "home-box")
	kp, _ := e2e.GenerateKeyPair()
	d.SetIdentityKey(kp)
	return d
}

// e2eNodePair connects a fake client to a node responder over net.Pipe (standing
// in for the gateway relay) and completes the Noise handshake. It returns the
// client Channel, the client Peer (to send sealed frames), a channel of relay
// frames the node sends back, and a cleanup func.
func e2eNodePair(t *testing.T, d *Node) (cch *api.Channel, client *api.Peer, fromNode chan api.RelayFrame, cleanup func()) {
	t.Helper()
	kpClient, _ := e2e.GenerateKeyPair()
	clientConn, nodeConn := net.Pipe()

	resp := d.newRelayResponder()
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{OnRelayFrame: resp.onFrame})
	resp.peer.Store(nodePeer)

	fromNode = make(chan api.RelayFrame, 32)
	client = api.NewPeer(clientConn, api.PeerOptions{OnRelayFrame: func(f api.RelayFrame) { fromNode <- f }})

	init, msg1, err := e2e.NewInitiator(kpClient, d.identity.Public, api.ChannelPrologue(d.id, "c1"))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame("c1", msg1)
	if err := client.SendRawFrame(hf); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
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
		cch = api.NewChannel("c1", sess)
	case <-time.After(2 * time.Second):
		t.Fatal("no handshake reply from node")
	}
	cleanup = func() { client.Close(); nodePeer.Close(); resp.closeAll() }
	return cch, client, fromNode, cleanup
}

func TestResponderRequestResponse(t *testing.T) {
	d := newE2ETestNode(t)
	d.server.Handle("test.echo", func(_ context.Context, params json.RawMessage) (any, error) {
		return params, nil
	})
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()

	id := json.RawMessage("1")
	params := json.RawMessage(`{"hello":"world"}`)
	req, _ := cch.SealRequestFrame(&id, "test.echo", "home", params)
	if err := client.SendRawFrame(req); err != nil {
		t.Fatalf("send req: %v", err)
	}
	select {
	case f := <-fromNode:
		res, rpcErr, err := cch.OpenResponse(f)
		if err != nil || rpcErr != nil {
			t.Fatalf("OpenResponse err=%v rpcErr=%v", err, rpcErr)
		}
		if string(res) != string(params) {
			t.Errorf("echo = %s, want %s", res, params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response from node")
	}
}

func TestResponderErrorResponse(t *testing.T) {
	d := newE2ETestNode(t)
	d.server.Handle("test.fail", func(context.Context, json.RawMessage) (any, error) {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "boom detail"}
	})
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()

	id := json.RawMessage("2")
	req, _ := cch.SealRequestFrame(&id, "test.fail", "home", nil)
	client.SendRawFrame(req)
	select {
	case f := <-fromNode:
		if bytes.Contains(f.Raw, []byte("boom detail")) {
			t.Fatal("error detail leaked in cleartext")
		}
		_, rpcErr, err := cch.OpenResponse(f)
		if err != nil {
			t.Fatalf("OpenResponse: %v", err)
		}
		if rpcErr == nil || rpcErr.Message != "boom detail" {
			t.Errorf("rpc error = %+v", rpcErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no error response")
	}
}

func TestResponderStreamsNotifications(t *testing.T) {
	d := newE2ETestNode(t)
	d.server.Handle("test.sub", func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, ok := api.NotifierFrom(ctx)
		if !ok {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no notifier"}
		}
		go func() { _ = n.Notify("test.note", json.RawMessage(`{"n":7}`)) }()
		return nil, nil
	})
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()

	id := json.RawMessage("3")
	req, _ := cch.SealRequestFrame(&id, "test.sub", "home", nil)
	client.SendRawFrame(req)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-fromNode:
			if f.Method == "test.note" {
				p, err := cch.OpenParams(f)
				if err != nil || string(p) != `{"n":7}` {
					t.Fatalf("notification params = %s err=%v", p, err)
				}
				return
			}
			// Response frame: a real client Opens every frame in arrival order to keep
			// the Noise dec-nonce in sync. Open and discard it.
			if _, _, err := cch.OpenResponse(f); err != nil {
				t.Fatalf("open response frame: %v", err)
			}
		case <-deadline:
			t.Fatal("no test.note notification streamed over the channel")
		}
	}
}

// buildLockedResponder returns a relayResponder whose trust store authorizes only
// authorizedClientPub. Any other client is rejected (fail-closed).
func buildLockedResponder(t *testing.T, authorizedClientPub []byte) *relayResponder {
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
	genesisHead := lg.Head()
	if err := lg.AuthorizeDevice(authorizedClientPub, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(lg.Entries())
	ss := trustlog.NewSyncStore(genesisHead)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	d.trust.Store(ss)
	return d.newRelayResponder()
}

// buildOpenResponder returns a relayResponder with no trust store (open mode):
// any client's handshake establishes a channel.
func buildOpenResponder(t *testing.T) *relayResponder {
	t.Helper()
	d := newE2ETestNode(t)
	return d.newRelayResponder()
}

// runClientHandshake drives a Noise IK handshake from clientKP into r, waits
// for msg2 (authorized) or a short timeout (rejected), and reports whether a
// channel was established.
func runClientHandshake(t *testing.T, r *relayResponder, clientKP e2e.KeyPair) bool {
	t.Helper()
	const chanID = "enforce-test-chan"

	clientConn, nodeConn := net.Pipe()
	fromNode := make(chan api.RelayFrame, 8)
	clientPeer := api.NewPeer(clientConn, api.PeerOptions{
		OnRelayFrame: func(f api.RelayFrame) {
			select {
			case fromNode <- f:
			default:
			}
		},
	})
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{OnRelayFrame: r.onFrame})
	r.peer.Store(nodePeer)
	t.Cleanup(func() { clientPeer.Close(); nodePeer.Close(); r.closeAll() })

	init, msg1, err := e2e.NewInitiator(clientKP, r.static.Public, api.ChannelPrologue(r.d.id, chanID))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame(chanID, msg1)
	if err := clientPeer.SendRawFrame(hf); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	_ = init

	select {
	case <-fromNode:
		// msg2 received: node established the channel
	case <-time.After(200 * time.Millisecond):
		// no reply: node rejected the client
	}
	return r.lookup(chanID) != nil
}

func TestResponderEnforcesAuthorizedClient(t *testing.T) {
	clientKP, _ := e2e.GenerateKeyPair()

	// Authorized client → channel established.
	r := buildLockedResponder(t, clientKP.Public)
	if !runClientHandshake(t, r, clientKP) {
		t.Fatal("authorized client should establish a channel")
	}

	// Unauthorized client (different key) → no channel.
	other, _ := e2e.GenerateKeyPair()
	r2 := buildLockedResponder(t, clientKP.Public) // authorizes clientKP, not other
	if runClientHandshake(t, r2, other) {
		t.Fatal("unauthorized client must be rejected (no channel)")
	}

	// Open mode (nil trust store) → any client establishes a channel.
	rOpen := buildOpenResponder(t)
	if !runClientHandshake(t, rOpen, other) {
		t.Fatal("open-mode responder should accept any client")
	}
}

// buildDisabledLockedResponder returns a locked responder whose trust store is
// Disabled (genesis carries the disablement commitment, then Disable is called).
// authorizedClientPub is still in the chain, but enforcement must be off.
func buildDisabledLockedResponder(t *testing.T, authorizedClientPub []byte) *relayResponder {
	t.Helper()
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
	genesisHead := lg.Head()
	if err := lg.AuthorizeDevice(authorizedClientPub, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(lg.Entries())
	ss := trustlog.NewSyncStore(genesisHead)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := ss.Disable(secret, signer); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	d.trust.Store(ss)
	return d.newRelayResponder()
}

func TestResponderDisabledStoreAcceptsAll(t *testing.T) {
	authorizedKP, _ := e2e.GenerateKeyPair()
	unauthorized, _ := e2e.GenerateKeyPair()

	// Disabled store: even an unauthorized client gets a channel.
	r := buildDisabledLockedResponder(t, authorizedKP.Public)
	if !runClientHandshake(t, r, unauthorized) {
		t.Fatal("disabled store must accept unauthorized client (enforcement off)")
	}
}

// buildLocalDisabledLockedResponder returns a locked responder whose per-node
// local-disable flag is set. authorizedClientPub is the only authorized identity
// in the trust store, but enforcement must be off via the local-disable escape hatch.
func buildLocalDisabledLockedResponder(t *testing.T, authorizedClientPub []byte) *relayResponder {
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
	genesisHead := lg.Head()
	if err := lg.AuthorizeDevice(authorizedClientPub, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(lg.Entries())
	ss := trustlog.NewSyncStore(genesisHead)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	d.trust.Store(ss)
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "trustlog-chain"))
	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}
	return d.newRelayResponder()
}

func TestResponderLocalDisabledAcceptsAll(t *testing.T) {
	authorizedKP, _ := e2e.GenerateKeyPair()
	unauthorized, _ := e2e.GenerateKeyPair()

	// Local-disable flag set: even an unauthorized client gets a channel.
	r := buildLocalDisabledLockedResponder(t, authorizedKP.Public)
	if !runClientHandshake(t, r, unauthorized) {
		t.Fatal("local-disabled node must accept unauthorized client (enforcement off)")
	}

	// Sanity-check: without local-disable the same unauthorized client is rejected.
	r2 := buildLockedResponder(t, authorizedKP.Public)
	if runClientHandshake(t, r2, unauthorized) {
		t.Fatal("unauthorized client must be rejected when local-disable is not set")
	}
}
