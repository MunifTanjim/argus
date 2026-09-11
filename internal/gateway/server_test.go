package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// TestClientServerServesVAPIDKey guards a regression where the unified client
// server stopped registering the push methods (registerPush was only called by
// the deleted plaintext server), so push.vapidKey returned "method not found"
// and mobile devices could never fetch the key to register.
func TestClientServerServesVAPIDKey(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	s.SetVAPIDPublicKey("test-vapid-pub")
	res, err := s.clientSrv.DispatchFunc()(context.Background(), api.MethodPushVAPIDKey, nil)
	if err != nil {
		t.Fatalf("push.vapidKey dispatch: %v", err)
	}
	got, ok := res.(api.PushVAPIDKey)
	if !ok {
		t.Fatalf("result type = %T, want api.PushVAPIDKey", res)
	}
	if got.Key != "test-vapid-pub" {
		t.Fatalf("vapid key = %q, want test-vapid-pub", got.Key)
	}
}

type delivererFunc func(context.Context, string, []byte, string, string) error

func (f delivererFunc) Deliver(ctx context.Context, ep string, b []byte, ttl, u string) error {
	return f(ctx, ep, b, ttl, u)
}

func TestNodeDispatchPushDeliverReportsGone(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	var gotBody []byte
	s.SetPushDeliverer(delivererFunc(func(_ context.Context, _ string, body []byte, _, _ string) error {
		gotBody = body
		return push.ErrGone
	}))

	gwConn, nodeConn := net.Pipe()
	defer gwConn.Close()

	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: "n1", Label: "n1"}, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		},
	})
	defer nodePeer.Close()
	go s.serveNode(gwConn)

	eventually(t, func() bool {
		s.relayMu.Lock()
		defer s.relayMu.Unlock()
		return s.nodePeers["n1"] != nil
	})

	var result api.PushDeliverResult
	err := nodePeer.Call(api.MethodPushDeliver, api.PushDeliverParams{
		Endpoint:   "https://p/ep",
		Ciphertext: base64.StdEncoding.EncodeToString([]byte("opaque")),
	}, &result)
	if err != nil {
		t.Fatalf("push.deliver: %v", err)
	}
	if !result.Gone {
		t.Fatal("want Gone=true")
	}
	if string(gotBody) != "opaque" {
		t.Fatalf("deliverer got %q, want decoded ciphertext", gotBody)
	}
}

func TestRelayOpenCloseAndNodesList(t *testing.T) {
	a := New(0)
	srv := NewServer(a, nil, nil)

	gwNodeConn, nodeConn := net.Pipe()
	defer gwNodeConn.Close()

	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: "n1", Label: "n1-box"}, nil
			}
			return nil, nil
		},
	})
	defer nodePeer.Close()

	go srv.serveNode(gwNodeConn)

	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return srv.nodePeers["n1"] != nil
	})

	gwClientConn, appConn := net.Pipe()
	defer gwClientConn.Close()
	go srv.clientSrv.ServeConnContext(context.Background(), gwClientConn)
	app := api.NewPeer(appConn, api.PeerOptions{})
	defer app.Close()

	var listResult api.NodesListResult
	if err := app.Call(api.MethodNodesList, nil, &listResult); err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	if len(listResult.Nodes) != 1 || listResult.Nodes[0].ID != "n1" {
		t.Fatalf("nodes.list: want [{ID:n1}], got %+v", listResult.Nodes)
	}

	var openResult api.RelayOpenResult
	if err := app.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &openResult); err != nil {
		t.Fatalf("relay.open: %v", err)
	}
	chanID := openResult.ChanID
	if chanID == "" {
		t.Fatal("relay.open: expected non-empty chan_id")
	}

	srv.relayMu.Lock()
	_, recorded := srv.channels[chanID]
	srv.relayMu.Unlock()
	if !recorded {
		t.Fatalf("channel %q not recorded after relay.open", chanID)
	}

	if err := app.Call(api.MethodRelayClose, api.RelayCloseParams{ChanID: chanID}, nil); err != nil {
		t.Fatalf("relay.close: %v", err)
	}
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		_, stillThere := srv.channels[chanID]
		return !stillThere
	})
}

