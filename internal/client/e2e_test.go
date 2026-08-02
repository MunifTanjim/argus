package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// fakeGatewayNode is one peer that plays BOTH the gateway (answers nodes.list /
// relay.open as normal RPCs) and the node (terminates the E2E channel in
// OnRelayFrame: handshake via e2e.Respond, then decrypt→handle→seal). The
// E2EClient talks to a single peer, so collapsing the two roles is faithful.
type fakeGatewayNode struct {
	nodeID  string
	nodeKey e2e.KeyPair
	peer    *api.Peer
	nodeCh  *api.Channel // set after handshake; only touched on the read loop
	// handle is invoked with (method, opened params) and returns (result, rpcErr,
	// preNotify) — preNotify (if non-nil) is sealed as a notification BEFORE the response.
	handle func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote)
	// postHandshake, when set, is sealed and sent immediately after msg2 — the node
	// pushing state the moment the channel exists, before any client request.
	postHandshake *fakeNote
}

type fakeNote struct {
	method string
	params json.RawMessage
}

func newFakeGatewayNode(t *testing.T, nodeID string) (*fakeGatewayNode, net.Conn) {
	t.Helper()
	kp, _ := e2e.GenerateKeyPair()
	gwConn, clientConn := net.Pipe()
	f := &fakeGatewayNode{nodeID: nodeID, nodeKey: kp}
	f.peer = api.NewPeer(gwConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			switch method {
			case api.MethodNodesList:
				return api.NodesListResult{Nodes: []api.NodeDescriptor{{
					ID: nodeID, Label: nodeID + "-box", Online: true,
					IdentityPubKey: base64.StdEncoding.EncodeToString(kp.Public),
				}}}, nil
			case api.MethodRelayOpen:
				return api.RelayOpenResult{ChanID: "c1"}, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
		},
		OnRelayFrame: f.onFrame,
	})
	return f, clientConn
}

func (f *fakeGatewayNode) onFrame(_ *api.Peer, fr api.RelayFrame) {
	if fr.Method == api.MethodE2EHandshake {
		msg1, err := api.HandshakeFromFrame(fr)
		if err != nil {
			return
		}
		sess, _, msg2, err := e2e.Respond(f.nodeKey, api.ChannelPrologue(f.nodeID, fr.Route.ChanID), msg1)
		if err != nil {
			return
		}
		f.nodeCh = api.NewChannel(fr.Route.ChanID, sess)
		hf, _ := api.MarshalHandshakeFrame(fr.Route.ChanID, msg2)
		_ = f.peer.SendRawFrame(hf)
		if n := f.postHandshake; n != nil {
			nf, _ := f.nodeCh.SealNotificationFrame(n.method, api.RouteHeader{}, n.params)
			_ = f.peer.SendRawFrame(nf)
		}
		return
	}
	if f.nodeCh == nil {
		return
	}
	params, err := f.nodeCh.OpenParams(fr)
	if err != nil {
		return
	}
	result, rpcErr, note := f.handle(fr.Method, params)
	if note != nil { // seal the notification BEFORE the response (arrival order)
		nf, _ := f.nodeCh.SealNotificationFrame(note.method, api.RouteHeader{}, note.params)
		_ = f.peer.SendRawFrame(nf)
	}
	rf, _ := f.nodeCh.SealResponseFrame(fr.ID, result, rpcErr)
	_ = f.peer.SendRawFrame(rf)
}

// fakeNode is one node the fakeMultiGateway terminates a channel for.
type fakeNode struct {
	id     string
	key    e2e.KeyPair
	ch     *api.Channel // per-channel session, set at handshake (single read loop, no lock)
	handle func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote)
	// postHandshake, when set, is sealed right after msg2 — a real node pushes its
	// registry snapshot the moment the channel exists.
	postHandshake *fakeNote
	// beforeMsg2, when set, runs while the client is still blocked waiting for the
	// handshake reply — the window in which its channel is not yet registered.
	beforeMsg2 func()
	// beacon state for courier-dedupe tests; zero-value means no beacon configured.
	beaconPub  []byte
	beaconPriv ed25519.PrivateKey
	beaconCtr  uint64
}

