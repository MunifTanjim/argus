package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/registry"
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
// in for the gateway relay) and completes the Noise handshake.
func e2eNodePair(t *testing.T, d *Node) (cch *api.Channel, client *api.Peer, fromNode chan api.RelayFrame, cleanup func()) {
	t.Helper()
	cch, client, fromNode, _, cleanup = e2eUplinkPair(t, d, nil)
	return cch, client, fromNode, cleanup
}

// e2eUplinkPair is e2eNodePair over an uplink that serves node.identify; identify,
// when non-nil, runs as the gateway before the client handshakes.
func e2eUplinkPair(t *testing.T, d *Node, identify func(gw *api.Peer)) (cch *api.Channel, client *api.Peer, fromNode chan api.RelayFrame, resp *relayResponder, cleanup func()) {
	t.Helper()
	kpClient, _ := e2e.GenerateKeyPair()
	clientConn, nodeConn := net.Pipe()

	resp = d.newRelayResponder(nil)
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{Dispatch: d.uplinkDispatch(), OnRelayFrame: resp.onFrame})
	resp.peer.Store(nodePeer)

	fromNode = make(chan api.RelayFrame, 32)
	client = api.NewPeer(clientConn, api.PeerOptions{OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { fromNode <- f }})
	if identify != nil {
		identify(client)
	}

	init, msg1, err := e2e.NewInitiator(kpClient, d.identity.Public, api.ChannelPrologue(d.id, "c1"))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame("c1", msg1)
	if err := client.SendRawFrame(api.EncodingJSON, hf); err != nil {
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
	return cch, client, fromNode, resp, cleanup
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
	req, _ := cch.SealRequestFrame(api.EncodingJSON, &id, "test.echo", "home", params)
	if err := client.SendRawFrame(api.EncodingJSON, req); err != nil {
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
	req, _ := cch.SealRequestFrame(api.EncodingJSON, &id, "test.fail", "home", nil)
	client.SendRawFrame(api.EncodingJSON, req)
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
	req, _ := cch.SealRequestFrame(api.EncodingJSON, &id, "test.sub", "home", nil)
	client.SendRawFrame(api.EncodingJSON, req)

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
			// Response frame: open to keep the Noise dec-nonce in sync.
			if _, _, err := cch.OpenResponse(f); err != nil {
				t.Fatalf("open response frame: %v", err)
			}
		case <-deadline:
			t.Fatal("no test.note notification streamed over the channel")
		}
	}
}

func discovered(paneID string) []registry.DiscoveredSession {
	return []registry.DiscoveredSession{{HasPane: true, Server: session.TmuxServerArgus, PaneID: paneID}}
}

// awaitSessionEvent reads frames until a session.event arrives. Every frame is
// Opened in arrival order to keep the dec-nonce in sync.
func awaitSessionEvent(t *testing.T, cch *api.Channel, fromNode chan api.RelayFrame) registry.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-fromNode:
			p, err := cch.OpenParams(f)
			if err != nil {
				t.Fatalf("open frame %q: %v", f.Method, err)
			}
			if f.Method != api.MethodSessionEvent {
				continue
			}
			var ev registry.Event
			if err := json.Unmarshal(p, &ev); err != nil {
				t.Fatalf("decode session.event: %v", err)
			}
			return ev
		case <-deadline:
			t.Fatal("no session.event streamed over the E2E channel")
		}
	}
}

