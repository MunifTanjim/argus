package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// relayPair connects a fake node (id n1) and a fake client to srv and opens a
// relay channel between them.
func relayPair(t *testing.T, srv *Server) (client, node *api.Peer, chanID string, fromClient, fromNode chan api.RelayFrame) {
	t.Helper()
	fromClient = make(chan api.RelayFrame, 8)
	fromNode = make(chan api.RelayFrame, 8)

	gwNodeConn, nodeConn := net.Pipe()
	node = api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: "n1", Label: "n1"}, nil
			}
			return nil, nil
		},
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { fromClient <- f },
	})
	go srv.serveNode(gwNodeConn)
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.nodePeers["n1"] != nil
	})

	gwClientConn, appConn := net.Pipe()
	go srv.clientSrv.ServeConnContext(context.Background(), gwClientConn)
	client = api.NewPeer(appConn, api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { fromNode <- f },
	})
	t.Cleanup(func() { client.Close(); node.Close(); gwNodeConn.Close(); gwClientConn.Close() })

	var open api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &open); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	return client, node, open.ChanID, fromClient, fromNode
}

func awaitRelay(t *testing.T, ch chan api.RelayFrame) api.RelayFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no relay frame forwarded")
		return api.RelayFrame{}
	}
}

func TestGatewayForwardsCBORUnchanged(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	client, node, chanID, fromClient, fromNode := relayPair(t, srv)
	ch := api.NewPlainChannel(chanID)

	params, _ := api.CBORMarshal(api.TerminalInputParams{TermID: "t1", Data: []byte{0, 1, 0xff}})
	id := json.RawMessage("7")
	req, err := ch.SealRequestFrame(api.EncodingCBOR, &id, api.MethodTerminalInput, "n1", params)
	if err != nil {
		t.Fatalf("SealRequestFrame: %v", err)
	}
	if err := client.SendRawFrame(api.EncodingCBOR, req); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f := awaitRelay(t, fromClient); f.Enc != api.EncodingCBOR || !bytes.Equal(f.Raw, req) {
		t.Fatalf("client->node frame enc=%v raw=%x, want CBOR %x", f.Enc, f.Raw, req)
	}

	out, _ := api.CBORMarshal(api.TerminalOutput{TermID: "t1", Data: []byte("hi\x00")})
	note, err := ch.SealNotificationFrame(api.EncodingCBOR, api.MethodTerminalOutput, api.RouteHeader{}, out)
	if err != nil {
		t.Fatalf("SealNotificationFrame: %v", err)
	}
	if err := node.SendRawFrame(api.EncodingCBOR, note); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f := awaitRelay(t, fromNode); f.Enc != api.EncodingCBOR || !bytes.Equal(f.Raw, note) {
		t.Fatalf("node->client frame enc=%v raw=%x, want CBOR %x", f.Enc, f.Raw, note)
	}
}