// fakeMultiGateway is one peer playing the gateway for several nodes: nodes.list
// advertises all of them, relay.open{node_id} allocates a chan_id bound to that
// node, and OnRelayFrame routes handshake/sealed frames to the right node by chan_id.
// Set chain before Connect to serve a trust-log chain from trustlog.sync.
type fakeMultiGateway struct {
	peer       *api.Peer
	mu         sync.Mutex           // guards nodes/order/deliveries: addNode races the peer read loop
	nodes      map[string]*fakeNode // node id -> node
	order      []*fakeNode          // stable nodes.list order
	byChan     map[string]*fakeNode // chan_id -> node
	nextCh     int
	chain      []byte // served by trustlog.sync; nil = empty result
	deliveries int    // total beacon.deliver calls received across all nodes
}

func newFakeMultiGateway(t *testing.T, nodes ...*fakeNode) (*fakeMultiGateway, net.Conn) {
	t.Helper()
	gwConn, clientConn := net.Pipe()
	g := &fakeMultiGateway{
		nodes:  map[string]*fakeNode{},
		byChan: map[string]*fakeNode{},
	}
	for _, n := range nodes {
		g.nodes[n.id] = n
		g.order = append(g.order, n)
	}
	g.peer = api.NewPeer(gwConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case api.MethodNodesList:
				var descs []api.NodeDescriptor
				for _, n := range g.snapshotNodes() {
					descs = append(descs, g.nodeDescriptor(n))
				}
				return api.NodesListResult{Nodes: descs}, nil
			case api.MethodRelayOpen:
				var p api.RelayOpenParams
				_ = json.Unmarshal(params, &p)
				n := g.node(p.NodeID)
				if n == nil {
					return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node"}
				}
				g.nextCh++
				chID := "c" + strconv.Itoa(g.nextCh)
				g.byChan[chID] = n
				return api.RelayOpenResult{ChanID: chID}, nil
			case api.MethodTrustLogSync:
				g.mu.Lock()
				ch := g.chain
				g.mu.Unlock()
				if ch == nil {
					return api.TrustLogSyncResult{}, nil
				}
				entries, err := trustlog.ChainEntries(ch)
				if err != nil {
					return api.TrustLogSyncResult{}, nil
				}
				return api.TrustLogSyncResult{Entries: entries}, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
		},
		OnRelayFrame: g.onFrame,
	})
	return g, clientConn
}

func (g *fakeMultiGateway) snapshotNodes() []*fakeNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*fakeNode(nil), g.order...)
}

func (g *fakeMultiGateway) node(id string) *fakeNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.nodes[id]
}

func (g *fakeMultiGateway) nodeDescriptor(n *fakeNode) api.NodeDescriptor {
	return api.NodeDescriptor{
		ID: n.id, Label: n.id + "-box", Online: true,
		IdentityPubKey: base64.StdEncoding.EncodeToString(n.key.Public),
	}
}

// beaconDeliveries returns the total number of beacon.deliver calls received
// by all nodes managed by this gateway. Guarded by mu.
func (g *fakeMultiGateway) beaconDeliveries() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deliveries
}

// bumpBeaconCounter increments the beacon counter for the first node in order.
// Call ingestBeaconFromDescriptor(gw.descriptor(0)) afterward to update the
// client's stored beacon before the next deliverBeacons call.
func (g *fakeMultiGateway) bumpBeaconCounter(t *testing.T) {
	t.Helper()
	g.mu.Lock()
	if len(g.order) > 0 {
		g.order[0].beaconCtr++
	}
	g.mu.Unlock()
}

// courierTestTip is the fixed tip used by descriptor when constructing beacons
// for courier-dedupe tests.
var courierTestTip = bytes.Repeat([]byte{0xab}, 32)

// descriptor returns a NodeDescriptor for g.order[i] with a signed beacon
// built from the node's beacon key and current counter. Used by courier tests.
func (g *fakeMultiGateway) descriptor(i int) api.NodeDescriptor {
	g.mu.Lock()
	n := g.order[i]
	pub := n.beaconPub
	priv := n.beaconPriv
	ctr := n.beaconCtr
	g.mu.Unlock()
	b := api.SignBeacon(priv, pub, courierTestTip, 1, ctr)
	return api.NodeDescriptor{
		ID:             n.id,
		IdentityPubKey: base64.StdEncoding.EncodeToString(n.key.Public),
		BeaconPubKey:   base64.StdEncoding.EncodeToString(pub),
		Beacon:         &b,
	}
}

