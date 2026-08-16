package e2etest

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
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

// TestLockEnforcement is the PR 5a acceptance test: on a pinned locked-mode
// network an authorized device connects and an unauthorized device is rejected.
func TestLockEnforcement(t *testing.T) {
	agg := gateway.New(time.Second, true)
	srv := gateway.NewServer(agg, nil, nil, true)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("node keypair: %v", err)
	}
	n := node.New()
	n.SetIdentity("le-node", "Lock Enforcement Node")
	n.SetVersion("itest")
	n.SetIdentityKey(nodeKP)
	n.SetE2EE(true)

	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	authKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("authKP: %v", err)
	}

	tlog, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()

	// Authorize the node's identity pub so locked clients open a channel to it.
	if err := tlog.AuthorizeDevice(nodeKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice(node): %v", err)
	}
	// Authorize the client's static so the node accepts its channel.
	if err := tlog.AuthorizeDevice(authKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice(authKP): %v", err)
	}
	chain := trustlog.MarshalChain(tlog.Entries())

	dir := t.TempDir()
	nodeChainPath := filepath.Join(dir, "node-trustlog")
	if err := os.WriteFile(nodeChainPath, chain, 0o600); err != nil {
		t.Fatalf("write node chain: %v", err)
	}
	if err := n.EnableTrustLog(genesisHash, nodeChainPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "le-node online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.ID == "le-node" && nd.IdentityPubKey != "" && nd.Online {
				return true
			}
		}
		return false
	})
	poll.Close()

	// The chain is also written to disk for the client so the trust store is seeded
	// from disk, sidestepping a race where the client syncs before the node pushes.
	clientChainPath := filepath.Join(dir, "client-trustlog")
	if err := os.WriteFile(clientChainPath, chain, 0o600); err != nil {
		t.Fatalf("write client chain: %v", err)
	}

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}

	t.Run("authorized", func(t *testing.T) {
		c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, authKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
		}
		defer c.Close()

		var roster api.NodesListResult
		if err := c.Call(api.MethodNodesList, nil, &roster); err != nil {
			t.Fatalf("nodes.list: %v", err)
		}
		if len(roster.Nodes) == 0 {
			t.Fatal("nodes.list: empty roster")
		}

		var agents api.AgentsListResult
		if err := c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "le-node"}, &agents); err != nil {
			t.Fatalf("agents.list over E2E (authorized): %v", err)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		// Short timeout so the test does not block for the production 10-second window
		// while waiting for a msg2 the node will never send.
		client.SetHandshakeTimeoutForTest(500 * time.Millisecond)
		t.Cleanup(func() { client.SetHandshakeTimeoutForTest(10 * time.Second) })

		unauthKP, err := e2e.GenerateKeyPair()
		if err != nil {
			t.Fatalf("unauthKP: %v", err)
		}
		// Same chain: the client's trust store accepts the node (nodeKP.Public is in
		// the chain) but the node rejects this client (unauthKP.Public is absent).
		c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, unauthKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		err = c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "le-node"}, &agents)
		if err == nil {
			t.Fatal("unauthorized client: agents.list succeeded; node should have rejected the channel")
		}
		if !isTransportErr(err) {
			// err is an *api.RPCError — the sealed call reached the node handler,
			// meaning enforcement did not fire (security defect in Task 3/4 wiring).
			t.Fatalf("unauthorized client: enforcement did not fire — sealed call reached node handler: %v", err)
		}
	})

	t.Run("tofu", func(t *testing.T) {
		// A separate gateway with a TOFU node (SetTrustChainPath only, no
		// EnableTrustLog) confirms that the default open-mode behavior is unchanged.
		tofuAgg := gateway.New(time.Second, true)
		tofuSrv := gateway.NewServer(tofuAgg, nil, nil, true)
		tofuTS := httptest.NewServer(tofuSrv.Handler())
		defer tofuTS.Close()

		tofuKP, err := e2e.GenerateKeyPair()
		if err != nil {
			t.Fatalf("TOFU node keypair: %v", err)
		}
		tn := node.New()
		tn.SetIdentity("tofu-node", "TOFU Node")
		tn.SetVersion("itest")
		tn.SetIdentityKey(tofuKP)
		tn.SetE2EE(true)
		tn.SetTrustChainPath(filepath.Join(t.TempDir(), "tofu-chain"))
		go tn.ConnectGateway(ctx, wsURL(tofuTS.URL, "/node"), "", nil)

		tofuPollConn, err := api.DialWSConn(ctx, wsURL(tofuTS.URL, "/client"), "", nil)
		if err != nil {
			t.Fatalf("TOFU poll dial: %v", err)
		}
		tofuPoll := api.NewClient(tofuPollConn)
		waitFor(t, "tofu-node online", func() bool {
			var r api.NodesListResult
			if tofuPoll.Call(api.MethodNodesList, nil, &r) != nil {
				return false
			}
			for _, nd := range r.Nodes {
				if nd.ID == "tofu-node" && nd.IdentityPubKey != "" && nd.Online {
					return true
				}
			}
			return false
		})
		tofuPoll.Close()

		tofuDial := func(ctx context.Context) (net.Conn, error) {
			return api.DialWSConn(ctx, wsURL(tofuTS.URL, "/client"), "", nil)
		}
		c, err := client.NewReconnectingE2EClient(ctx, tofuDial)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClient (TOFU): %v", err)
		}
		defer c.Close()

		var roster api.NodesListResult
		if err := c.Call(api.MethodNodesList, nil, &roster); err != nil {
			t.Fatalf("nodes.list (TOFU): %v", err)
		}
		var agents api.AgentsListResult
		if err := c.Call(api.MethodAgentsList, nil, &agents); err != nil {
			t.Fatalf("agents.list over E2E (TOFU): %v", err)
		}
	})
}