func TestResponderStreamsRegistrySnapshotAndEvents(t *testing.T) {
	d := newE2ETestNode(t)
	d.reg.ReconcileSessions("claude", discovered("%1"))

	cch, _, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()

	if ev := awaitSessionEvent(t, cch, fromNode); ev.Session.ID != "argus:%1" {
		t.Fatalf("snapshot event session = %q, want argus:%%1", ev.Session.ID)
	}

	d.reg.ReconcileSessions("claude", discovered("%2"))
	if ev := awaitSessionEvent(t, cch, fromNode); ev.Session.ID != "argus:%2" {
		t.Fatalf("live event session = %q, want argus:%%2", ev.Session.ID)
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
	genesisHash := lg.Tip()
	if err := lg.AuthorizeDevice(authorizedClientPub, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(lg.Entries())
	ss := trustlog.NewSyncStore(genesisHash)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	d.trust.Store(ss)
	return d.newRelayResponder(nil)
}

// buildOpenResponder returns a relayResponder with no trust store (open/TOFU mode).
func buildOpenResponder(t *testing.T) *relayResponder {
	t.Helper()
	d := newE2ETestNode(t)
	return d.newRelayResponder(nil)
}

// runClientHandshake drives a Noise IK handshake into r and reports whether a
// channel was established.
func runClientHandshake(t *testing.T, r *relayResponder, clientKP e2e.KeyPair) bool {
	t.Helper()
	const chanID = "enforce-test-chan"

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
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{OnRelayFrame: r.onFrame})
	r.peer.Store(nodePeer)
	t.Cleanup(func() { clientPeer.Close(); nodePeer.Close(); r.closeAll() })

	init, msg1, err := e2e.NewInitiator(clientKP, r.static.Public, api.ChannelPrologue(r.d.id, chanID))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame(chanID, msg1)
	if err := clientPeer.SendRawFrame(api.EncodingJSON, hf); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	_ = init

	select {
	case <-fromNode:
	case <-time.After(200 * time.Millisecond):
	}
	return r.lookup(chanID) != nil
}

func TestResponderEnforcesAuthorizedClient(t *testing.T) {
	clientKP, _ := e2e.GenerateKeyPair()

	r := buildLockedResponder(t, clientKP.Public)
	if !runClientHandshake(t, r, clientKP) {
		t.Fatal("authorized client should establish a channel")
	}

	other, _ := e2e.GenerateKeyPair()
	r2 := buildLockedResponder(t, clientKP.Public)
	if runClientHandshake(t, r2, other) {
		t.Fatal("unauthorized client must be rejected (no channel)")
	}

	rOpen := buildOpenResponder(t)
	if !runClientHandshake(t, rOpen, other) {
		t.Fatal("open-mode responder should accept any client")
	}
}

// buildDisabledLockedResponder returns a locked responder whose trust store is
// Disabled; enforcement must be off.
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
	genesisHash := lg.Tip()
	if err := lg.AuthorizeDevice(authorizedClientPub, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(lg.Entries())
	ss := trustlog.NewSyncStore(genesisHash)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := ss.Disable(secret, signer); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	d.trust.Store(ss)
	return d.newRelayResponder(nil)
}

func TestResponderDisabledStoreAcceptsAll(t *testing.T) {
	authorizedKP, _ := e2e.GenerateKeyPair()
	unauthorized, _ := e2e.GenerateKeyPair()

	r := buildDisabledLockedResponder(t, authorizedKP.Public)
	if !runClientHandshake(t, r, unauthorized) {
		t.Fatal("disabled store must accept unauthorized client (enforcement off)")
	}
}

func TestResponderPlaintextChannelNoHandshake(t *testing.T) {
	d := newE2ETestNode(t) // e2ee is false by default
	d.server.Handle("test.echo", func(_ context.Context, params json.RawMessage) (any, error) {
		return params, nil
	})

	r := d.newRelayResponder(nil)

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
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{OnRelayFrame: r.onFrame})
	r.peer.Store(nodePeer)
	t.Cleanup(func() { clientPeer.Close(); nodePeer.Close(); r.closeAll() })

	ch := api.NewPlainChannel("c1")
	id := json.RawMessage(`1`)
	frame, err := ch.SealRequestFrame(api.EncodingJSON, &id, "test.echo", d.id, json.RawMessage(`{"hello":"plain"}`))
	if err != nil {
		t.Fatalf("SealRequestFrame: %v", err)
	}
	if err := clientPeer.SendRawFrame(api.EncodingJSON, frame); err != nil {
		t.Fatalf("send req: %v", err)
	}

	select {
	case f := <-fromNode:
		res, rpcErr, err := ch.OpenResponse(f)
		if err != nil || rpcErr != nil {
			t.Fatalf("OpenResponse err=%v rpcErr=%v", err, rpcErr)
		}
		if string(res) != `{"hello":"plain"}` {
			t.Errorf("echo = %s, want {\"hello\":\"plain\"}", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response from node in plaintext mode")
	}

	cs := r.lookup("c1")
	if cs == nil {
		t.Fatal("plaintext channel must be lazily created on first frame")
	}
	if cs.clientStatic != nil {
		t.Error("plaintext channel must not have a clientStatic key")
	}
}

// gatewayIdentify returns an e2eUplinkPair identify hook that calls node.identify
// on the uplink with params, as a gateway does (nil params: an old gateway).
func gatewayIdentify(t *testing.T, params any) func(*api.Peer) {
	return func(gw *api.Peer) {
		if err := gw.Call(api.MethodNodeIdentify, params, nil); err != nil {
			t.Fatalf("uplink node.identify: %v", err)
		}
	}
}

var cborCaps = api.IdentifyParams{Capabilities: &api.PeerCapabilities{CBOR: true}}

func sendRelayRequest(t *testing.T, cch *api.Channel, client *api.Peer, enc api.Encoding, id, method string, params []byte) {
	t.Helper()
	rid := json.RawMessage(id)
	req, err := cch.SealRequestFrame(enc, &rid, method, "home", params)
	if err != nil {
		t.Fatalf("SealRequestFrame: %v", err)
	}
	if err := client.SendRawFrame(enc, req); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
}

// awaitRelayFrame opens every frame in arrival order (keeping the dec-nonce in
// sync) and returns the first one with method ("" for a response) and its inner bytes.
func awaitRelayFrame(t *testing.T, cch *api.Channel, fromNode chan api.RelayFrame, method string) (api.RelayFrame, []byte, *api.RPCError) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-fromNode:
			var inner []byte
			var rpcErr *api.RPCError
			var err error
			if f.Method == "" {
				inner, rpcErr, err = cch.OpenResponse(f)
			} else {
				inner, err = cch.OpenParams(f)
			}
			if err != nil {
				t.Fatalf("open frame %q: %v", f.Method, err)
			}
			if f.Method == method {
				return f, inner, rpcErr
			}
		case <-deadline:
			t.Fatalf("no %q frame from node", method)
			return api.RelayFrame{}, nil, nil
		}
	}
}