// addNode makes n visible to nodes.list and relay.open, as a node joining the
// gateway mid-session does.
func (g *fakeMultiGateway) addNode(n *fakeNode) {
	g.mu.Lock()
	g.nodes[n.id] = n
	g.order = append(g.order, n)
	g.mu.Unlock()
}

// setChain updates the chain served by trustlog.sync. Safe to call from test
// goroutines while the gateway peer loop is running.
func (g *fakeMultiGateway) setChain(chain []byte) {
	g.mu.Lock()
	g.chain = chain
	g.mu.Unlock()
}

// emitBeaconEvent pushes g.order[i]'s current signed beacon as a NodeEventBeacon,
// the only way a beacon reaches a running client.
func (g *fakeMultiGateway) emitBeaconEvent(i int) {
	_ = g.peer.Notify(api.MethodNodeEvent, api.NodeEvent{Type: api.NodeEventBeacon, Node: g.descriptor(i)})
}

// emitNodeEvent pushes a roster notification, the gateway's only signal that a
// node's reachability changed.
func (g *fakeMultiGateway) emitNodeEvent(evType string, n *fakeNode) {
	nd := g.nodeDescriptor(n)
	nd.Online = evType != api.NodeEventOffline && evType != api.NodeEventRemoved
	_ = g.peer.Notify(api.MethodNodeEvent, api.NodeEvent{Type: evType, Node: nd})
}

func (g *fakeMultiGateway) onFrame(_ *api.Peer, f api.RelayFrame) {
	n := g.byChan[f.Route.ChanID]
	if n == nil {
		return
	}
	if f.Method == api.MethodE2EHandshake {
		msg1, err := api.HandshakeFromFrame(f)
		if err != nil {
			return
		}
		sess, _, msg2, err := e2e.Respond(n.key, api.ChannelPrologue(n.id, f.Route.ChanID), msg1)
		if err != nil {
			return
		}
		n.ch = api.NewChannel(f.Route.ChanID, sess)
		if hook := n.beforeMsg2; hook != nil {
			hook()
		}
		hf, _ := api.MarshalHandshakeFrame(f.Route.ChanID, msg2)
		_ = g.peer.SendRawFrame(hf)
		if note := n.postHandshake; note != nil {
			nf, _ := n.ch.SealNotificationFrame(note.method, api.RouteHeader{}, note.params)
			_ = g.peer.SendRawFrame(nf)
		}
		return
	}
	if n.ch == nil {
		return
	}
	params, err := n.ch.OpenParams(f)
	if err != nil {
		return
	}
	result, rpcErr, note := n.handle(f.Method, params)
	if note != nil {
		nf, _ := n.ch.SealNotificationFrame(note.method, api.RouteHeader{}, note.params)
		_ = g.peer.SendRawFrame(nf)
	}
	rf, _ := n.ch.SealResponseFrame(f.ID, result, rpcErr)
	_ = g.peer.SendRawFrame(rf)
}

func TestE2EClientRequestResponse(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(_ string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return params, nil, nil // echo
	}

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var out map[string]any
	if err := c.callNode("n1", "sessions.input", map[string]any{"text": "hi"}, &out); err != nil {
		t.Fatalf("callNode: %v", err)
	}
	if out["text"] != "hi" {
		t.Errorf("echo = %v, want text=hi", out)
	}
}

func TestE2EClientErrorResponse(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "boom"}, nil
	}

	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	err := c.callNode("n1", "sessions.kill", nil, nil)
	rpcErr, ok := err.(*api.RPCError)
	if !ok || rpcErr.Message != "boom" {
		t.Fatalf("callNode err = %v (%T), want *api.RPCError boom", err, err)
	}
}

func TestE2EClientUnknownNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) { return nil, nil, nil }
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.callNode("nope", "ping", nil, nil); err == nil {
		t.Fatal("callNode to an unknown node must error")
	}
}

