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

	a := New(time.Second, true)
	srv := NewServer(a, nil, nil, true)

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
	go srv.serveNodeBlind(gwConn)
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
