package e2etest

import (
	"context"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
)

// TestIdleFleetStaysUnderChatterBudget is the regression guard for the whole
// chatter effort: two nodes and a client, locked and fully converged, must issue
// almost nothing while idle. It fails loudly if a future change reintroduces a
// per-tick full-chain transfer or an unconditional broadcast.
//
// What is counted: trustlog.pull, trustlog.offer, beacon.offer (node→gateway) and
// trustlog.pull, nodes.list (client→gateway). beacon.deliver is E2E-relayed and
// invisible to the gateway counter. Keepalive is excluded — it is time-based and
// machine-speed-sensitive; these methods are tick-proportional, making the budget
// independent of wall-clock jitter.
func TestIdleFleetStaysUnderChatterBudget(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(5 * time.Minute)
		client.SetTrustSyncIntervalForTest(5 * time.Minute)
	})

	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	sd := sockDir(t)
	sockA := filepath.Join(sd, "a.sock")
	nodeA := startLockNode(t, ctx, "node-a", ts.URL, sockA, filepath.Join(dir, "a-chain"))
	nodeB := startLockNode(t, ctx, "node-b", ts.URL, filepath.Join(sd, "b.sock"), filepath.Join(dir, "b-chain"))

	// Wait for both nodes on the roster with signer and identity keys.
	var roster api.NodesListResult
	waitFor(t, "both nodes rostered with keys", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var r api.NodesListResult
		if api.NewClient(pc).Call(api.MethodNodesList, nil, &r) != nil || len(r.Nodes) != 2 {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.SignerPubKey == "" || nd.IdentityPubKey == "" {
				return false
			}
		}
		roster = r
		return true
	})

	// Generate a stable client identity and authorize it in the genesis along with
	// both node identity keys so the locked client can open channels to both nodes.
	clientKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}
	devices := gatherDevicesForTest(roster.Nodes)
	devices = append(devices, clientKP.Public)

	var aConn net.Conn
	waitFor(t, "node A socket ready", func() bool {
		c, derr := net.Dial("unix", sockA)
		if derr != nil {
			return false
		}
		aConn = c
		return true
	})
	ac := api.NewClient(aConn)
	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{
		Signers: [][]byte{nodeA.SignerPublic()},
		Devices: devices,
	}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	ac.Close()

	// Wait for node-b to quarantine before pinning: this ensures (a) the chain is on
	// the gateway and (b) node-b has seen it. Without this the adopt races the first
	// offer from node-a and sometimes times out on slow/loaded machines.
	waitFor(t, "node-b quarantines", func() bool { return nodeB.Quarantined() })

	if err := nodeB.AdoptPin(initRes.Tip); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	waitFor(t, "node-b converged", func() bool {
		st := nodeB.TrustStore()
		return st != nil && st.Length() > 0
	})

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, "")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer c.Close()

	waitFor(t, "client trust synced", func() bool { return c.TrustTip() != nil })

	// Let any startup burst settle before the measurement window.
	time.Sleep(5 * 50 * time.Millisecond)

	before := srv.ChatterRPCCountForTest()
	time.Sleep(20 * 50 * time.Millisecond)
	got := srv.ChatterRPCCountForTest() - before

	t.Logf("idle fleet chatter RPCs in 20-tick window: %d", got)

	// Measured baseline: 61 RPCs on 2026-07-31. Budget adds ~25% headroom.
	// At 50 ms/tick: 2 nodes × 20 ticks × 1 trustlog.pull = 40; 1 client × 20 ticks
	// × 1 trustlog.pull = 20; nodes.list from syncRoster (~2×/node in window) = ~4;
	// trustlog.offer = 0 (conditional: gateway already has our fingerprint).
	const budget = 77
	if got > budget {
		t.Fatalf("idle fleet issued %d RPCs in the window, budget %d — something is polling or broadcasting unconditionally", got, budget)
	}
}