func TestE2EClientStreamsNotifications(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	// On this request the node seals a notification BEFORE the response, so the
	// client must Open both (in arrival order) — the response drains via callNode,
	// the notification via Events().
	f.handle = func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`{"ok":true}`), nil, &fakeNote{
			method: "test.note", params: json.RawMessage(`{"n":9}`),
		}
	}

	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var out map[string]any
	if err := c.callNode("n1", "sessions.refresh", nil, &out); err != nil {
		t.Fatalf("callNode: %v", err)
	}
	if out["ok"] != true {
		t.Errorf("response = %v, want ok=true", out)
	}

	select {
	case ev := <-c.Events():
		if ev.Method != "test.note" {
			t.Fatalf("notification method = %q", ev.Method)
		}
		var got map[string]int
		if err := json.Unmarshal(ev.Params, &got); err != nil || got["n"] != 9 {
			t.Fatalf("notification params = %s err=%v", ev.Params, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification delivered to Events()")
	}
}

// A node that pushes as soon as its channel is up (registry snapshot) sends the
// first sealed frame right behind msg2. The client must have the channel usable
// by then: dropping that frame both loses the state and desyncs the dec-nonce,
// killing every later frame on the channel.
func TestE2EClientReceivesNotificationRightAfterHandshake(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.postHandshake = &fakeNote{method: "test.snapshot", params: json.RawMessage(`{"n":1}`)}
	f.handle = func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`{"ok":true}`), nil, nil
	}

	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	select {
	case ev := <-c.Events():
		if ev.Method != "test.snapshot" {
			t.Fatalf("notification method = %q, want test.snapshot", ev.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-handshake notification never reached Events()")
	}

	// The channel must still decrypt: a dropped frame would have desynced it.
	var out map[string]any
	if err := c.callNode("n1", "sessions.refresh", nil, &out); err != nil {
		t.Fatalf("callNode after post-handshake notification: %v", err)
	}
	if out["ok"] != true {
		t.Errorf("response = %v, want ok=true", out)
	}
}

func TestE2EClientConcurrentCallsOrdered(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(_ string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return params, nil, nil // echo the seq back
	}
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	const n = 20
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(seq int) {
			var out map[string]int
			if err := c.callNode("n1", "sessions.input", map[string]int{"seq": seq}, &out); err != nil {
				errc <- err
				return
			}
			if out["seq"] != seq {
				errc <- fmtErr(seq, out["seq"])
				return
			}
			errc <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errc; err != nil {
			t.Fatalf("concurrent call: %v", err)
		}
	}
}

func fmtErr(want, got int) error { return &mismatch{want, got} }

type mismatch struct{ want, got int }

func (e *mismatch) Error() string {
	return "seq mismatch: got " + strconv.Itoa(e.got) + " want " + strconv.Itoa(e.want)
}

// byNodeSnapshot returns a snapshot of the current byNode map (test-only accessor).
func (m *E2EClient) byNodeSnapshot() map[string]*nodeChan {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]*nodeChan, len(m.byNode))
	for k, v := range m.byNode {
		out[k] = v
	}
	return out
}

// short timeouts keep the suite fast if something wedges
func init() { callTimeout = 3 * time.Second; handshakeTimeoutNs.Store(int64(3 * time.Second)) }

func mustKP(t *testing.T) e2e.KeyPair {
	t.Helper()
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	return kp
}

// newE2EClientForTest writes chain (if provided) to chainPath, then constructs an
// E2EClient pinned to head. The server end of the pipe is unused — we only assert
// on the constructed trust state, not on the wire.
func newE2EClientForTest(t *testing.T, head []byte, chainPath string, chain ...[]byte) (*E2EClient, error) {
	t.Helper()
	if len(chain) == 1 {
		if err := os.WriteFile(chainPath, chain[0], 0o600); err != nil {
			t.Fatalf("seed chain: %v", err)
		}
	}
	_, cli := net.Pipe()
	return NewE2EClientWithIdentity(cli, mustKP(t), head, chainPath)
}

