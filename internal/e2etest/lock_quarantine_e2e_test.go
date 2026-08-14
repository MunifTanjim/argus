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

// TestLockQuarantine is the PR 5b acceptance test: an unpinned node on a locked
// network quarantines (refuses all channels) and recovers when pinned via AdoptPin.
// The default TOFU case (no chain anywhere) is unchanged.
func TestLockQuarantine(t *testing.T) {
	node.SetTrustSyncIntervalForTest(100 * time.Millisecond)
	t.Cleanup(func() { node.SetTrustSyncIntervalForTest(5 * time.Minute) })
	node.SetTriggeredPullIntervalForTest(50 * time.Millisecond)
	t.Cleanup(node.ResetTriggeredPullIntervalForTest)

	agg := gateway.New(time.Second, true)
	srv := gateway.NewServer(agg, nil, nil, true)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	// Pinned node (the authority that seeds the gateway's entry store).
	pinnedKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("pinnedKP: %v", err)
	}
	pinnedNode := node.New()
	pinnedNode.SetIdentity("lq-pinned", "LQ Pinned Node")
	pinnedNode.SetVersion("itest")
	pinnedNode.SetIdentityKey(pinnedKP)
	pinnedNode.SetE2EE(true)

	// Unpinned node (the subject of the quarantine test). Its identity pub is
	// authorized so a locked client can open a channel to it after recovery.
	unpinnedKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("unpinnedKP: %v", err)
	}
	if err := tlog.AuthorizeDevice(unpinnedKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice(unpinnedKP): %v", err)
	}
	// authKP is the client static the node accepts post-recovery.
	if err := tlog.AuthorizeDevice(authKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice(authKP): %v", err)
	}
	chain := trustlog.MarshalChain(tlog.Entries())

	dir := t.TempDir()
	pinnedChainPath := filepath.Join(dir, "pinned-chain")
	if err := os.WriteFile(pinnedChainPath, chain, 0o600); err != nil {
		t.Fatalf("write pinned chain: %v", err)
	}
	if err := pinnedNode.EnableTrustLog(genesisHash, pinnedChainPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	go pinnedNode.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	unpinnedNode := node.New()
	unpinnedNode.SetIdentity("lq-unpinned", "LQ Unpinned Node")
	unpinnedNode.SetVersion("itest")
	unpinnedNode.SetIdentityKey(unpinnedKP)
	unpinnedNode.SetE2EE(true)
	unpinnedChainPath := filepath.Join(dir, "unpinned-chain")
	unpinnedNode.SetTrustChainPath(unpinnedChainPath)
	go unpinnedNode.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	// The chain is also written to disk for locked clients so their trust stores are
	// seeded before Connect(), avoiding a race where the client syncs before the
	// pinned node has pushed.
	clientChainPath := filepath.Join(dir, "client-chain")
	if err := os.WriteFile(clientChainPath, chain, 0o600); err != nil {
		t.Fatalf("write client chain: %v", err)
	}

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "both lq nodes online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		pinned, unpinned := false, false
		for _, nd := range r.Nodes {
			switch {
			case nd.ID == "lq-pinned" && nd.IdentityPubKey != "" && nd.Online:
				pinned = true
			case nd.ID == "lq-unpinned" && nd.IdentityPubKey != "" && nd.Online:
				unpinned = true
			}
		}
		return pinned && unpinned
	})
	poll.Close()

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}

	t.Run("quarantine", func(t *testing.T) {
		waitFor(t, "lq-unpinned quarantined", func() bool {
			return unpinnedNode.Quarantined()
		})

		// Short timeout so the test does not block for the production 10-second window
		// while waiting for a msg2 the quarantined node will never send.
		client.SetHandshakeTimeoutForTest(500 * time.Millisecond)
		t.Cleanup(func() { client.SetHandshakeTimeoutForTest(10 * time.Second) })

		// Locked client: trust store knows lq-unpinned's identity pub so it opens a
		// channel to it; the quarantined node drops the handshake regardless.
		c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, authKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		err = c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "lq-unpinned"}, &agents)
		if err == nil {
			t.Fatal("quarantined node: agents.list succeeded; node should have refused the channel")
		}
		if !isTransportErr(err) {
			t.Fatalf("quarantined node: enforcement did not fire — sealed call reached handler: %v", err)
		}
	})

	t.Run("recovery", func(t *testing.T) {
		if err := unpinnedNode.AdoptPin(genesisHash); err != nil {
			t.Fatalf("AdoptPin: %v", err)
		}
		if unpinnedNode.Quarantined() {
			t.Fatal("AdoptPin must clear the quarantine gate")
		}

		t.Run("authorized", func(t *testing.T) {
			c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, authKP, clientChainPath)
			if err != nil {
				t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
			}
			defer c.Close()

			var agents api.AgentsListResult
			if err := c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "lq-unpinned"}, &agents); err != nil {
				t.Fatalf("authorized client: agents.list failed after recovery: %v", err)
			}
		})

		t.Run("unauthorized", func(t *testing.T) {
			client.SetHandshakeTimeoutForTest(500 * time.Millisecond)
			t.Cleanup(func() { client.SetHandshakeTimeoutForTest(10 * time.Second) })

			unauthKP, err := e2e.GenerateKeyPair()
			if err != nil {
				t.Fatalf("unauthKP: %v", err)
			}
			cu, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, unauthKP, clientChainPath)
			if err != nil {
				t.Fatalf("unauthorized client: NewReconnectingE2EClientLocked: %v", err)
			}
			defer cu.Close()

			var uagents api.AgentsListResult
			err = cu.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "lq-unpinned"}, &uagents)
			if err == nil {
				t.Fatal("unauthorized client: agents.list succeeded after recovery; enforcement should reject it")
			}
			if !isTransportErr(err) {
				t.Fatalf("unauthorized client: enforcement did not fire — sealed call reached handler: %v", err)
			}
		})
	})

	t.Run("tofu", func(t *testing.T) {
		// A separate gateway serving NO chain. An unpinned node must NOT quarantine.
		tofuAgg := gateway.New(time.Second, true)
		tofuSrv := gateway.NewServer(tofuAgg, nil, nil, true)
		tofuTS := httptest.NewServer(tofuSrv.Handler())
		defer tofuTS.Close()

		tofuKP, err := e2e.GenerateKeyPair()
		if err != nil {
			t.Fatalf("TOFU keypair: %v", err)
		}
		tn := node.New()
		tn.SetIdentity("lq-tofu", "LQ TOFU Node")
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
		waitFor(t, "lq-tofu online", func() bool {
			var r api.NodesListResult
			if tofuPoll.Call(api.MethodNodesList, nil, &r) != nil {
				return false
			}
			for _, nd := range r.Nodes {
				if nd.ID == "lq-tofu" && nd.IdentityPubKey != "" && nd.Online {
					return true
				}
			}
			return false
		})
		tofuPoll.Close()

		if tn.Quarantined() {
			t.Fatal("unpinned node on a TOFU network must not quarantine")
		}

		tofuDial := func(ctx context.Context) (net.Conn, error) {
			return api.DialWSConn(ctx, wsURL(tofuTS.URL, "/client"), "", nil)
		}
		c, err := client.NewReconnectingE2EClient(ctx, tofuDial)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClient (TOFU): %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		if err := c.Call(api.MethodAgentsList, nil, &agents); err != nil {
			t.Fatalf("agents.list over E2E (TOFU): %v", err)
		}
	})
}
