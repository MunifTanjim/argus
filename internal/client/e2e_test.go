package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
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

func (f *fakeGatewayNode) onFrame(fr api.RelayFrame) {
	if fr.Method == api.MethodE2EHandshake {
		msg1, err := api.HandshakeFromFrame(fr)
		if err != nil {
			return
		}
		sess, msg2, err := e2e.Respond(f.nodeKey, api.ChannelPrologue(f.nodeID, fr.Route.ChanID), msg1)
		if err != nil {
			return
		}
		f.nodeCh = api.NewChannel(fr.Route.ChanID, sess)
		hf, _ := api.MarshalHandshakeFrame(fr.Route.ChanID, msg2)
		_ = f.peer.SendRawFrame(hf)
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
}

// fakeMultiGateway is one peer playing the gateway for several nodes: nodes.list
// advertises all of them, relay.open{node_id} allocates a chan_id bound to that
// node, and OnRelayFrame routes handshake/sealed frames to the right node by chan_id.
type fakeMultiGateway struct {
	peer   *api.Peer
	nodes  map[string]*fakeNode // node id -> node
	byChan map[string]*fakeNode // chan_id -> node
	nextCh int
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
	}
	g.peer = api.NewPeer(gwConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case api.MethodNodesList:
				var descs []api.NodeDescriptor
				for _, n := range nodes { // stable order
					descs = append(descs, api.NodeDescriptor{
						ID: n.id, Label: n.id + "-box", Online: true,
						IdentityPubKey: base64.StdEncoding.EncodeToString(n.key.Public),
					})
				}
				return api.NodesListResult{Nodes: descs}, nil
			case api.MethodRelayOpen:
				var p api.RelayOpenParams
				_ = json.Unmarshal(params, &p)
				n := g.nodes[p.NodeID]
				if n == nil {
					return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node"}
				}
				g.nextCh++
				chID := "c" + strconv.Itoa(g.nextCh)
				g.byChan[chID] = n
				return api.RelayOpenResult{ChanID: chID}, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
		},
		OnRelayFrame: g.onFrame,
	})
	return g, clientConn
}

func (g *fakeMultiGateway) onFrame(f api.RelayFrame) {
	n := g.byChan[f.Route.ChanID]
	if n == nil {
		return
	}
	if f.Method == api.MethodE2EHandshake {
		msg1, err := api.HandshakeFromFrame(f)
		if err != nil {
			return
		}
		sess, msg2, err := e2e.Respond(n.key, api.ChannelPrologue(n.id, f.Route.ChanID), msg1)
		if err != nil {
			return
		}
		n.ch = api.NewChannel(f.Route.ChanID, sess)
		hf, _ := api.MarshalHandshakeFrame(f.Route.ChanID, msg2)
		_ = g.peer.SendRawFrame(hf)
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

// short timeouts keep the suite fast if something wedges
func init() { callTimeout = 3 * time.Second; handshakeTimeout = 3 * time.Second }