// handleTestEmit registers test.emit, which pushes one terminal.output on the
// calling channel.
func handleTestEmit(d *Node) {
	d.server.Handle("test.emit", func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, _ := api.NotifierFrom(ctx)
		return nil, n.Notify(api.MethodTerminalOutput, api.TerminalOutput{TermID: "t1", Data: []byte{0, 0xff, 'x'}})
	})
}

// clientIdentifyThenEmit sends node.identify (params may be nil) on the channel,
// then test.emit, and returns the terminal.output frame.
func clientIdentifyThenEmit(t *testing.T, cch *api.Channel, client *api.Peer, fromNode chan api.RelayFrame, params []byte) api.RelayFrame {
	t.Helper()
	sendRelayRequest(t, cch, client, api.EncodingJSON, "1", api.MethodNodeIdentify, params)
	if _, _, rpcErr := awaitRelayFrame(t, cch, fromNode, ""); rpcErr != nil {
		t.Fatalf("channel node.identify: %v", rpcErr)
	}
	sendRelayRequest(t, cch, client, api.EncodingJSON, "2", "test.emit", nil)
	f, inner, _ := awaitRelayFrame(t, cch, fromNode, api.MethodTerminalOutput)
	if _, _, rpcErr := awaitRelayFrame(t, cch, fromNode, ""); rpcErr != nil {
		t.Fatalf("test.emit: %v", rpcErr)
	}
	var out api.TerminalOutput
	var err error
	if f.Enc == api.EncodingCBOR {
		err = api.CBORUnmarshal(inner, &out)
	} else {
		err = json.Unmarshal(inner, &out)
	}
	if err != nil || out.TermID != "t1" || !bytes.Equal(out.Data, []byte{0, 0xff, 'x'}) {
		t.Fatalf("terminal.output = %+v (err %v)", out, err)
	}
	return f
}

