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
	if err := nodeB.EnableTrustLog(initRes.Head, chainPathB); err != nil {
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
