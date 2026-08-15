package e2etest

import (
	"bytes"
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

// TestLockDisablePropagatesAndStopsEnforcement proves the disable flow end-to-end:
// a locked node REFUSES an unauthorized client, then lock.disable turns enforcement
// OFF so the same unsigned client key is served.
func TestLockDisablePropagatesAndStopsEnforcement(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetHandshakeTimeoutForTest(300 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(5 * time.Minute)
		client.SetTrustSyncIntervalForTest(5 * time.Minute)
		client.SetHandshakeTimeoutForTest(10 * time.Second)
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
		Signers:         [][]byte{nodeA.SignerPublic()},
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
	// The client syncs at Connect time; if the chain is empty the client-side
	// filter excludes node A so no channel is attempted and node-side enforcement
	// is never exercised.
	waitFor(t, "genesis propagated to gateway", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var got api.TrustLogSyncResult
		if err := api.NewClient(pc).Call(api.MethodTrustLogSync, api.TrustLogSyncParams{}, &got); err != nil || len(got.Entries) == 0 {
			return false
		}
		st := trustlog.NewSyncStore(initRes.Tip)
		for _, chain := range trustlog.AssembleChains(got.Entries) {
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

	// REFUSED: clientKP is not authorized. The node drops the Noise handshake (no
	// msg2); Connect() returns nil, but byNode is empty so any node-addressed call
	// fails with "no channel to node".
	cUnauth, err := client.NewReconnectingE2EClientLocked(ctx, dial, initRes.Tip, clientKP, "")
	if err != nil {
		t.Fatalf("Connect should succeed even for unauthorized client: %v", err)
	}
	defer cUnauth.Close()
	var agentsUnauth api.AgentsListResult
	unauthCallErr := cUnauth.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "node-a"}, &agentsUnauth)
	if unauthCallErr == nil {
		t.Fatal("unauthorized client should have no channel to node-a")
	}
	if !strings.Contains(unauthCallErr.Error(), "no channel to node") {
		t.Fatalf("unexpected error (want \"no channel to node\"): %v", unauthCallErr)
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
		var got api.TrustLogSyncResult
		if err := api.NewClient(pc).Call(api.MethodTrustLogSync, api.TrustLogSyncParams{}, &got); err != nil || len(got.Entries) == 0 {
			return false
		}
		st := trustlog.NewSyncStore(initRes.Tip)
		for _, chain := range trustlog.AssembleChains(got.Entries) {
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

	// RE-INIT: disable + reinit is the documented recovery path, so a disabled log
	// must not block lock.init. The new genesis is a new network — enforcement is
	// back on and the still-unsigned clientKP is refused again.
	var reinitRes api.LockInitResult
	if err := ac.Call(api.MethodLockInit, api.LockInitParams{
		Signers:         [][]byte{nodeA.SignerPublic()},
		GenDisablements: 1,
		Devices:         [][]byte{idA},
	}, &reinitRes); err != nil {
		t.Fatalf("lock.init after disable: %v", err)
	}
	if bytes.Equal(reinitRes.Tip, initRes.Tip) {
		t.Fatal("re-init must create a new genesis")
	}
	if st := nodeA.TrustStore(); st == nil || st.Disabled() {
		t.Fatal("node A must be enabled again after re-init")
	}
	waitFor(t, "re-init genesis propagated to gateway", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		defer pc.Close()
		var got api.TrustLogSyncResult
		if err := api.NewClient(pc).Call(api.MethodTrustLogSync, api.TrustLogSyncParams{}, &got); err != nil {
			return false
		}
		st := trustlog.NewSyncStore(reinitRes.Tip)
		for _, chain := range trustlog.AssembleChains(got.Entries) {
			st.Ingest(chain) //nolint:errcheck
		}
		return st.DeviceAuthorized(idA) && !st.Disabled()
	})

	cReinit, err := client.NewReconnectingE2EClientLocked(ctx, dial, reinitRes.Tip, clientKP, "")
	if err != nil {
		t.Fatalf("Connect should succeed even for unauthorized client: %v", err)
	}
	defer cReinit.Close()
	waitFor(t, "re-init client refused again", func() bool {
		var agents api.AgentsListResult
		err := cReinit.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "node-a"}, &agents)
		return err != nil && strings.Contains(err.Error(), "no channel to node")
	})

	// Signing the same key into the NEW chain serves it: the refusal above is the
	// new genesis enforcing, not a client that never synced a chain.
	var signRes api.LockDeviceResult
	if err := ac.Call(api.MethodLockSign, api.LockDeviceParams{Device: clientKP.Public}, &signRes); err != nil {
		t.Fatalf("lock.sign after re-init: %v", err)
	}
	cSigned, err := client.NewReconnectingE2EClientLocked(ctx, dial, reinitRes.Tip, clientKP, "")
	if err != nil {
		t.Fatalf("signed client connect: %v", err)
	}
	defer cSigned.Close()
	waitFor(t, "signed client served on the new chain", func() bool {
		var agents api.AgentsListResult
		return cSigned.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "node-a"}, &agents) == nil
	})
}