func TestPushPortSetTokenAdmin(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	var got string
	s.SetPushPortTokenSetter(func(tok string) error { got = tok; return nil })

	dispatch := s.clientSrv.DispatchFunc()

	params, _ := json.Marshal(api.PushPortSetTokenParams{Token: "pit_xyz"})
	adminCtx := api.WithPrincipal(context.Background(), api.Principal{Admin: true})
	if _, err := dispatch(adminCtx, api.MethodPushPortSetToken, params); err != nil {
		t.Fatalf("admin dispatch: %v", err)
	}
	if got != "pit_xyz" {
		t.Fatalf("setter got %q, want pit_xyz", got)
	}

	if _, err := dispatch(context.Background(), api.MethodPushPortSetToken, params); err == nil {
		t.Fatal("non-admin: want error, got nil")
	}

	s2 := NewServer(New(0), nil, nil)
	dispatch2 := s2.clientSrv.DispatchFunc()
	_, err := dispatch2(adminCtx, api.MethodPushPortSetToken, params)
	if err == nil {
		t.Fatal("nil setter: want error, got nil")
	}
	if rpcErr, ok := err.(*api.RPCError); !ok || rpcErr.Message != "pushport not enabled on this gateway" {
		t.Fatalf("nil setter: want 'pushport not enabled on this gateway', got %v", err)
	}
}

func TestServerInfoPushPortConfigured(t *testing.T) {
	dispatch := func(s *Server) func(context.Context, string, json.RawMessage) (any, error) {
		return s.clientSrv.DispatchFunc()
	}

	call := func(t *testing.T, s *Server) api.ServerInfo {
		t.Helper()
		res, err := dispatch(s)(context.Background(), api.MethodServerInfo, nil)
		if err != nil {
			t.Fatalf("server.info: %v", err)
		}
		info, ok := res.(api.ServerInfo)
		if !ok {
			t.Fatalf("result type = %T, want api.ServerInfo", res)
		}
		return info
	}

	// no hook set: PushPortConfigured must be false
	s := NewServer(New(0), nil, nil)
	if info := call(t, s); info.PushPortConfigured {
		t.Fatal("want PushPortConfigured=false when no hook is set")
	}

	// hook returns false
	s2 := NewServer(New(0), nil, nil)
	s2.SetPushPortStatus(func() bool { return false })
	if info := call(t, s2); info.PushPortConfigured {
		t.Fatal("want PushPortConfigured=false when hook returns false")
	}

	// hook returns true
	s3 := NewServer(New(0), nil, nil)
	s3.SetPushPortStatus(func() bool { return true })
	if info := call(t, s3); !info.PushPortConfigured {
		t.Fatal("want PushPortConfigured=true when hook returns true")
	}
}

func makeBlindNodeEntries(t *testing.T) [][]byte {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	raw := trustlog.MarshalChain(log.Entries())
	entries, err := trustlog.ChainEntries(raw)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	return entries
}

func TestTrustLogPushSyncAndChanged(t *testing.T) {
	a := New(0)
	srv := NewServer(a, nil, nil)

	connectNode := func(id string, got chan api.Notification) *api.Peer {
		t.Helper()
		gwConn, nodeConn := net.Pipe()
		t.Cleanup(func() { gwConn.Close() })
		p := api.NewPeer(nodeConn, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				if method == api.MethodNodeIdentify {
					return api.IdentifyResult{ID: id, Label: id + "-box"}, nil
				}
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
			},
			OnNotify: func(n api.Notification) {
				if got != nil {
					got <- n
				}
			},
		})
		t.Cleanup(func() { p.Close() })
		go srv.serveNode(gwConn)
		return p
	}

	node2Got := make(chan api.Notification, 4)
	node1 := connectNode("node1", nil)
	connectNode("node2", node2Got)

	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return len(srv.nodePeers) >= 2
	})

	entries := makeBlindNodeEntries(t)

	if err := node1.Call(api.MethodTrustLogPush, api.TrustLogPushParams{Entries: entries}, nil); err != nil {
		t.Fatalf("trustlog.push: %v", err)
	}

	select {
	case n := <-node2Got:
		if n.Method != api.MethodTrustLogChanged {
			t.Fatalf("node2 got %q, want %q", n.Method, api.MethodTrustLogChanged)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node2 did not receive trustlog.changed after push")
	}

	var syncResult api.TrustLogSyncResult
	if err := node1.Call(api.MethodTrustLogSync, api.TrustLogSyncParams{}, &syncResult); err != nil {
		t.Fatalf("trustlog.sync: %v", err)
	}
	if len(syncResult.Entries) != len(entries) {
		t.Fatalf("trustlog.sync: got %d entries, want %d", len(syncResult.Entries), len(entries))
	}
}