var clientCBORParams = json.RawMessage(`{"capabilities":{"cbor":true}}`)

func TestNodeIdentifyRecordsClientCBOR(t *testing.T) {
	d := newE2ETestNode(t)
	handleTestEmit(d)

	cch, client, fromNode, _, cleanup := e2eUplinkPair(t, d, gatewayIdentify(t, cborCaps))
	defer cleanup()
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, clientCBORParams); f.Enc != api.EncodingCBOR {
		t.Fatal("terminal.output after client declared cbor: want CBOR")
	}

	cch2, client2, fromNode2, _, cleanup2 := e2eUplinkPair(t, d, gatewayIdentify(t, cborCaps))
	defer cleanup2()
	if f := clientIdentifyThenEmit(t, cch2, client2, fromNode2, nil); f.Enc != api.EncodingJSON {
		t.Fatal("terminal.output after identify without params: want JSON")
	}
}

func TestNodeNoCBORBehindOldGateway(t *testing.T) {
	d := newE2ETestNode(t)
	handleTestEmit(d)

	cch, client, fromNode, _, cleanup := e2eUplinkPair(t, d, gatewayIdentify(t, nil))
	defer cleanup()
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, clientCBORParams); f.Enc != api.EncodingJSON {
		t.Fatal("terminal.output behind a gateway that never declared cbor: want JSON")
	}
}

func TestUplinkCBORIsPerUplink(t *testing.T) {
	d := newE2ETestNode(t)
	handleTestEmit(d)

	cch, client, fromNode, _, cleanup := e2eUplinkPair(t, d, gatewayIdentify(t, cborCaps))
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, clientCBORParams); f.Enc != api.EncodingCBOR {
		t.Fatal("first uplink: want CBOR")
	}
	cleanup()

	cch2, client2, fromNode2, _, cleanup2 := e2eUplinkPair(t, d, gatewayIdentify(t, nil))
	defer cleanup2()
	if f := clientIdentifyThenEmit(t, cch2, client2, fromNode2, clientCBORParams); f.Enc != api.EncodingJSON {
		t.Fatal("second uplink identified without params: want JSON")
	}
}

func TestNodeCBORInputDispatched(t *testing.T) {
	d := newE2ETestNode(t)
	cch, client, fromNode, resp, cleanup := e2eUplinkPair(t, d, gatewayIdentify(t, cborCaps))
	defer cleanup()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	n, _ := api.NotifierFrom(resp.lookup("c1").ctx)
	ct := newConnTerms()
	ct.m["t1"] = &term{pty: pw, termID: "t1"}
	d.termsMu.Lock()
	d.terms[n] = ct
	d.termsMu.Unlock()

	data := []byte{0, 0xff, '\r'}
	params, _ := api.CBORMarshal(api.TerminalInputParams{TermID: "t1", Data: data})
	sendRelayRequest(t, cch, client, api.EncodingCBOR, "5", api.MethodTerminalInput, params)

	f, result, rpcErr := awaitRelayFrame(t, cch, fromNode, "")
	if rpcErr != nil {
		t.Fatalf("terminal.input: %v", rpcErr)
	}
	if f.Enc != api.EncodingCBOR || !bytes.Equal(result, []byte{0xf6}) {
		t.Fatalf("response enc=%v result=%x, want CBOR null", f.Enc, result)
	}
	got := make([]byte, len(data))
	if _, err := io.ReadFull(pr, got); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("pty got %x (err %v), want %x", got, err, data)
	}
}

func TestNodeCBORRequestForNonCBORMethodRejected(t *testing.T) {
	d := newE2ETestNode(t)
	cch, client, fromNode, _, cleanup := e2eUplinkPair(t, d, gatewayIdentify(t, cborCaps))
	defer cleanup()

	// {1: 2} is well-formed CBOR that fails to decode into map[string]any, so
	// any attempt to decode the params would surface as "invalid params".
	sendRelayRequest(t, cch, client, api.EncodingCBOR, "6", api.MethodSessionsList, []byte{0xa1, 0x01, 0x02})
	f, _, rpcErr := awaitRelayFrame(t, cch, fromNode, "")
	want := "cbor not supported for " + api.MethodSessionsList
	if f.Enc != api.EncodingCBOR || rpcErr == nil || rpcErr.Code != api.CodeInvalidRequest || rpcErr.Message != want {
		t.Fatalf("response enc=%v err=%v, want CBOR %d %q", f.Enc, rpcErr, api.CodeInvalidRequest, want)
	}
}