func TestClientTrustStorePersists(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	log, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := log.Tip()
	dev := bytes.Repeat([]byte{0x44}, 32)
	_ = log.AuthorizeDevice(dev, signer)
	chain := trustlog.MarshalChain(log.Entries())

	dir := t.TempDir()
	path := filepath.Join(dir, "client-trustlog-chain")

	// A client seeded with the chain on disk authorizes the device immediately.
	c1, err := newE2EClientForTest(t, head, path, chain)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if !c1.trust.DeviceAuthorized(dev) {
		t.Fatal("client should load the persisted chain and authorize the device")
	}

	// Rollback resistance: ingesting a shorter (genesis-only) strict-prefix chain
	// is a no-op that keeps the current chain (changed=false), never a rollback.
	genesisOnly := trustlog.MarshalChain(log.Entries()[:1])
	if changed, ierr := c1.trust.Ingest(genesisOnly); ierr != nil || changed {
		t.Fatalf("shorter chain must be a no-op: changed=%v err=%v", changed, ierr)
	}
	if !c1.trust.DeviceAuthorized(dev) {
		t.Fatal("state must remain on the longer chain after a rollback attempt")
	}
}

func TestClientSkipsUnauthorizedNode(t *testing.T) {
	// Build a trust chain authorizing only nodeAuth's identity key.
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := lg.Tip()

	authNode := &fakeNode{id: "nodeAuth", key: mustKP(t)}
	unauthNode := &fakeNode{id: "nodeUnauth", key: mustKP(t)}

	// Only nodeAuth's Noise public key is authorized in the chain.
	_ = lg.AuthorizeDevice(authNode.key.Public, signer)
	chain := trustlog.MarshalChain(lg.Entries())

	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	authNode.handle = noop
	unauthNode.handle = noop

	gw, clientConn := newFakeMultiGateway(t, authNode, unauthNode)
	gw.chain = chain
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, head)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()

	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	snap := c.byNodeSnapshot()
	if _, ok := snap["nodeAuth"]; !ok {
		t.Fatal("authorized node should have a channel")
	}
	if _, ok := snap["nodeUnauth"]; ok {
		t.Fatal("unauthorized node must be skipped (no channel)")
	}
}

// A rostered node that is offline (within grace, no live relay peer) or otherwise
// unreachable (relay.open → unknown node) must not abort the whole session: Connect
// skips it and keeps the reachable nodes.
func TestClientConnectSkipsOfflineAndUnreachableNodes(t *testing.T) {
	good := &fakeNode{id: "good", key: mustKP(t)}
	good.handle = func(_ string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return params, nil, nil // echo
	}
	offlineKey := mustKP(t)
	phantomKey := mustKP(t)

	gwConn, clientConn := net.Pipe()
	g := &fakeMultiGateway{nodes: map[string]*fakeNode{"good": good}, byChan: map[string]*fakeNode{}}
	g.peer = api.NewPeer(gwConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case api.MethodNodesList:
				return api.NodesListResult{Nodes: []api.NodeDescriptor{
					{ID: "good", Label: "good", Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(good.key.Public)},
					{ID: "offline", Label: "offline", Online: false, IdentityPubKey: base64.StdEncoding.EncodeToString(offlineKey.Public)},
					{ID: "phantom", Label: "phantom", Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(phantomKey.Public)},
				}}, nil
			case api.MethodRelayOpen:
				var p api.RelayOpenParams
				_ = json.Unmarshal(params, &p)
				if p.NodeID != "good" { // offline must never reach here; phantom has no peer
					return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node: " + p.NodeID}
				}
				g.nextCh++
				chID := "c" + strconv.Itoa(g.nextCh)
				g.byChan[chID] = good
				return api.RelayOpenResult{ChanID: chID}, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
		},
		OnRelayFrame: g.onFrame,
	})
	defer g.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()

	if err := c.Connect(); err != nil {
		t.Fatalf("connect must not fail on one offline/unreachable node: %v", err)
	}

	snap := c.byNodeSnapshot()
	if _, ok := snap["good"]; !ok {
		t.Fatal("reachable node should have a channel")
	}
	if _, ok := snap["offline"]; ok {
		t.Fatal("offline node must be skipped (no relay.open, no channel)")
	}
	if _, ok := snap["phantom"]; ok {
		t.Fatal("unreachable node must be skipped fail-soft (no channel)")
	}

	var out map[string]any
	if err := c.callNode("good", "sessions.input", map[string]any{"text": "hi"}, &out); err != nil {
		t.Fatalf("callNode good: %v", err)
	}
	if out["text"] != "hi" {
		t.Errorf("echo = %v, want text=hi", out)
	}
}

