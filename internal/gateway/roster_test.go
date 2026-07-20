package gateway

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestNodesListAndNodeEventStream(t *testing.T) {
	a := New(50 * time.Millisecond)
	src := newFakeSource("n1", "n1-box")
	src.idPubKey = "PUB1"
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Roster()) == 1 })
	srv := NewServer(a, nil, nil)

	nodeEvents := make(chan api.NodeEvent, 16)
	gwConn, appConn := net.Pipe()
	go srv.clientSrv.ServeConnContext(context.Background(), gwConn)
	app := api.NewPeer(appConn, api.PeerOptions{
		OnNotify: func(n api.Notification) {
			if n.Method == api.MethodNodeEvent {
				var ev api.NodeEvent
				if json.Unmarshal(n.Params, &ev) == nil {
					nodeEvents <- ev
				}
			}
		},
	})
	defer app.Close()

	// nodes.list returns the roster with the identity pubkey.
	var res api.NodesListResult
	if err := app.Call(api.MethodNodesList, nil, &res); err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].ID != "n1" ||
		res.Nodes[0].IdentityPubKey != "PUB1" || !res.Nodes[0].Online {
		t.Fatalf("nodes.list = %+v", res.Nodes)
	}

	// On connect, the roster is replayed as an 'added' node.event.
	select {
	case ev := <-nodeEvents:
		if ev.Type != api.NodeEventAdded || ev.Node.ID != "n1" {
			t.Fatalf("connect roster event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no added node.event on connect")
	}

	// Node disconnects: offline then removed reach the client.
	close(src.done)
	gotOffline, gotRemoved := false, false
	deadline := time.After(3 * time.Second)
	for !(gotOffline && gotRemoved) {
		select {
		case ev := <-nodeEvents:
			switch ev.Type {
			case api.NodeEventOffline:
				gotOffline = true
			case api.NodeEventRemoved:
				gotRemoved = true
			}
		case <-deadline:
			t.Fatalf("missing node events (offline=%v removed=%v)", gotOffline, gotRemoved)
		}
	}
}

func TestServeNodeThreadsIdentityPubKey(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)
	gwConn, nodeConn := net.Pipe()
	defer gwConn.Close()
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: "n2", Label: "n2-box", Version: "9", IdentityPubKey: "PUBNODE"}, nil
			}
			return nil, nil
		},
	})
	defer nodePeer.Close()
	go srv.serveNode(gwConn)
	eventually(t, func() bool { return len(a.Roster()) == 1 })
	if r := a.Roster(); r[0].ID != "n2" || r[0].IdentityPubKey != "PUBNODE" {
		t.Fatalf("serveNode roster = %+v", r)
	}
}