func TestNodeSocketCBORInputDispatched(t *testing.T) {
	d := newE2ETestNode(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	d.server.Handle("test.term", func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, _ := api.NotifierFrom(ctx)
		ct := newConnTerms()
		ct.m["t1"] = &term{pty: pw, termID: "t1"}
		d.termsMu.Lock()
		d.terms[n] = ct
		d.termsMu.Unlock()
		return nil, nil
	})

	ca, cb := net.Pipe()
	go d.server.ServeConn(cb)
	client := api.NewPeer(ca, api.PeerOptions{})
	defer client.Close()
	if err := client.Call("test.term", nil, nil); err != nil {
		t.Fatalf("test.term: %v", err)
	}
	client.SetRemoteCBOR(true)

	data := []byte{0, 0xff, '\r'}
	if err := client.Call(api.MethodTerminalInput, api.TerminalInputParams{TermID: "t1", Data: data}, nil); err != nil {
		t.Fatalf("terminal.input: %v", err)
	}
	got := make([]byte, len(data))
	if _, err := io.ReadFull(pr, got); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("pty got %x (err %v), want %x", got, err, data)
	}
}

func TestNodeReidentifyKeepsClientCBOR(t *testing.T) {
	d := newE2ETestNode(t)
	handleTestEmit(d)

	cch, client, fromNode, _, cleanup := e2eUplinkPair(t, d, gatewayIdentify(t, cborCaps))
	defer cleanup()
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, clientCBORParams); f.Enc != api.EncodingCBOR {
		t.Fatal("terminal.output after client declared cbor: want CBOR")
	}
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, nil); f.Enc != api.EncodingCBOR {
		t.Fatal("terminal.output after re-identify without params: want CBOR")
	}
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, json.RawMessage(`{}`)); f.Enc != api.EncodingCBOR {
		t.Fatal("terminal.output after re-identify without capabilities: want CBOR")
	}
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, json.RawMessage(`{"capabilities":{"cbor":false}}`)); f.Enc != api.EncodingJSON {
		t.Fatal("terminal.output after identify with cbor false: want JSON")
	}
}

func TestNodeIdentifyToleratesMalformedParams(t *testing.T) {
	d := newE2ETestNode(t)
	handleTestEmit(d)

	cch, client, fromNode, _, cleanup := e2eUplinkPair(t, d, gatewayIdentify(t, cborCaps))
	defer cleanup()
	clientIdentifyThenEmit(t, cch, client, fromNode, clientCBORParams)

	sendRelayRequest(t, cch, client, api.EncodingJSON, "3", api.MethodNodeIdentify, json.RawMessage(`{"capabilities":"nope"}`))
	_, inner, rpcErr := awaitRelayFrame(t, cch, fromNode, "")
	if rpcErr != nil {
		t.Fatalf("node.identify with malformed params: %v", rpcErr)
	}
	var res api.IdentifyResult
	if err := json.Unmarshal(inner, &res); err != nil || res.ID != d.id {
		t.Fatalf("identify result = %s (err %v), want id %q", inner, err, d.id)
	}
	if f := clientIdentifyThenEmit(t, cch, client, fromNode, nil); f.Enc != api.EncodingCBOR {
		t.Fatal("terminal.output after malformed identify: want CBOR unchanged")
	}
}

// rosterKey puts id in d's roster as online with the given identity key.
func rosterKey(d *Node, id string, pub []byte) {
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{
		ID: id, Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(pub),
	}})
}