func TestClientDisabledStoreConnectsAll(t *testing.T) {
	// Build a trust chain with a disablement commitment, authorizing only nodeAuth.
	signer, _ := trustlog.GenerateSigner()
	secret, err := trustlog.GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("GenerateDisablementSecret: %v", err)
	}
	commitment := trustlog.DisablementCommitment(secret)
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, [][]byte{commitment})
	head := lg.Tip()

	authNode := &fakeNode{id: "nodeAuth", key: mustKP(t)}
	unauthNode := &fakeNode{id: "nodeUnauth", key: mustKP(t)}

	_ = lg.AuthorizeDevice(authNode.key.Public, signer)
	chain := trustlog.MarshalChain(lg.Entries())

	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	authNode.handle = noop
	unauthNode.handle = noop

	gw, clientConn := newFakeMultiGateway(t, authNode, unauthNode)
	gw.chain = chain
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, head)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()

	// Ingest the chain and disable the trust store before connecting, so
	// enforcement is already off when Connect runs.
	if _, err := c.trust.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := c.trust.Disable(secret, signer); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	// The fake gateway serves only the pre-disable chain (gw.chain). During
	// Connect, syncTrustLog's Ingest rejects that shorter chain (rollback
	// resistance), so the disabled state is preserved when channelling nodes.
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	snap := c.byNodeSnapshot()
	if _, ok := snap["nodeAuth"]; !ok {
		t.Fatal("authorized node should have a channel")
	}
	if _, ok := snap["nodeUnauth"]; !ok {
		t.Fatal("disabled store must open a channel to the otherwise-unauthorized node")
	}
}

// TestClientBeaconKnownSetCachedByTip verifies that checkBeaconConsistencyWithChain
// caches the entry-hash set keyed on the resolved chain tip and reuses it on an
// unchanged tick, then rebuilds it when the tip advances.
func TestClientBeaconKnownSetCachedByTip(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := lg.Tip()
	_ = lg.AuthorizeDevice(bytes.Repeat([]byte{0x44}, 32), signer)
	entries := lg.Entries()
	chain := trustlog.MarshalChain(entries)
	chainTip := trustlog.HashEntry(&entries[len(entries)-1])

	_, cli := net.Pipe()
	m, err := NewE2EClientWithIdentity(cli, mustKP(t), head, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if _, err := m.trust.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Add a beacon directly so the early-return guard does not skip the body.
	key := string(bytes.Repeat([]byte{0xab}, 32))
	m.mu.Lock()
	m.beacons[key] = api.Beacon{Tip: chainTip}
	m.mu.Unlock()

	// Prime: first pass builds and caches the known-set.
	m.checkBeaconConsistencyWithChain(chain, chainTip)
	m.mu.Lock()
	tip1 := append([]byte(nil), m.beaconKnownTip...)
	set1 := m.beaconKnown
	m.mu.Unlock()
	if set1 == nil || len(tip1) == 0 {
		t.Fatal("expected known-set cached after first pass")
	}

	// Unchanged chain: mark the cached map with a sentinel key and verify the
	// second pass does NOT replace it (same map instance ⇒ sentinel still present).
	const sentinel = "\xff_beacon_cache_sentinel"
	m.mu.Lock()
	m.beaconKnown[sentinel] = true
	m.mu.Unlock()

	m.checkBeaconConsistencyWithChain(chain, chainTip)

	m.mu.Lock()
	hasSentinel := m.beaconKnown[sentinel]
	m.mu.Unlock()
	if !hasSentinel {
		t.Fatal("unchanged-chain tick must reuse the cached known-set, not rebuild it")
	}

	// Changed tip: extend the chain by one entry; the cache must rebuild.
	// The sentinel must be gone (new map allocated for new tip).
	_ = lg.AuthorizeDevice(bytes.Repeat([]byte{0x55}, 32), signer)
	newEntries := lg.Entries()
	newChain := trustlog.MarshalChain(newEntries)
	newChainTip := trustlog.HashEntry(&newEntries[len(newEntries)-1])
	if _, err := m.trust.Ingest(newChain); err != nil {
		t.Fatalf("Ingest extended chain: %v", err)
	}

	m.checkBeaconConsistencyWithChain(newChain, newChainTip)

	m.mu.Lock()
	hasSentinelAfter := m.beaconKnown[sentinel]
	m.mu.Unlock()
	if hasSentinelAfter {
		t.Fatal("changed chain tip must rebuild the known-set, not reuse the stale cache")
	}
}

func TestPushRegisterFansOutToAllNodes(t *testing.T) {
	var mu sync.Mutex
	received := map[string]int{}

	makeHandler := func(id string) func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
			if method == api.MethodPushRegister {
				mu.Lock()
				received[id]++
				mu.Unlock()
			}
			return nil, nil, nil
		}
	}

	n1 := &fakeNode{id: "n1", key: mustKP(t), handle: makeHandler("n1")}
	n2 := &fakeNode{id: "n2", key: mustKP(t), handle: makeHandler("n2")}
	gw, clientConn := newFakeMultiGateway(t, n1, n2)
	defer gw.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := c.Call(api.MethodPushRegister, nil, nil); err != nil {
		t.Fatalf("push.register: %v", err)
	}

	mu.Lock()
	r1, r2 := received["n1"], received["n2"]
	mu.Unlock()
	if r1 != 1 || r2 != 1 {
		t.Errorf("push.register reached n1=%d n2=%d times, want 1 each", r1, r2)
	}
}