func TestGatewayServerInfoReportsCBOR(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	res, err := s.clientSrv.DispatchFunc()(context.Background(), api.MethodServerInfo, nil)
	if err != nil {
		t.Fatalf("server.info: %v", err)
	}
	b, _ := json.Marshal(res)
	var info struct {
		Capabilities *struct {
			CBOR bool `json:"cbor"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Capabilities == nil || !info.Capabilities.CBOR {
		t.Fatalf("server.info = %s, want capabilities.cbor true", b)
	}
}

func TestGatewayIdentifyDeclaresCBOR(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	got := make(chan json.RawMessage, 1)
	gwConn, nodeConn := net.Pipe()
	defer gwConn.Close()
	node := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				got <- params
				return api.IdentifyResult{ID: "n1", Label: "n1"}, nil
			}
			return nil, nil
		},
	})
	defer node.Close()
	go srv.serveNode(gwConn)

	select {
	case params := <-got:
		var p api.IdentifyParams
		if err := json.Unmarshal(params, &p); err != nil || !p.Capabilities.CBOR {
			t.Fatalf("node.identify params = %s (err %v), want capabilities.cbor true", params, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway never called node.identify")
	}
}

// nodeTap records, in arrival order, every notification and relay frame a fake
// node receives on its uplink. Both are delivered from the peer's read loop, so
// the order here is the order on the wire.
type nodeTap struct{ events chan any }

func (tp *nodeTap) await(t *testing.T, what string, match func(any) bool) any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-tp.events:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
			return nil
		}
	}
}

func isNote(method string) func(any) bool {
	return func(ev any) bool { n, ok := ev.(api.Notification); return ok && n.Method == method }
}

func isFrame(ev any) bool { _, ok := ev.(api.RelayFrame); return ok }

func connectNode(t *testing.T, srv *Server, id string) (*api.Peer, *nodeTap) {
	t.Helper()
	return connectNodeCaps(t, srv, id, api.NodeCapabilities{})
}

func connectNodeCaps(t *testing.T, srv *Server, id string, caps api.NodeCapabilities) (*api.Peer, *nodeTap) {
	t.Helper()
	tap := &nodeTap{events: make(chan any, 1024)}
	gwConn, nodeConn := net.Pipe()
	peer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: id, Label: id, Capabilities: caps}, nil
			}
			return nil, nil
		},
		OnNotify:     func(n api.Notification) { tap.events <- n },
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { tap.events <- f },
	})
	go srv.serveNode(gwConn)
	t.Cleanup(func() { peer.Close(); gwConn.Close() })
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.nodePeers[id] != nil
	})
	return peer, tap
}

// nodePair connects two fake nodes (na, nb) to srv over node uplinks.
func nodePair(t *testing.T, srv *Server) (a, b *api.Peer, aID, bID string, aTap, bTap *nodeTap) {
	t.Helper()
	aID, bID = "na", "nb"
	a, aTap = connectNode(t, srv, aID)
	b, bTap = connectNode(t, srv, bID)
	return a, b, aID, bID, aTap, bTap
}

func relayFrom(t *testing.T, chanID, method string) []byte {
	t.Helper()
	raw, err := api.NewPlainChannel(chanID).SealNotificationFrame(api.EncodingCBOR, method, api.RouteHeader{}, []byte{0xa0})
	if err != nil {
		t.Fatalf("SealNotificationFrame: %v", err)
	}
	return raw
}

func openFrom(t *testing.T, p *api.Peer, nodeID string) string {
	t.Helper()
	var open api.RelayOpenResult
	if err := p.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: nodeID}, &open); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	if open.ChanID == "" {
		t.Fatal("relay.open: empty chan_id")
	}
	return open.ChanID
}

func wantInvalidRequest(t *testing.T, err error) {
	t.Helper()
	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Fatalf("err = %v, want CodeInvalidRequest", err)
	}
}

func TestNodeRelayOpenForwardsBothWays(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	a, b, _, bID, aTap, bTap := nodePair(t, srv)
	chanID := openFrom(t, a, bID)

	ab := relayFrom(t, chanID, "a.to.b")
	if err := a.SendRawFrame(api.EncodingCBOR, ab); err != nil {
		t.Fatalf("send a->b: %v", err)
	}
	if f := bTap.await(t, "a->b frame", isFrame).(api.RelayFrame); !bytes.Equal(f.Raw, ab) || f.Route.ChanID != chanID {
		t.Fatalf("b got %x on %q, want %x on %q", f.Raw, f.Route.ChanID, ab, chanID)
	}

	ba := relayFrom(t, chanID, "b.to.a")
	if err := b.SendRawFrame(api.EncodingCBOR, ba); err != nil {
		t.Fatalf("send b->a: %v", err)
	}
	if f := aTap.await(t, "b->a frame", isFrame).(api.RelayFrame); !bytes.Equal(f.Raw, ba) {
		t.Fatalf("a got %x, want %x", f.Raw, ba)
	}
}

func TestNodeRelayOpenSendsPeerOpenedFirst(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	a, _, aID, bID, _, bTap := nodePair(t, srv)
	chanID := openFrom(t, a, bID)
	if err := a.SendRawFrame(api.EncodingCBOR, relayFrom(t, chanID, "hello")); err != nil {
		t.Fatalf("send: %v", err)
	}

	ev := bTap.await(t, "peer_opened or frame", func(ev any) bool {
		return isFrame(ev) || isNote(api.MethodRelayPeerOpened)(ev)
	})
	n, ok := ev.(api.Notification)
	if !ok {
		t.Fatal("b received a relayed frame before relay.peer_opened")
	}
	p, err := api.Decode[api.RelayPeerOpenedParams](n.Params)
	if err != nil {
		t.Fatalf("decode peer_opened: %v", err)
	}
	if p.ChanID != chanID || p.FromNode != aID {
		t.Fatalf("peer_opened = %+v, want {ChanID:%s FromNode:%s}", p, chanID, aID)
	}
	bTap.await(t, "first frame", isFrame)
}

func TestNodeRelayOpenRejectsSelf(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	a, _, aID, _, _, _ := nodePair(t, srv)
	err := a.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: aID}, nil)
	wantInvalidRequest(t, err)
}

func TestNodeRelayOpenCapsChannels(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	a, _, _, bID, _, _ := nodePair(t, srv)
	for i := 0; i < maxChannelsPerNode; i++ {
		openFrom(t, a, bID)
	}
	err := a.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: bID}, nil)
	wantInvalidRequest(t, err)
}

func TestNodeUplinkCloseDropsChannelsBothSides(t *testing.T) {
	t.Run("opener closes", func(t *testing.T) {
		srv := NewServer(New(0), nil, nil)
		a, _, _, bID, _, bTap := nodePair(t, srv)
		chanID := openFrom(t, a, bID)
		a.Close()
		n := bTap.await(t, "relay.closed on b", isNote(api.MethodRelayClosed)).(api.Notification)
		if p, _ := api.Decode[api.RelayCloseParams](n.Params); p.ChanID != chanID {
			t.Fatalf("relay.closed chan = %q, want %q", p.ChanID, chanID)
		}
	})
	t.Run("target closes", func(t *testing.T) {
		srv := NewServer(New(0), nil, nil)
		a, b, _, bID, aTap, _ := nodePair(t, srv)
		chanID := openFrom(t, a, bID)
		b.Close()
		n := aTap.await(t, "relay.closed on a", isNote(api.MethodRelayClosed)).(api.Notification)
		if p, _ := api.Decode[api.RelayCloseParams](n.Params); p.ChanID != chanID {
			t.Fatalf("relay.closed chan = %q, want %q", p.ChanID, chanID)
		}
	})
}

func TestNodeRelayCloseByOpener(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	a, _, _, bID, _, bTap := nodePair(t, srv)
	chanID := openFrom(t, a, bID)
	if err := a.Call(api.MethodRelayClose, api.RelayCloseParams{ChanID: chanID}, nil); err != nil {
		t.Fatalf("relay.close: %v", err)
	}
	bTap.await(t, "relay.closed on b", isNote(api.MethodRelayClosed))
	srv.relayMu.Lock()
	_, still := srv.channels[chanID]
	srv.relayMu.Unlock()
	if still {
		t.Fatal("channel still open after relay.close")
	}
}

func TestNodePeersReceiveRosterEvents(t *testing.T) {
	srv := NewServer(New(10*time.Millisecond), nil, nil)
	a, _, aID, _, _, bTap := nodePair(t, srv)
	nodeEvent := func(typ string) func(any) bool {
		return func(ev any) bool {
			if !isNote(api.MethodNodeEvent)(ev) {
				return false
			}
			e, err := api.Decode[api.NodeEvent](ev.(api.Notification).Params)
			return err == nil && e.Type == typ && e.Node.ID == aID
		}
	}
	bTap.await(t, "node.event added for a", nodeEvent(api.NodeEventAdded))
	a.Close()
	bTap.await(t, "node.event removed for a", nodeEvent(api.NodeEventRemoved))
}

func TestClientRelayUnchanged(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	client, node, chanID, fromClient, fromNode := relayPair(t, srv)

	up := relayFrom(t, chanID, "up")
	if err := client.SendRawFrame(api.EncodingCBOR, up); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f := awaitRelay(t, fromClient); !bytes.Equal(f.Raw, up) {
		t.Fatalf("node got %x, want %x", f.Raw, up)
	}
	down := relayFrom(t, chanID, "down")
	if err := node.SendRawFrame(api.EncodingCBOR, down); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f := awaitRelay(t, fromNode); !bytes.Equal(f.Raw, down) {
		t.Fatalf("client got %x, want %x", f.Raw, down)
	}

	a, _, _, bID, _, bTap := nodePair(t, srv)
	nodeChan := openFrom(t, a, bID)
	if err := client.SendRawFrame(api.EncodingCBOR, relayFrom(t, nodeChan, "intrude")); err != nil {
		t.Fatalf("send: %v", err)
	}
	quiet := time.After(200 * time.Millisecond)
	for {
		select {
		case ev := <-bTap.events:
			if isFrame(ev) {
				t.Fatalf("client frame reached a node-opened channel: %+v", ev)
			}
		case <-quiet:
			return
		}
	}
}

func TestNodeRelayOpenCapHoldsUnderConcurrency(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	a, _, _, bID, _, _ := nodePair(t, srv)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2*maxChannelsPerNode; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: bID}, &api.RelayOpenResult{}) == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := int(ok.Load()); n > maxChannelsPerNode {
		t.Fatalf("%d channels opened, limit %d", n, maxChannelsPerNode)
	}
}
