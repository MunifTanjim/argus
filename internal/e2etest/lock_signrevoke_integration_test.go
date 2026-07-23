package e2etest

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
)

func TestLockSignRevoke(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(30 * time.Second)
		client.SetTrustSyncIntervalForTest(30 * time.Second)
	})

	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	sockA := filepath.Join(dir, "a.sock")
	chainPathA := filepath.Join(dir, "chainA")
	chainPathB := filepath.Join(dir, "chainB")

	startLockNode(t, ctx, "node-a", ts.URL, sockA, chainPathA)
	nodeB := startLockNode(t, ctx, "node-b", ts.URL, filepath.Join(dir, "b.sock"), chainPathB)

	// Wait until both nodes are rostered.
	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "both nodes rostered", func() bool {
		var r api.NodesListResult
		return poll.Call(api.MethodNodesList, nil, &r) == nil && len(r.Nodes) == 2
	})
	poll.Close()

	// Dial node A's unix socket (wait for it to be ready).
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

	// lock.init on A — A is the sole signer, no additional devices initially.
	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}

	// Enable node B out-of-band with the genesis head so it can pull from gateway.
	if err := nodeB.EnableTrustLog(initRes.Tip, chainPathB); err != nil {
		t.Fatalf("enable B: %v", err)
	}

	dev := bytes.Repeat([]byte{0x7C}, 32)

	// lock.sign dev on A → propagates to B.
	var signRes api.LockDeviceResult
	if err := ac.Call(api.MethodLockSign, api.LockDeviceParams{Device: dev}, &signRes); err != nil {
		t.Fatalf("lock.sign: %v", err)
	}
	waitFor(t, "device authorized on node B", func() bool {
		st := nodeB.TrustStore()
		return st != nil && st.DeviceAuthorized(dev)
	})

	// lock.revoke dev on A → propagates to B.
	var revokeRes api.LockDeviceResult
	if err := ac.Call(api.MethodLockRevoke, api.LockDeviceParams{Device: dev}, &revokeRes); err != nil {
		t.Fatalf("lock.revoke: %v", err)
	}
	waitFor(t, "device revoked on node B", func() bool {
		st := nodeB.TrustStore()
		return st != nil && !st.DeviceAuthorized(dev)
	})
}

// TestLockSignerAddRemove adds node B as a second signer, authorizes a device
// via node B, then removes node B as signer and asserts the device is
// retroactively invalidated.
func TestLockSignerAddRemove(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(30 * time.Second)
		client.SetTrustSyncIntervalForTest(30 * time.Second)
	})

	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	sockA := filepath.Join(dir, "a.sock")
	sockB := filepath.Join(dir, "b.sock")
	chainPathA := filepath.Join(dir, "chainA")
	chainPathB := filepath.Join(dir, "chainB")

	nodeA := startLockNode(t, ctx, "node-a", ts.URL, sockA, chainPathA)
	nodeB := startLockNode(t, ctx, "node-b", ts.URL, sockB, chainPathB)

	// Wait until both nodes are rostered with signer keys.
	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	var roster api.NodesListResult
	waitFor(t, "both nodes rostered with signer keys", func() bool {
		if poll.Call(api.MethodNodesList, nil, &roster) != nil || len(roster.Nodes) != 2 {
			return false
		}
		for _, nd := range roster.Nodes {
			if nd.SignerPubKey == "" {
				return false
			}
		}
		return true
	})
	poll.Close()

	// Extract node B's signer pubkey from the roster.
	var signerBPub []byte
	for _, nd := range roster.Nodes {
		if nd.ID == "node-b" {
			signerBPub, _ = base64.StdEncoding.DecodeString(nd.SignerPubKey)
		}
	}
	if len(signerBPub) == 0 {
		t.Fatal("node-b signer key not found in roster")
	}

	// Dial node A's socket (wait for it to be ready).
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

	// lock.init on A — A is the sole signer initially.
	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}

	// Enable node B with the genesis head so it can sync from the gateway.
	if err := nodeB.EnableTrustLog(initRes.Tip, chainPathB); err != nil {
		t.Fatalf("enable B: %v", err)
	}

	// Add node B as a trusted signer.
	var addRes api.LockDeviceResult
	if err := ac.Call(api.MethodLockAddSigner, api.LockSignerParams{Signer: signerBPub}, &addRes); err != nil {
		t.Fatalf("lock.addSigner: %v", err)
	}

	// Wait for node B to sync and recognise itself as a trusted signer.
	waitFor(t, "node B trusted as signer", func() bool {
		st := nodeB.TrustStore()
		return st != nil && st.SignerTrusted(signerBPub)
	})

	// Dial node B's socket and sign a device as node B.
	var bConn net.Conn
	waitFor(t, "node B socket ready", func() bool {
		c, derr := net.Dial("unix", sockB)
		if derr != nil {
			return false
		}
		bConn = c
		return true
	})
	bc := api.NewClient(bConn)
	defer bc.Close()

	dev := bytes.Repeat([]byte{0xBC}, 32)
	var signRes api.LockDeviceResult
	if err := bc.Call(api.MethodLockSign, api.LockDeviceParams{Device: dev}, &signRes); err != nil {
		t.Fatalf("lock.sign by B: %v", err)
	}

	// Wait for the device to be authorized on node A (chain propagated via gateway).
	waitFor(t, "device authorized on node A", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.DeviceAuthorized(dev)
	})

	// Remove node B as signer (from node A, which is still the original signer).
	var removeRes api.LockDeviceResult
	if err := ac.Call(api.MethodLockRemoveSigner, api.LockSignerParams{Signer: signerBPub}, &removeRes); err != nil {
		t.Fatalf("lock.removeSigner: %v", err)
	}

	// Retroactive invalidation: the device authorized only by the now-removed
	// signer B must become unauthorized on both nodes.
	waitFor(t, "device revoked on node B after signer removal", func() bool {
		st := nodeB.TrustStore()
		return st != nil && !st.DeviceAuthorized(dev)
	})
	waitFor(t, "device revoked on node A after signer removal", func() bool {
		st := nodeA.TrustStore()
		return st != nil && !st.DeviceAuthorized(dev)
	})
}