func TestPushTestReturnsGoneOnlyIfAllNodesGone(t *testing.T) {
	gone := &api.RPCError{Code: api.CodePushGone, Message: "gone"}
	okHandler := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	goneHandler := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, gone, nil
	}

	// partial success: n1 succeeds, n2 reports gone -> overall success
	t.Run("partial-success", func(t *testing.T) {
		n1 := &fakeNode{id: "n1", key: mustKP(t), handle: okHandler}
		n2 := &fakeNode{id: "n2", key: mustKP(t), handle: goneHandler}
		gw, clientConn := newFakeMultiGateway(t, n1, n2)
		defer gw.peer.Close()
		c, err := NewE2EClient(clientConn)
		if err != nil {
			t.Fatalf("NewE2EClient: %v", err)
		}
		defer c.Close()
		if err := c.Connect(); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		if err := c.Call(api.MethodPushTest, nil, nil); err != nil {
			t.Errorf("partial-success push.test should return nil, got %v", err)
		}
	})

	// all gone: n1 gone, n2 gone -> CodePushGone
	t.Run("all-gone", func(t *testing.T) {
		n1 := &fakeNode{id: "n1", key: mustKP(t), handle: goneHandler}
		n2 := &fakeNode{id: "n2", key: mustKP(t), handle: goneHandler}
		gw, clientConn := newFakeMultiGateway(t, n1, n2)
		defer gw.peer.Close()
		c, err := NewE2EClient(clientConn)
		if err != nil {
			t.Fatalf("NewE2EClient: %v", err)
		}
		defer c.Close()
		if err := c.Connect(); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		err = c.Call(api.MethodPushTest, nil, nil)
		rpcErr, isRPC := err.(*api.RPCError)
		if !isRPC || rpcErr.Code != api.CodePushGone {
			t.Errorf("all-gone push.test: want CodePushGone, got %v", err)
		}
	})
}

// TestByNodeWrittenBeforeSessionEventDelivered pins the happens-before guarantee:
// byNode must be written before the node's first session.event reaches the consumer.
// A consumer that calls sessions.list or callNode in response to that event must find
// the node. The test is deterministic because the read loop is single-threaded —
// byNode is written before the postHandshake frame is even dispatched, so by the
// time the event exits c.Events() the registration is visible.
func TestByNodeWrittenBeforeSessionEventDelivered(t *testing.T) {
	n := &fakeNode{
		id:  "snap-node",
		key: mustKP(t),
		postHandshake: &fakeNote{
			method: api.MethodSessionEvent,
			params: json.RawMessage(`{"type":"added","session":{"id":"s1"}}`),
		},
		handle: func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
			return nil, nil, nil
		},
	}
	gw, clientConn := newFakeMultiGateway(t, n)
	defer gw.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Method != api.MethodSessionEvent {
				continue
			}
			if _, ok := c.byNodeSnapshot()[n.id]; !ok {
				t.Fatalf("session.event for %q reached consumer but node not in byNode", n.id)
			}
			return
		case <-deadline:
			t.Fatal("no session.event received within deadline")
		}
	}
}
