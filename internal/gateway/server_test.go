package gateway

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
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

// TestRelayOpenCloseAndNodesList exercises the server path: a node connects via
// serveNode, a client opens a relay channel, confirms it gets a chan_id, closes it,
// and verifies nodes.list returns the roster.
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
