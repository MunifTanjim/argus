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

// TestLockDisablePropagatesAndStopsEnforcement proves the disable flow end-to-end:
// a locked node REFUSES an unauthorized client, then lock.disable turns enforcement
// OFF so the same unsigned client key is served.
func TestLockDisablePropagatesAndStopsEnforcement(t *testing.T) {
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

	// Capture node A's identity pubkey from the roster so it can be placed in
	// Devices. Including idA means the client-side (5b) will open a channel to
	// node A, which then lets the node-side (5a) reject the unauthorized client.
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

	// lock.init: authorize node A's identity so the client's 5b filter passes
	// it through (the node-side 5a check then rejects the unsigned client key).
	// GenDisablements:1 causes the node to generate one disablement secret.
	var initRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{
		GenDisablements: 1,
		Devices:         [][]byte{idA},
	}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	if len(initRes.DisablementSecrets) == 0 {
		t.Fatal("lock.init returned no disablement secrets")
	}
	secret := initRes.DisablementSecrets[0]

	// Wait until the genesis (with idA authorized) has propagated to the gateway.
	// The client syncs at Connect time; if the chain is empty the client-side 5b
	// filter excludes node A and NewReconnectingE2EClientLocked returns nil
	// instead of an error, which defeats the node-enforcement assertion below.
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

	// Generate a stable client identity that is NOT lock-signed.
	clientKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}

	// REFUSED: the node has enforcement on; clientKP is not authorized.
	// The node drops the Noise handshake (no msg2), so openChannel times out
	// after SetHandshakeTimeoutForTest and NewReconnectingE2EClientLocked fails.
	cUnauth, unauthErr := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, "")
	if unauthErr == nil {
		cUnauth.Close()
		t.Fatal("unauthorized client should be refused by the locked node")
	}

	// Disable enforcement: consume the disablement secret on node A.
	var disableRes api.LockDisableResult
	if err := ac.Call(api.MethodLockDisable, api.LockDisableParams{Secret: secret}, &disableRes); err != nil {
		t.Fatalf("lock.disable: %v", err)
	}
	if !disableRes.Disabled {
		t.Fatal("lock.disable returned Disabled=false")
	}

	// Wait until node A's own trust store reflects the KindDisable entry.
	waitFor(t, "node A trust store disabled", func() bool {
		st := nodeA.TrustStore()
		return st != nil && st.Disabled()
	})

	// Wait until the disable entry has propagated to the gateway so the fresh
	// client can sync the chain and see enforcement is off before it connects.
	waitFor(t, "disable propagated to gateway", func() bool {
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
		return st.Disabled()
	})

	// SERVED: a fresh locked client using the same unsigned clientKP now connects
	// because enforcement is off: the node skips the authorization check and the
	// client-side opens channels to all nodes (Disabled() is true on both sides).
	cPost, err := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, "")
	if err != nil {
		t.Fatalf("post-disable client should connect: %v", err)
	}
	defer cPost.Close()

	// A node-addressed call proves the full encrypted channel is functional.
	waitFor(t, "post-disable agents.list succeeds", func() bool {
		var agents api.AgentsListResult
		return cPost.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "node-a"}, &agents) == nil
	})
}
