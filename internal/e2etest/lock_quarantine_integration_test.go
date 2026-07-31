package e2etest

import (
	"context"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
)

// TestUnpinnedNodeQuarantinesThenRecoversOnPin proves the whole loop: node-b
// joins a network that node-a has already locked, quarantines because it has no
// pin, and starts enforcing normally once pinned to the same genesis.
func TestUnpinnedNodeQuarantinesThenRecoversOnPin(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	t.Cleanup(func() { node.SetTrustSyncIntervalForTest(5 * time.Minute) })

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

	waitFor(t, "both nodes rostered", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var r api.NodesListResult
		return api.NewClient(pc).Call(api.MethodNodesList, nil, &r) == nil && len(r.Nodes) == 2
	})

	// node-a locks the network with itself as the only signer. node-b holds no pin.
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
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{Signers: [][]byte{nodeA.SignerPublic()}}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	ac.Close()
	genesis := initRes.Tip

	waitFor(t, "node-b quarantines", func() bool { return nodeB.Quarantined() })

	if nodeA.Quarantined() {
		t.Fatal("the node that ran lock init self-pins; it must never quarantine")
	}

	if err := nodeB.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	waitFor(t, "node-b enforces after pinning", func() bool {
		return !nodeB.Quarantined() && nodeB.TrustStore() != nil && nodeB.TrustStore().Length() > 0
	})
}
