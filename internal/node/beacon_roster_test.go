package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gateway"
)

// TestSyncRosterPopulatesPeerBeaconPubs verifies that a node connecting to a real
// gateway uplink calls nodes.list successfully and populates peerBeaconPubs, so
// handleBeaconDeliver can accept couriered beacons from registered peers.
// This test would fail if nodes.list were absent from the gateway's node-uplink dispatch.
//
// NOTE: skipped until Task 4 adds syncRoster to runTrustSyncLoop (scope-map F5).
func TestSyncRosterPopulatesPeerBeaconPubs(t *testing.T) {
	t.Skip("deferred: requires Task 4 to integrate syncRoster into runTrustSyncLoop")

	agg := gateway.New(time.Second, false)
	srv := gateway.NewServer(agg, nil, nil, false)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	alphaPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate beacon key: %v", err)
	}
	alphaPubB64 := base64.StdEncoding.EncodeToString(alphaPub)

	gwConn, alphaConn := net.Pipe()
	t.Cleanup(func() { gwConn.Close(); alphaConn.Close() })
	alphaPeer := api.NewPeer(gwConn, api.PeerOptions{})
	t.Cleanup(func() { alphaPeer.Close() })
	alphaSource := gateway.NewRemoteSource("alpha", "alpha-box", "1", "", "", alphaPubB64, api.NodeCapabilities{}, alphaPeer, nil)
	agg.AddSource(alphaSource)

	d := newNode(nil)
	d.SetIdentity("beta", "beta-box")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go d.ConnectGateway(ctx, wsURL(ts.URL)+"/node", "", nil)

	waitFor(t, func() bool {
		d.peerBeaconMu.Lock()
		defer d.peerBeaconMu.Unlock()
		return d.peerBeaconPubs[string(alphaPub)]
	})
}