// gatewayUplink wires r to a fake gateway over net.Pipe the way runUplink does
// (gateway notifications and relay frames on one read loop) and returns the
// gateway end plus the frames the node sends back.
func gatewayUplink(t *testing.T, r *relayResponder) (gw *api.Peer, fromNode chan api.RelayFrame) {
	t.Helper()
	gwConn, nodeConn := net.Pipe()
	fromNode = make(chan api.RelayFrame, 32)
	gw = api.NewPeer(gwConn, api.PeerOptions{OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { fromNode <- f }})
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch:     r.d.uplinkDispatch(),
		OnNotify:     r.d.uplinkNotify(r),
		OnRelayFrame: r.onFrame,
	})
	r.peer.Store(nodePeer)
	t.Cleanup(func() { gw.Close(); nodePeer.Close(); r.closeAll() })
	return gw, fromNode
}

// announcePeer sends relay.peer_opened for chanID from the fake gateway.
func announcePeer(t *testing.T, gw *api.Peer, chanID, fromNode string) {
	t.Helper()
	if err := gw.Notify(api.MethodRelayPeerOpened, api.RelayPeerOpenedParams{ChanID: chanID, FromNode: fromNode}); err != nil {
		t.Fatalf("notify peer_opened: %v", err)
	}
}

// handshakeOver completes a Noise handshake on chanID through gw as clientKP.
func handshakeOver(t *testing.T, r *relayResponder, gw *api.Peer, fromNode chan api.RelayFrame, chanID string, clientKP e2e.KeyPair) *api.Channel {
	t.Helper()
	init, msg1, err := e2e.NewInitiator(clientKP, r.static.Public, api.ChannelPrologue(r.d.id, chanID))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame(chanID, msg1)
	if err := gw.SendRawFrame(api.EncodingJSON, hf); err != nil {
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
		return api.NewChannel(chanID, sess)
	case <-time.After(2 * time.Second):
		t.Fatal("no handshake reply from node")
	}
	return nil
}

// callOver sends one request on ch and returns its result or RPC error,
// skipping (but opening, to keep the nonce in sync) notification frames.
func callOver(t *testing.T, ch *api.Channel, gw *api.Peer, fromNode chan api.RelayFrame, nodeID, method string, params json.RawMessage) (json.RawMessage, *api.RPCError) {
	t.Helper()
	id := json.RawMessage("77")
	req, err := ch.SealRequestFrame(api.EncodingJSON, &id, method, nodeID, params)
	if err != nil {
		t.Fatalf("SealRequestFrame: %v", err)
	}
	if err := gw.SendRawFrame(api.EncodingJSON, req); err != nil {
		t.Fatalf("send req: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-fromNode:
			if f.Method != "" {
				if _, err := ch.OpenParams(f); err != nil {
					t.Fatalf("open notification %q: %v", f.Method, err)
				}
				continue
			}
			res, rpcErr, err := ch.OpenResponse(f)
			if err != nil {
				t.Fatalf("OpenResponse: %v", err)
			}
			return res, rpcErr
		case <-deadline:
			t.Fatalf("no response to %s", method)
		}
	}
}

func TestPeerOpenedChannelGetsPeerDispatch(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, _ := twoNodes(t, e2ee)
		err := a.peers.Load().call(peerCtx(t), "b", api.MethodOrchSessionCurrent, api.CallerParams{}, nil)
		if isCode(err, api.CodeMethodNotFound) {
			t.Fatalf("orch.session.current over a node-opened channel: %v", err)
		}
		// lock.* and server.stop stay local-only on peer channels.
		for _, m := range []string{"lock.status", api.MethodServerStop} {
			if err := a.peers.Load().call(peerCtx(t), "b", m, nil, nil); !isCode(err, api.CodeMethodNotFound) {
				t.Fatalf("%s over a node-opened channel: err = %v, want method not found", m, err)
			}
		}
	})
}

func TestClientChannelBlocksOrch(t *testing.T) {
	d := newE2ETestNode(t)
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()
	for _, m := range []string{api.MethodOrchSessionList, api.MethodOrchMessageSend, api.MethodOrchInteractionRespond, api.MethodOrchPeerDeliver} {
		_, rpcErr := callOver(t, cch, client, fromNode, d.id, m, json.RawMessage(`{}`))
		if rpcErr == nil || rpcErr.Code != api.CodeMethodNotFound {
			t.Fatalf("%s over a client channel: rpcErr = %v, want method not found", m, rpcErr)
		}
	}
}

