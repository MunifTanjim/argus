package e2etest

import (
	"bytes"
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

// TestLockCLIRecovery is the PR 6a acceptance test: recovery over the local
// unix socket (the CLI path). An unpinned node on a locked network quarantines;
// lock.pin over the local socket clears it; lock.status reports the enforcing
// state; lock.local-disable is the escape hatch; and lock.* is refused over the
// co-located gateway dispatch path (LOCAL-ONLY).
func TestLockCLIRecovery(t *testing.T) {
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

	// Pinned node: seeds the gateway's entry store with a non-empty chain.
	pinnedKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("pinnedKP: %v", err)
	}
	// disKP is the local-disable node's identity key. It is authorized here so a
	// locked client can open a channel to it for the behavioral escape-hatch assertion.
	disKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("disKP: %v", err)
	}
	if err := tlog.AuthorizeDevice(authKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice(authKP): %v", err)
	}
	if err := tlog.AuthorizeDevice(disKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice(disKP): %v", err)
	}
	chain := trustlog.MarshalChain(tlog.Entries())

	dir := t.TempDir()
	pinnedChainPath := filepath.Join(dir, "pinned-chain")
	if err := os.WriteFile(pinnedChainPath, chain, 0o600); err != nil {
		t.Fatalf("write pinned chain: %v", err)
	}
	clientChainPath := filepath.Join(dir, "client-chain")
	if err := os.WriteFile(clientChainPath, chain, 0o600); err != nil {
		t.Fatalf("write client chain: %v", err)
	}

	pinnedNode := node.New()
	pinnedNode.SetIdentity("lcr-pinned", "LCR Pinned Node")
	pinnedNode.SetVersion("itest")
	pinnedNode.SetIdentityKey(pinnedKP)
	pinnedNode.SetE2EE(true)
	if err := pinnedNode.EnableTrustLog(genesisHash, pinnedChainPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	go pinnedNode.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	// Unpinned node: SetTrustChainPath only (no EnableTrustLog) → will quarantine
	// once the gateway advertises a non-empty chain.
	unpinnedKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("unpinnedKP: %v", err)
	}
	unpinnedChainPath := filepath.Join(dir, "unpinned-chain")

	unpinnedNode := node.New()
	unpinnedNode.SetIdentity("lcr-unpinned", "LCR Unpinned Node")
	unpinnedNode.SetVersion("itest")
	unpinnedNode.SetIdentityKey(unpinnedKP)
	unpinnedNode.SetE2EE(true)
	unpinnedNode.SetTrustChainPath(unpinnedChainPath)
	go unpinnedNode.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	// Start the unpinned node's local unix socket.
	socketPath := filepath.Join(dir, "unpinned.sock")
	sockCtx, sockCancel := context.WithCancel(ctx)
	defer sockCancel()
	sockDone := make(chan error, 1)
	go func() { sockDone <- unpinnedNode.Run(sockCtx, socketPath) }()
	t.Cleanup(func() {
		sockCancel()
		<-sockDone
	})

	// Wait for the socket to appear.
	waitFor(t, "unix socket ready", func() bool {
		_, err := os.Stat(socketPath)
		return err == nil
	})

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "both lcr nodes online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		pinned, unpinned := false, false
		for _, nd := range r.Nodes {
			switch {
			case nd.ID == "lcr-pinned" && nd.IdentityPubKey != "" && nd.Online:
				pinned = true
			case nd.ID == "lcr-unpinned" && nd.IdentityPubKey != "" && nd.Online:
				unpinned = true
			}
		}
		return pinned && unpinned
	})
	poll.Close()

	// The unpinned node must quarantine once it observes the non-empty chain.
	waitFor(t, "lcr-unpinned quarantined", func() bool {
		return unpinnedNode.Quarantined()
	})

	t.Run("default", func(t *testing.T) {
		// A node with no trust state configured: lock.status reports not-enabled.
		bareNode := node.New()
		bareNode.SetIdentity("lcr-bare", "LCR Bare")
		bareNode.SetVersion("itest")

		// dir is the outer t.TempDir(), whose path is short enough for a unix socket.
		bareSocketPath := filepath.Join(dir, "bare.sock")
		bareCtx, bareCancel := context.WithCancel(ctx)
		defer bareCancel()
		bareDone := make(chan error, 1)
		go func() { bareDone <- bareNode.Run(bareCtx, bareSocketPath) }()
		t.Cleanup(func() {
			bareCancel()
			<-bareDone
		})
		waitFor(t, "bare socket ready", func() bool {
			_, err := os.Stat(bareSocketPath)
			return err == nil
		})

		lc, err := api.Dial(bareSocketPath)
		if err != nil {
			t.Fatalf("dial bare socket: %v", err)
		}
		defer lc.Close()

		var st api.LockStatusResult
		if err := lc.Call(api.MethodLockStatus, nil, &st); err != nil {
			t.Fatalf("lock.status: %v", err)
		}
		if st.Enabled {
			t.Error("Enabled must be false when no trust genesis configured")
		}
		if st.Quarantined {
			t.Error("Quarantined must be false in TOFU mode")
		}
	})

	t.Run("pin", func(t *testing.T) {
		lc, err := api.Dial(socketPath)
		if err != nil {
			t.Fatalf("dial local socket: %v", err)
		}
		defer lc.Close()

		// lock.pin with the genesis hash the gateway is serving → clears quarantine.
		if err := lc.Call(api.MethodLockPin, api.LockPinParams{Genesis: genesisHash}, nil); err != nil {
			t.Fatalf("lock.pin: %v", err)
		}
		waitFor(t, "lock.pin clears quarantine", func() bool {
			return !unpinnedNode.Quarantined()
		})

		var st api.LockStatusResult
		if err := lc.Call(api.MethodLockStatus, nil, &st); err != nil {
			t.Fatalf("lock.status after pin: %v", err)
		}
		if !st.Enabled {
			t.Error("Enabled must be true after pin")
		}
		if !st.Pinned {
			t.Error("Pinned must be true after lock.pin")
		}
		if !bytes.Equal(st.PinGenesis, genesisHash) {
			t.Errorf("PinGenesis = %x, want %x", st.PinGenesis, genesisHash)
		}
		if st.Quarantined {
			t.Error("Quarantined must be false after recovery")
		}
	})

	t.Run("local-disable", func(t *testing.T) {
		// Re-quarantine a fresh unpinned node on the same gateway. disKP is authorized
		// in the chain so a locked client can open a channel to it for the behavioral
		// escape-hatch assertion after lock.local-disable.
		// Chain state in t.TempDir(); socket in the outer dir to keep the path
		// short enough for macOS's 104-byte sockaddr_un limit.
		disChainDir := t.TempDir()
		disNode := node.New()
		disNode.SetIdentity("lcr-dis", "LCR Disable")
		disNode.SetVersion("itest")
		disNode.SetIdentityKey(disKP)
		disNode.SetE2EE(true)
		disNode.SetTrustChainPath(filepath.Join(disChainDir, "dis-chain"))
		go disNode.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

		// dir is the outer t.TempDir(), whose path is short enough for a unix socket.
		disSocket := filepath.Join(dir, "dis.sock")
		disCtx, disCancel := context.WithCancel(ctx)
		defer disCancel()
		disDone := make(chan error, 1)
		go func() { disDone <- disNode.Run(disCtx, disSocket) }()
		t.Cleanup(func() {
			disCancel()
			<-disDone
		})
		waitFor(t, "dis socket ready", func() bool {
			_, err := os.Stat(disSocket)
			return err == nil
		})
		waitFor(t, "lcr-dis quarantined", func() bool {
			return disNode.Quarantined()
		})

		lc, err := api.Dial(disSocket)
		if err != nil {
			t.Fatalf("dial dis socket: %v", err)
		}
		defer lc.Close()

		if err := lc.Call(api.MethodLockLocalDisable, nil, nil); err != nil {
			t.Fatalf("lock.local-disable: %v", err)
		}

		var st api.LockStatusResult
		if err := lc.Call(api.MethodLockStatus, nil, &st); err != nil {
			t.Fatalf("lock.status after local-disable: %v", err)
		}
		if !st.LocalDisabled {
			t.Error("LocalDisabled must be true after lock.local-disable")
		}

		// Behavioral assertion: the escape hatch must allow real channels through the
		// gateway even though the quarantine gate is still tripped. disNode has no
		// trust store (unpinned/trust-nil), so after local-disable it accepts any
		// channel. The client uses the pinned path (locked client) so it can see the
		// network's trust log without quarantining and will open a channel to lcr-dis
		// (disKP.Public is authorized in the chain). agents.list must succeed.
		gwDial := func(ctx context.Context) (net.Conn, error) {
			return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		}
		c, err := client.NewReconnectingE2EClientLocked(ctx, gwDial, genesisHash, authKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		if err := c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "lcr-dis"}, &agents); err != nil {
			t.Fatalf("local-disable escape hatch: agents.list to quarantine-gated node failed: %v", err)
		}
	})

	t.Run("local-only", func(t *testing.T) {
		// lock.* must be rejected by the co-located gateway dispatch path.
		dispatch := unpinnedNode.DispatchFunc()
		ctx2 := context.Background()

		_, err := dispatch(ctx2, api.MethodLockPin, nil)
		if err == nil {
			t.Fatal("DispatchFunc must reject lock.pin")
		}
		rpcErr, ok := err.(*api.RPCError)
		if !ok || rpcErr.Code != api.CodeMethodNotFound {
			t.Errorf("want CodeMethodNotFound for lock.pin via DispatchFunc, got %v", err)
		}

		_, err = dispatch(ctx2, api.MethodLockStatus, nil)
		if err == nil {
			t.Fatal("DispatchFunc must reject lock.status")
		}
		rpcErr, ok = err.(*api.RPCError)
		if !ok || rpcErr.Code != api.CodeMethodNotFound {
			t.Errorf("want CodeMethodNotFound for lock.status via DispatchFunc, got %v", err)
		}
	})
}
