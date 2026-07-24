package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
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

func TestRosterCarriesSignerPubKey(t *testing.T) {
	a := New(time.Second)
	// A source advertising both a Noise identity pubkey and an Ed25519 signer pubkey.
	src := newFakeSource("n1", "node-1")
	src.idPubKey = "id-pub-b64"
	src.signerPubKey = "signer-pub-b64"
	a.AddSource(src)

	roster := a.Roster()
	if len(roster) != 1 {
		t.Fatalf("roster len = %d, want 1", len(roster))
	}
	if roster[0].SignerPubKey != "signer-pub-b64" {
		t.Fatalf("SignerPubKey = %q, want %q", roster[0].SignerPubKey, "signer-pub-b64")
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

// TestRosterCarriesBeaconVerbatim verifies that a beacon offered by a node via
// beacon.offer appears verbatim in the roster (nodes.list) and is streamed via
// node.event to connected clients, with bytes unchanged. The gateway never calls
// VerifyBeacon — it relays blindly.
func TestRosterCarriesBeaconVerbatim(t *testing.T) {
	// Build a real Ed25519 beacon keypair + sign a beacon.
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	want := api.SignBeacon(priv, pub, []byte("tip1234567890123456789012345678901"), 3, 7)

	a := New(time.Second)
	srv := NewServer(a, nil, nil)

	// Fake node: replies to node.identify with the beacon from IdentifyResult.
	gwConn, nodeConn := net.Pipe()
	defer gwConn.Close()
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{
					ID:           "bn1",
					Label:        "bn1-box",
					BeaconPubKey: "beacon-pub-b64",
					Beacon:       &want,
				}, nil
			}
			return nil, nil
		},
	})
	defer nodePeer.Close()
	go srv.serveNode(gwConn)
	eventually(t, func() bool { return len(a.Roster()) == 1 })

	// Roster (nodes.list) must carry the beacon verbatim from identify.
	roster := a.Roster()
	if len(roster) != 1 {
		t.Fatalf("roster len = %d", len(roster))
	}
	got := roster[0].Beacon
	if got == nil {
		t.Fatal("roster Beacon is nil; expected beacon from identify")
	}
	if !bytes.Equal(got.Sig, want.Sig) {
		t.Fatalf("beacon Sig mismatch: got %x want %x", got.Sig, want.Sig)
	}
	if got.Counter != want.Counter {
		t.Fatalf("beacon Counter = %d, want %d", got.Counter, want.Counter)
	}

	// Now the node pushes a fresh beacon via beacon.offer (simulates a tip change).
	want2 := api.SignBeacon(priv, pub, []byte("tip_updated_______________________"), 4, 8)

	// Subscribe a client to node.event to capture the beacon update.
	gwClientConn, appConn := net.Pipe()
	beaconEvents := make(chan api.NodeEvent, 8)
	go srv.clientSrv.ServeConnContext(context.Background(), gwClientConn)
	appPeer := api.NewPeer(appConn, api.PeerOptions{
		OnNotify: func(n api.Notification) {
			if n.Method == api.MethodNodeEvent {
				var ev api.NodeEvent
				if json.Unmarshal(n.Params, &ev) == nil && ev.Type == api.NodeEventBeacon {
					beaconEvents <- ev
				}
			}
		},
	})
	defer appPeer.Close()

	// Give the client connection time to receive the initial roster snapshot.
	time.Sleep(20 * time.Millisecond)

	// Node offers the second beacon via the gateway request path.
	if err := nodePeer.Call(api.MethodBeaconOffer, want2, nil); err != nil {
		t.Fatalf("beacon.offer: %v", err)
	}

	// Wait for the beacon event to propagate to the client.
	select {
	case ev := <-beaconEvents:
		if ev.Node.Beacon == nil {
			t.Fatal("node.event beacon is nil")
		}
		if !bytes.Equal(ev.Node.Beacon.Sig, want2.Sig) {
			t.Fatalf("event beacon Sig mismatch: got %x want %x", ev.Node.Beacon.Sig, want2.Sig)
		}
		if ev.Node.Beacon.Counter != want2.Counter {
			t.Fatalf("event beacon Counter = %d, want %d", ev.Node.Beacon.Counter, want2.Counter)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no beacon node.event received")
	}

	// Roster must now reflect the updated beacon.
	r2 := a.Roster()
	if r2[0].Beacon == nil || !bytes.Equal(r2[0].Beacon.Sig, want2.Sig) {
		t.Fatalf("roster after update: beacon = %+v", r2[0].Beacon)
	}
	if r2[0].BeaconPubKey != "beacon-pub-b64" {
		t.Fatalf("BeaconPubKey = %q", r2[0].BeaconPubKey)
	}
}
