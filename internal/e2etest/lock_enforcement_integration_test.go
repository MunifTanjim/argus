package e2etest

import (
	"context"
	"encoding/base64"
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
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// TestLockedNodeEnforcesAuthorizedClient proves the crown-jewel E2E enforcement
// invariant: a locked node refuses an unauthorized client's channel, then accepts
// the same client identity after lock.sign.
//
// Adaptation from the task brief: because the node enforces authorization at
// handshake time (by not responding with msg2), the client's Connect() fails
// (handshake timeout) rather than succeeding with a subsequent call failure. The
// invariant is proved with two client attempts — unauthorized (Connect fails) and
// authorized (Connect + Call both succeed) — using the same clientKP identity.
// SetHandshakeTimeoutForTest shortens the wait from 10 s to 300 ms so the
// unauthorized phase is fast enough for -count=3 stability runs.
func TestLockedNodeEnforcesAuthorizedClient(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetHandshakeTimeoutForTest(300 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(30 * time.Second)
		client.SetTrustSyncIntervalForTest(30 * time.Second)
		client.SetHandshakeTimeoutForTest(10 * time.Second)
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

	// Wait until node A appears on the roster with its identity key populated.
	waitFor(t, "node rostered with identity key", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var r api.NodesListResult
		return api.NewClient(pc).Call(api.MethodNodesList, nil, &r) == nil &&
			len(r.Nodes) == 1 && r.Nodes[0].IdentityPubKey != ""
	})

	// Capture node A's identity pubkey from the roster so it can be authorized
	// in lock.init — the client-side enforcement (Slice 5b) silently skips nodes
	// whose identity is not in Devices, so without this the client opens zero
	// channels and NewReconnectingE2EClientLocked returns nil instead of an error.
	var idA []byte
	{
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			t.Fatalf("roster dial: %v", err)
		}
		var roster api.NodesListResult
		if err := api.NewClient(pc).Call(api.MethodNodesList, nil, &roster); err != nil {
			t.Fatalf("nodes.list: %v", err)
		}
		pc.Close()
		for _, nd := range roster.Nodes {
			if nd.ID == "node-a" {
				b, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
				if err != nil {
					t.Fatalf("decode node-a identity: %v", err)
				}
				idA = b
			}
		}
		if len(idA) == 0 {
			t.Fatal("could not resolve node-a identity from roster")
		}
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

	// lock.init on A — A is the sole signer. Node A's identity is in Devices so
	// the client-side enforcement (Slice 5b) will attempt a channel to node A,
	// letting node A's enforcement (Slice 5a) reject the unauthorized client.
	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{Devices: [][]byte{idA}}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}

	// Wait until node A has pushed the genesis (with idA) to the gateway. The
	// client's trust store syncs at Connect time; if the gateway chain is empty
	// when the client connects, DeviceAuthorized(idA) returns false and the
	// client silently excludes node A — defeating the node-enforcement test.
	waitFor(t, "genesis propagated to gateway", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var got api.TrustLogPullResult
		if err := api.NewClient(pc).Call(api.MethodTrustLogPull, nil, &got); err != nil || len(got.Chains) == 0 {
			return false
		}
		st := trustlog.NewSyncStore(initRes.Tip)
		for _, chain := range got.Chains {
			st.Ingest(chain) //nolint:errcheck
		}
		return st.DeviceAuthorized(idA)
	})

	// Stable client identity the test controls so it can authorize exactly this key.
	clientKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}

	// UNAUTHORIZED: the node has a trust store with no authorized clients.
	// Connect() fails because the node drops the Noise handshake (no msg2 response),
	// causing openChannel to time out after SetHandshakeTimeoutForTest duration.
	cUnauth, unauthErr := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, "")
	if unauthErr == nil {
		cUnauth.Close()
		t.Fatal("unauthorized client should be refused by the locked node")
	}

	// Authorize the client's identity on the signer node.
	if _, err := lockSign(ac, clientKP.Public); err != nil {
		t.Fatalf("lock.sign: %v", err)
	}
	// Wait until node A's own trust store reflects the new device entry (the sync
	// loop propagates the chain; here we check the source node directly).
	waitFor(t, "node authorizes client", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.DeviceAuthorized(clientKP.Public)
	})

	// AUTHORIZED: reconnect with the same key — the node now accepts the handshake.
	// This is the "reconnect after authorize" step: a fresh Connect() with the same
	// clientKP presents the same static, which the node now finds in its trust store.
	cAuth, err := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, "")
	if err != nil {
		t.Fatalf("authorized client should connect: %v", err)
	}
	defer cAuth.Close()

	var agents api.AgentsListResult
	if err := cAuth.Call(api.MethodAgentsList, nil, &agents); err != nil {
		t.Fatalf("authorized call failed: %v", err)
	}
}

// lockSign authorizes a device key on a signer node via the unix socket client.
func lockSign(c *api.Client, dev []byte) (api.LockDeviceResult, error) {
	var res api.LockDeviceResult
	err := c.Call(api.MethodLockSign, api.LockDeviceParams{Device: dev}, &res)
	return res, err
}
