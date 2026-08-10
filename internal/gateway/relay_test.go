package gateway

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// adoptFakeNode connects a fake node via serveNode; frames receives relay frames
// the gateway forwards to it.
func adoptFakeNode(t *testing.T, srv *Server, id, pubkey string) (*api.Peer, chan api.RelayFrame) {
	t.Helper()
	frames := make(chan api.RelayFrame, 32)
	gwConn, nodeConn := net.Pipe()
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { frames <- f },
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: id, Label: id + "-box", Version: "9",
					Capabilities: api.NodeCapabilities{SpawnSession: true}, IdentityPubKey: pubkey}, nil
			}
			return nil, nil
		},
	})
	go srv.serveNode(gwConn)
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.nodePeers[id] != nil
	})
	return nodePeer, frames
}

// connectFakeClient serves a client via clientSrv; relayFrames receives relay
// frames the gateway forwards to it.
func connectFakeClient(t *testing.T, srv *Server) (*api.Peer, chan api.RelayFrame) {
	t.Helper()
	relayFrames := make(chan api.RelayFrame, 32)
	gwConn, appConn := net.Pipe()
	go srv.clientSrv.ServeConnContext(context.Background(), gwConn)
	client := api.NewPeer(appConn, api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) { relayFrames <- f },
	})
	return client, relayFrames
}

// rawRelayFrame builds a verbatim relay frame line for a chan_id with an opaque body.
func rawRelayFrame(t *testing.T, chanID, method, body string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"route":   map[string]string{"chan_id": chanID},
		"body":    body,
	})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	return raw
}

func TestRelayOpenForwardsBothDirections(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	nodePeer, nodeFrames := adoptFakeNode(t, srv, "n1", "PUB1")
	defer nodePeer.Close()
	client, clientFrames := connectFakeClient(t, srv)
	defer client.Close()

	var res api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	if res.ChanID == "" {
		t.Fatal("empty chan_id")
	}

	// client -> node (forwarded verbatim)
	if err := client.SendRawFrame(rawRelayFrame(t, res.ChanID, "sessions.input", "Y2xpZW50")); err != nil {
		t.Fatalf("client SendRawFrame: %v", err)
	}
	select {
	case f := <-nodeFrames:
		if f.Route.ChanID != res.ChanID || string(f.Body) != `"Y2xpZW50"` {
			t.Errorf("node got chan=%q body=%s", f.Route.ChanID, f.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node did not receive the client's frame")
	}

	// node -> client (forwarded verbatim)
	if err := nodePeer.SendRawFrame(rawRelayFrame(t, res.ChanID, "transcript.delta", "bm9kZQ==")); err != nil {
		t.Fatalf("node SendRawFrame: %v", err)
	}
	select {
	case f := <-clientFrames:
		if f.Route.ChanID != res.ChanID || string(f.Body) != `"bm9kZQ=="` {
			t.Errorf("client got chan=%q body=%s", f.Route.ChanID, f.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive the node's frame")
	}
}

func TestRelayOpenUnknownNode(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	client, _ := connectFakeClient(t, srv)
	defer client.Close()
	var res api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "nope"}, &res); err == nil {
		t.Fatal("relay.open to an unknown node must error")
	}
}

func TestRelayCloseDropsChannel(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	nodePeer, _ := adoptFakeNode(t, srv, "n1", "PUB1")
	defer nodePeer.Close()
	client, _ := connectFakeClient(t, srv)
	defer client.Close()

	var res api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	if err := client.Call(api.MethodRelayClose, api.RelayCloseParams{ChanID: res.ChanID}, nil); err != nil {
		t.Fatalf("relay.close: %v", err)
	}
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.channels[res.ChanID] == nil
	})
}

func TestClientDisconnectClosesChannels(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	nodePeer, _ := adoptFakeNode(t, srv, "n1", "PUB1")
	defer nodePeer.Close()
	client, _ := connectFakeClient(t, srv)

	var res api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.channels[res.ChanID] != nil
	})

	client.Close() // client drops its connection
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.channels[res.ChanID] == nil
	})
}

func TestEnqueueOverflowTearsDownChannel(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	// A channel whose queue is full and whose pump is not draining.
	ch := &relayChannel{chanID: "cX", toNode: make(chan []byte, 2), stop: make(chan struct{})}
	srv.relayMu.Lock()
	srv.channels["cX"] = ch
	srv.channels["cY"] = &relayChannel{chanID: "cY", toNode: make(chan []byte, 2), stop: make(chan struct{})}
	srv.relayMu.Unlock()

	ch.toNode <- []byte("a") // fill to cap (2)
	ch.toNode <- []byte("b")
	s := srv
	s.enqueue(ch, ch.toNode, []byte("c")) // overflow -> tear down cX only

	srv.relayMu.Lock()
	_, cxPresent := srv.channels["cX"]
	_, cyPresent := srv.channels["cY"]
	srv.relayMu.Unlock()
	if cxPresent {
		t.Error("overflow must tear down the overflowing channel")
	}
	if !cyPresent {
		t.Error("overflow must not affect other channels")
	}
	select {
	case <-ch.stop:
	default:
		t.Error("stop channel not closed on overflow teardown")
	}
}

func TestNodeDisconnectClosesChannels(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	nodePeer, _ := adoptFakeNode(t, srv, "n1", "PUB1")
	client, _ := connectFakeClient(t, srv)
	defer client.Close()

	var res api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.channels[res.ChanID] != nil
	})

	nodePeer.Close()
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.channels[res.ChanID] == nil && srv.nodePeers["n1"] == nil
	})
}
