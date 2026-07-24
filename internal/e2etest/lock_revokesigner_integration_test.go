package e2etest

import (
	"bytes"
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

// TestLockRevokeSignerCeremony proves the full revoke-signer co-signing ceremony
// works end-to-end across three real signer nodes connected through a real gateway:
//
//  1. Three signer nodes {a, b, c} initialise locked mode.
//  2. Compromised node c authorises device_c (and device_c2 as a competing entry).
//  3. The ceremony: a starts the revoke (revoked={c}), b co-signs, a finishes.
//  4. The gateway now holds two branches: c's extended chain and a's fork chain.
//  5. Phase 3 fork resolution picks a's fork (2 co-signs > 1 revoked signer).
//  6. All nodes converge: c is untrusted; device_c and device_c2 are revoked.
func TestLockRevokeSignerCeremony(t *testing.T) {
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

	ctx := t.Context()
	dir := t.TempDir()

	sockA := filepath.Join(dir, "a.sock")
	sockB := filepath.Join(dir, "b.sock")
	sockC := filepath.Join(dir, "c.sock")

	nodeA := startLockNode(t, ctx, "node-a", ts.URL, sockA, filepath.Join(dir, "chainA"))
	nodeB := startLockNode(t, ctx, "node-b", ts.URL, sockB, filepath.Join(dir, "chainB"))
	nodeC := startLockNode(t, ctx, "node-c", ts.URL, sockC, filepath.Join(dir, "chainC"))

	// Wait until all three nodes appear on the roster with signer keys.
	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	var roster api.NodesListResult
	waitFor(t, "all three nodes rostered with signer keys", func() bool {
		if poll.Call(api.MethodNodesList, nil, &roster) != nil || len(roster.Nodes) != 3 {
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

	// Resolve signer pubkeys for all three nodes from the roster.
	var signerAPub, signerBPub, signerCPub []byte
	for _, nd := range roster.Nodes {
		switch nd.ID {
		case "node-a":
			signerAPub, _ = base64.StdEncoding.DecodeString(nd.SignerPubKey)
		case "node-b":
			signerBPub, _ = base64.StdEncoding.DecodeString(nd.SignerPubKey)
		case "node-c":
			signerCPub, _ = base64.StdEncoding.DecodeString(nd.SignerPubKey)
		}
	}
	if len(signerAPub) == 0 || len(signerBPub) == 0 || len(signerCPub) == 0 {
		t.Fatal("signer keys not found in roster")
	}

	// Dial node A's socket and call lock.init with B and C as additional signers.
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

	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{Signers: [][]byte{signerBPub, signerCPub}}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	if initRes.SignerCount != 3 {
		t.Fatalf("SignerCount = %d, want 3", initRes.SignerCount)
	}

	// Enable trust log on B and C so they pull the genesis from the gateway.
	if err := nodeB.EnableTrustLog(initRes.Tip, filepath.Join(dir, "chainB")); err != nil {
		t.Fatalf("enable B: %v", err)
	}
	if err := nodeC.EnableTrustLog(initRes.Tip, filepath.Join(dir, "chainC")); err != nil {
		t.Fatalf("enable C: %v", err)
	}

	// Wait until all three nodes have synced the genesis and see each other as signers.
	waitFor(t, "node B sees itself as signer", func() bool {
		st := nodeB.TrustStore()
		return st != nil && st.SignerTrusted(signerBPub)
	})
	waitFor(t, "node C sees itself as signer", func() bool {
		st := nodeC.TrustStore()
		return st != nil && st.SignerTrusted(signerCPub)
	})

	// Dial node C's socket.
	var cConn net.Conn
	waitFor(t, "node C socket ready", func() bool {
		c, derr := net.Dial("unix", sockC)
		if derr != nil {
			return false
		}
		cConn = c
		return true
	})
	cc := api.NewClient(cConn)
	defer cc.Close()

	// Compromised node C authorises a device.
	deviceC := bytes.Repeat([]byte{0xCC}, 32)
	var signCRes api.LockDeviceResult
	if err := cc.Call(api.MethodLockSign, api.LockDeviceParams{Device: deviceC}, &signCRes); err != nil {
		t.Fatalf("lock.sign by C: %v", err)
	}

	// Wait for device_c to propagate to node A (confirms C's chain is live on the gateway).
	waitFor(t, "device_c authorized on node A", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.DeviceAuthorized(deviceC)
	})

	// Compromised node C also signs a second device, extending its chain further.
	// This creates a competing branch when the revoke fork chain is offered later.
	deviceC2 := bytes.Repeat([]byte{0xC2}, 32)
	var signC2Res api.LockDeviceResult
	if err := cc.Call(api.MethodLockSign, api.LockDeviceParams{Device: deviceC2}, &signC2Res); err != nil {
		t.Fatalf("lock.sign device_c2 by C: %v", err)
	}

	// Ensure device_c2 has propagated to the gateway before starting the revoke
	// ceremony so the fork-choice genuinely races two competing branches: C's
	// chain (device_c + device_c2) versus A's fork chain.
	waitFor(t, "device_c2 propagated to node A before revoke", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.DeviceAuthorized(deviceC2)
	})

	// --- Co-signing ceremony ---

	// Step 1: node A starts the revoke (revoked = {c}).
	var startRes api.LockRevokeSignerBlobResult
	if err := ac.Call(api.MethodLockRevokeSignerStart, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{signerCPub},
	}, &startRes); err != nil {
		t.Fatalf("lock.revokeSignerStart: %v", err)
	}
	blob := startRes.Blob
	if len(blob) == 0 {
		t.Fatal("revokeSignerStart returned empty blob")
	}

	// Step 2: node B co-signs.
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

	var cosignRes api.LockRevokeSignerBlobResult
	if err := bc.Call(api.MethodLockRevokeSignerCosign, api.LockRevokeSignerCosignParams{Blob: blob}, &cosignRes); err != nil {
		t.Fatalf("lock.revokeSignerCosign: %v", err)
	}
	blob = cosignRes.Blob
	if len(blob) == 0 {
		t.Fatal("revokeSignerCosign returned empty blob")
	}

	// Step 3: node A finishes — builds the fork chain and ingests it locally.
	// At this point, node C's chain (with device_c and device_c2) is still on the
	// gateway as a competing branch. After A's finish, the gateway holds two branches:
	// C's extended chain and A's fork chain. Phase 3's forkChoice picks A's fork.
	var finishRes api.LockRevokeSignerFinishResult
	if err := ac.Call(api.MethodLockRevokeSignerFinish, api.LockRevokeSignerFinishParams{Blob: blob}, &finishRes); err != nil {
		t.Fatalf("lock.revokeSignerFinish: %v", err)
	}
	if len(finishRes.Tip) == 0 {
		t.Fatal("revokeSignerFinish returned empty tip")
	}

	// --- Assertions ---

	// C must become untrusted on all three nodes (fork chain wins).
	waitFor(t, "node A: c is untrusted", func() bool {
		st := nodeA.TrustStore()
		return st != nil && !st.SignerTrusted(signerCPub)
	})
	waitFor(t, "node B: c is untrusted", func() bool {
		st := nodeB.TrustStore()
		return st != nil && !st.SignerTrusted(signerCPub)
	})
	waitFor(t, "node C: c is untrusted (fork chain propagated)", func() bool {
		st := nodeC.TrustStore()
		return st != nil && !st.SignerTrusted(signerCPub)
	})

	// A and B must remain trusted signers on all three nodes after fork resolution.
	// A bug that accidentally revokes A or B must make these assertions fail.
	waitFor(t, "node A: A is still trusted signer", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.SignerTrusted(signerAPub)
	})
	waitFor(t, "node A: B is still trusted signer", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.SignerTrusted(signerBPub)
	})
	waitFor(t, "node B: A is still trusted signer", func() bool {
		st := nodeB.TrustStore()
		return st != nil && st.SignerTrusted(signerAPub)
	})
	waitFor(t, "node B: B is still trusted signer", func() bool {
		st := nodeB.TrustStore()
		return st != nil && st.SignerTrusted(signerBPub)
	})
	waitFor(t, "node C: A is still trusted signer", func() bool {
		st := nodeC.TrustStore()
		return st != nil && st.SignerTrusted(signerAPub)
	})
	waitFor(t, "node C: B is still trusted signer", func() bool {
		st := nodeC.TrustStore()
		return st != nil && st.SignerTrusted(signerBPub)
	})

	// The fork chain erases C's entries: device_c and device_c2 must be unauthorized.
	waitFor(t, "device_c revoked on node A (fork erased C's actions)", func() bool {
		st := nodeA.TrustStore()
		return st != nil && !st.DeviceAuthorized(deviceC)
	})
	waitFor(t, "device_c2 revoked on node A (fork erased C's competing entry)", func() bool {
		st := nodeA.TrustStore()
		return st != nil && !st.DeviceAuthorized(deviceC2)
	})
	waitFor(t, "device_c2 revoked on node B", func() bool {
		st := nodeB.TrustStore()
		return st != nil && !st.DeviceAuthorized(deviceC2)
	})
	waitFor(t, "device_c2 revoked on node C (accepted the fork chain)", func() bool {
		st := nodeC.TrustStore()
		return st != nil && !st.DeviceAuthorized(deviceC2)
	})
	waitFor(t, "device_c revoked on node B", func() bool {
		st := nodeB.TrustStore()
		return st != nil && !st.DeviceAuthorized(deviceC)
	})
	waitFor(t, "device_c revoked on node C (accepted the fork chain)", func() bool {
		st := nodeC.TrustStore()
		return st != nil && !st.DeviceAuthorized(deviceC)
	})
}
