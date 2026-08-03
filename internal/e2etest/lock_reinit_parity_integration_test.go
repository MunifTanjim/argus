package e2etest

import (
	"bytes"
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

// TestReinitOverDisabledLogMatchesFirstLock pins the sync interval at its production
// length so nothing here can be a tick: node-b must react to the push. It asserts the
// property the parity work exists for — relocking behaves exactly like first-locking,
// including recovery in a single lock pin.
func TestReinitOverDisabledLogMatchesFirstLock(t *testing.T) {
	node.SetTrustSyncIntervalForTest(10 * time.Minute)
	// Three pushes land in quick succession here (lock, disable, relock); the
	// production rate limit would defer each by its full window.
	node.SetTriggeredPullIntervalForTest(50 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(5 * time.Minute)
		node.ResetTriggeredPullIntervalForTest()
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

	waitFor(t, "both nodes rostered", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var r api.NodesListResult
		return api.NewClient(pc).Call(api.MethodNodesList, nil, &r) == nil && len(r.Nodes) == 2
	})

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
	defer ac.Close()

	// 1. First lock: node-b quarantines, then one AdoptPin recovers it.
	var g1 api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{
		Signers:         [][]byte{nodeA.SignerPublic()},
		GenDisablements: 1,
	}, &g1); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	waitFor(t, "node-b quarantines on the first lock", func() bool { return nodeB.Quarantined() })
	if err := nodeB.AdoptPin(g1.Tip); err != nil {
		t.Fatalf("AdoptPin G1: %v", err)
	}
	waitFor(t, "node-b enforces G1", func() bool {
		return !nodeB.Quarantined() && nodeB.TrustStore() != nil && nodeB.TrustStore().Length() > 0
	})

	// 2. Break glass. Both nodes converge on the disabled chain and keep working:
	// a disabled network with no successor is exactly what break-glass is for.
	var disableRes api.LockDisableResult
	if err := ac.Call(api.MethodLockDisable, api.LockDisableParams{Secret: g1.DisablementSecrets[0]}, &disableRes); err != nil {
		t.Fatalf("lock.disable: %v", err)
	}
	waitFor(t, "node-b sees the disable", func() bool {
		st := nodeB.TrustStore()
		return st != nil && st.Disabled()
	})
	if nodeB.Quarantined() {
		t.Fatal("a disabled network with no successor must keep working")
	}

	// 3. Relock. node-b must behave exactly as it did in step 1.
	var g2 api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{
		Signers:         [][]byte{nodeA.SignerPublic()},
		GenDisablements: 1,
	}, &g2); err != nil {
		t.Fatalf("lock.init over the disabled log: %v", err)
	}
	waitFor(t, "node-b quarantines on the relock", func() bool { return nodeB.Quarantined() })
	waitFor(t, "node-b names the new root", func() bool {
		return bytes.Equal(nodeB.QuarantineGenesis(), g2.Tip)
	})

	// 3b. Relock a second time, without repinning node-b in between. The gateway now
	// retains three roots — two dead, one live — and node-b must name the live one:
	// that fingerprint is what the operator compares against node-a and then pins.
	if err := ac.Call(api.MethodLockDisable, api.LockDisableParams{Secret: g2.DisablementSecrets[0]}, &disableRes); err != nil {
		t.Fatalf("second lock.disable: %v", err)
	}
	var g3 api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{Signers: [][]byte{nodeA.SignerPublic()}}, &g3); err != nil {
		t.Fatalf("second lock.init: %v", err)
	}
	waitFor(t, "node-b follows the second relock", func() bool {
		return bytes.Equal(nodeB.QuarantineGenesis(), g3.Tip)
	})

	// 4. One command, no unpin — against the root node-b was told to adopt.
	if err := nodeB.AdoptPin(nodeB.QuarantineGenesis()); err != nil {
		t.Fatalf("AdoptPin must not require an unpin: %v", err)
	}
	waitFor(t, "node-b enforces the live root", func() bool {
		st := nodeB.TrustStore()
		return !nodeB.Quarantined() && st != nil && !st.Disabled() && bytes.Equal(st.Tip(), g3.Tip)
	})
	if nodeA.Equivocation() || nodeB.Equivocation() {
		t.Fatal("a legitimate relock must not latch equivocation")
	}
}