func TestClientChannelServesMessageList(t *testing.T) {
	d := newE2ETestNode(t)
	cch, client, fromNode, cleanup := e2eNodePair(t, d)
	defer cleanup()
	_, rpcErr := callOver(t, cch, client, fromNode, d.id, api.MethodMessageList, json.RawMessage(`{}`))
	if rpcErr != nil && rpcErr.Code == api.CodeMethodNotFound {
		t.Fatalf("message.list over a client channel: rpcErr = %v", rpcErr)
	}
}

func TestPeerOpenedKeyMismatchFallsBack(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	rosterKey(d, "b", mustKeyPair(t).Public) // b's roster key is not the opener's key

	announcePeer(t, gw, "c1", "b")
	ch := handshakeOver(t, r, gw, fromNode, "c1", mustKeyPair(t))
	cs := r.lookup("c1")
	if cs == nil {
		t.Fatal("key mismatch must fall back to a client channel, not drop it")
	}
	if r.channelPeer(cs) != "" {
		t.Fatal("key mismatch must serve the channel as a client channel")
	}
	if _, rpcErr := callOver(t, ch, gw, fromNode, d.id, api.MethodOrchSessionCurrent, json.RawMessage(`{}`)); rpcErr == nil || rpcErr.Code != api.CodeMethodNotFound {
		t.Fatalf("orch.session.current after key mismatch: rpcErr = %v, want method not found", rpcErr)
	}
	if r.pendingPeerCountForTest() != 0 {
		t.Fatal("peer_opened record must be consumed by the handshake")
	}

	// Contrast: the matching key does get peer dispatch.
	bKP := mustKeyPair(t)
	rosterKey(d, "b", bKP.Public)
	announcePeer(t, gw, "c2", "b")
	ch2 := handshakeOver(t, r, gw, fromNode, "c2", bKP)
	if cs := r.lookup("c2"); cs == nil || r.channelPeer(cs) != "b" {
		t.Fatalf("matching key: chan state = %+v, want a peer channel from b", cs)
	}
	if _, rpcErr := callOver(t, ch2, gw, fromNode, d.id, api.MethodOrchSessionCurrent, json.RawMessage(`{}`)); rpcErr != nil && rpcErr.Code == api.CodeMethodNotFound {
		t.Fatalf("orch.session.current with matching key: %v", rpcErr)
	}

	// The roster key for b changes: the live channel loses peer dispatch.
	rosterKey(d, "b", mustKeyPair(t).Public)
	if _, rpcErr := callOver(t, ch2, gw, fromNode, d.id, api.MethodOrchSessionCurrent, json.RawMessage(`{}`)); rpcErr == nil || rpcErr.Code != api.CodeMethodNotFound {
		t.Fatalf("orch.session.current after roster key change: rpcErr = %v, want method not found", rpcErr)
	}
}

func TestPeerOpenedLockedUnauthorizedRejected(t *testing.T) {
	bKP := mustKeyPair(t)
	r := buildLockedResponder(t, mustKeyPair(t).Public) // b's key is not authorized
	rosterKey(r.d, "b", bKP.Public)
	r.notePeerOpened("enforce-test-chan", "b", "")
	if runClientHandshake(t, r, bKP) {
		t.Fatal("locked store without the opener's key must reject the channel")
	}
	if r.pendingPeerCountForTest() != 0 {
		t.Fatal("rejected handshake must still consume the peer_opened record")
	}
}

