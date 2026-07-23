package e2etest

import (
	"context"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// TestClientExcludesUnauthorizedNode proves symmetric client-side enforcement:
// a locked client syncs the trust log at Connect time, opens a channel only to
// node A (whose identity is authorized), and silently excludes node B (whose
// identity is not in the authorized-devices set).
//
// Assertion strategy: after Connect, a node-addressed call to node-a succeeds
// (channel exists); a node-addressed call to node-b fails with "no channel to
// node" (callNode returns that error when byNode[nodeID] is nil, which it is
// because Connect skipped node-b during silent exclusion).
func TestClientExcludesUnauthorizedNode(t *testing.T) {
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
	nodeA := startLockNode(t, ctx, "node-a", ts.URL, sockA, filepath.Join(dir, "a-chain"))
	_ = startLockNode(t, ctx, "node-b", ts.URL, filepath.Join(dir, "b.sock"), filepath.Join(dir, "b-chain"))

	// Wait until both nodes are rostered with signer and identity keys.
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
		return true
	})

	// Capture the roster while both nodes are present.
	var roster api.NodesListResult
	{
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			t.Fatalf("roster dial: %v", err)
		}
		if err := api.NewClient(pc).Call(api.MethodNodesList, nil, &roster); err != nil {
			t.Fatalf("nodes.list: %v", err)
		}
		pc.Close()
	}

	// Resolve node A's identity key and node B's signer key from the roster.
	var sigB, idA []byte
	for _, nd := range roster.Nodes {
		switch nd.ID {
		case "node-a":
			b, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
			if err != nil {
				t.Fatalf("decode node-a identity: %v", err)
			}
			idA = b
		case "node-b":
			b, err := base64.StdEncoding.DecodeString(nd.SignerPubKey)
			if err != nil {
				t.Fatalf("decode node-b signer: %v", err)
			}
			sigB = b
		}
	}
	if len(idA) == 0 || len(sigB) == 0 {
		t.Fatal("could not resolve node keys from roster")
	}

	// Dial node A's unix socket (it becomes ready shortly after the node starts).
	var aConn net.Conn
	waitFor(t, "node A socket ready", func() bool {
		c, err := net.Dial("unix", sockA)
		if err != nil {
			return false
		}
		aConn = c
		return true
	})
	ac := api.NewClient(aConn)
	defer ac.Close()

	// lock.init: node B's signer key is included (for co-signing), but ONLY node
	// A's identity key is in Devices — node B is NOT an authorized device.
	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{
		Signers: [][]byte{sigB},
		Devices: [][]byte{idA},
	}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}

	// Generate a stable client identity and authorize it on node A so the node
	// accepts the client's Noise handshake (5a node-side enforcement).
	clientKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate client keypair: %v", err)
	}
	if _, err := lockSign(ac, clientKP.Public); err != nil {
		t.Fatalf("lock.sign client: %v", err)
	}

	// Wait until node A's trust store reflects the client's authorization.
	waitFor(t, "node A authorizes client", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.DeviceAuthorized(clientKP.Public)
	})

	// Wait until node A has pushed the full chain (including the lock.sign entry) to
	// the gateway. Connect() only syncs once at startup; if the gateway chain is empty
	// or stale, the client's trust store stays at genesis with no authorized devices
	// and silently excludes ALL nodes, including node A. Poll until the chain is live.
	waitFor(t, "chain propagated to gateway", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var got api.TrustLogChain
		if err := api.NewClient(pc).Call(api.MethodTrustLogPull, nil, &got); err != nil || len(got.Chain) == 0 {
			return false
		}
		st := trustlog.NewSyncStore(initRes.Tip)
		_, err = st.Ingest(got.Chain)
		return err == nil && st.DeviceAuthorized(clientKP.Public)
	})

	// Build the locked client: it syncs the trust log at Connect time, then for
	// each rostered node checks DeviceAuthorized(nd.IdentityPubKey). Node A is
	// authorized; node B is not — node B is silently excluded (no channel opened).
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, filepath.Join(dir, "client-chain"))
	if err != nil {
		t.Fatalf("locked client: %v", err)
	}
	defer c.Close()

	// AUTHORIZED: node-a-addressed call succeeds (channel was opened during Connect).
	var agentsA api.AgentsListResult
	if err := c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "node-a"}, &agentsA); err != nil {
		t.Fatalf("call to authorized node-a failed: %v", err)
	}

	// EXCLUDED: node-b-addressed call fails with "no channel to node" because the
	// client silently skipped node B during Connect (its identity is not authorized).
	var agentsB api.AgentsListResult
	err = c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "node-b"}, &agentsB)
	if err == nil {
		t.Fatal("call to unauthorized node-b should fail (no channel opened)")
	}
	if !strings.Contains(err.Error(), "no channel to node") {
		t.Fatalf("unexpected error for node-b (want \"no channel to node\"): %v", err)
	}
}