func TestPeerOpenedPlaintext(t *testing.T) {
	d := newE2ETestNode(t) // plaintext
	d.server.Handle("test.peer", func(ctx context.Context, _ json.RawMessage) (any, error) {
		id, ok := peerNodeFrom(ctx)
		return map[string]any{"id": id, "ok": ok}, nil
	})
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)

	announcePeer(t, gw, "p1", "b")
	res, rpcErr := callOver(t, api.NewPlainChannel("p1"), gw, fromNode, d.id, "test.peer", nil)
	if rpcErr != nil {
		t.Fatalf("test.peer: %v", rpcErr)
	}
	if string(res) != `{"id":"b","ok":true}` {
		t.Fatalf("peerNodeFrom = %s, want b", res)
	}

	// A client channel (no peer_opened) carries no peer node.
	res, rpcErr = callOver(t, api.NewPlainChannel("p2"), gw, fromNode, d.id, "test.peer", nil)
	if rpcErr != nil {
		t.Fatalf("test.peer: %v", rpcErr)
	}
	if string(res) != `{"id":"","ok":false}` {
		t.Fatalf("client channel peerNodeFrom = %s, want none", res)
	}
}

func TestPeerOpenedRecordsDoNotLeak(t *testing.T) {
	d := newE2ETestNode(t)
	r := d.newRelayResponder(nil)
	r.notePeerOpened("x", "b", "")
	r.closeChan("x") // relay.closed before any frame
	if n := r.pendingPeerCountForTest(); n != 0 {
		t.Fatalf("pending after close = %d, want 0", n)
	}
	for i := 0; i < maxPendingPeerOpened+10; i++ {
		r.notePeerOpened("c"+strconv.Itoa(i), "b", "")
	}
	if n := r.pendingPeerCountForTest(); n > maxPendingPeerOpened {
		t.Fatalf("pending = %d, want <= %d", n, maxPendingPeerOpened)
	}
}

func TestPeerChannelGetsNoRegistryStream(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	bKP := mustKeyPair(t)
	rosterKey(d, "b", bKP.Public)

	cch := handshakeOver(t, r, gw, fromNode, "c1", mustKeyPair(t))
	announcePeer(t, gw, "p1", "b")
	handshakeOver(t, r, gw, fromNode, "p1", bKP)
	if cs := r.lookup("p1"); cs == nil || r.channelPeer(cs) != "b" {
		t.Fatalf("p1 = %+v, want a peer channel from b", cs)
	}

	d.reg.ReconcileSessions("claude", discovered("%1"))
	d.notifyProjectsChanged()

	var gotEvent, gotProjects bool
	var onPeer []string
	deadline := time.After(2 * time.Second)
	for !gotEvent || !gotProjects {
		select {
		case f := <-fromNode:
			if f.Route.ChanID != "c1" {
				onPeer = append(onPeer, f.Method)
				continue
			}
			if _, err := cch.OpenParams(f); err != nil {
				t.Fatalf("open %q: %v", f.Method, err)
			}
			gotEvent = gotEvent || f.Method == api.MethodSessionEvent
			gotProjects = gotProjects || f.Method == api.MethodProjectChanged
		case <-deadline:
			t.Fatalf("client channel: session.event=%v project.changed=%v", gotEvent, gotProjects)
		}
	}
	settle := time.After(200 * time.Millisecond)
	for done := false; !done; {
		select {
		case f := <-fromNode:
			if f.Route.ChanID != "c1" {
				onPeer = append(onPeer, f.Method)
			}
		case <-settle:
			done = true
		}
	}
	if len(onPeer) != 0 {
		t.Fatalf("peer channel got notifications %v", onPeer)
	}
}

func TestPeerChannelPromotedWhenRosterCatchesUp(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	bKP := mustKeyPair(t)

	// b is not in the roster yet when its channel is opened.
	announcePeer(t, gw, "p1", "b")
	ch := handshakeOver(t, r, gw, fromNode, "p1", bKP)
	if _, rpcErr := callOver(t, ch, gw, fromNode, d.id, api.MethodOrchSessionCurrent, json.RawMessage(`{}`)); rpcErr == nil || rpcErr.Code != api.CodeMethodNotFound {
		t.Fatalf("orch.session.current before b is in the roster: rpcErr = %v, want method not found", rpcErr)
	}

	rosterKey(d, "b", bKP.Public)
	if _, rpcErr := callOver(t, ch, gw, fromNode, d.id, api.MethodOrchSessionCurrent, json.RawMessage(`{}`)); rpcErr != nil && rpcErr.Code == api.CodeMethodNotFound {
		t.Fatalf("orch.session.current after b joins the roster: %v", rpcErr)
	}
}
