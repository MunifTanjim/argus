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

// TestLockSigning is the PR 6b acceptance test: lock.init via the local socket
// establishes a locked network; an authorized device connects through the gateway;
// lock.sign admits a second device; lock.revoke-device deauthorizes it.
func TestLockSigning(t *testing.T) {
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("nodeKP: %v", err)
	}
	signerKey, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	clientKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("clientKP: %v", err)
	}
	secondKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("secondKP: %v", err)
	}

	// dir is the outer TempDir whose path is short enough for macOS's 104-byte
	// sockaddr_un limit. Chain files live under a separate inner dir.
	dir := t.TempDir()
	nodeChainPath := filepath.Join(t.TempDir(), "node-chain")

	n := node.New()
	n.SetIdentity("ls-node", "Lock Signing Node")
	n.SetVersion("itest")
	n.SetIdentityKey(nodeKP)
	n.SetE2EE(true)
	n.SetSignerKey(signerKey)
	n.SetTrustChainPath(nodeChainPath)
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	socketPath := filepath.Join(dir, "ls.sock")
	sockCtx, sockCancel := context.WithCancel(ctx)
	defer sockCancel()
	sockDone := make(chan error, 1)
	go func() { sockDone <- n.Run(sockCtx, socketPath) }()
	t.Cleanup(func() {
		sockCancel()
		<-sockDone
	})

	waitFor(t, "ls.sock ready", func() bool {
		_, err := os.Stat(socketPath)
		return err == nil
	})

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "ls-node online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.ID == "ls-node" && nd.IdentityPubKey != "" && nd.Online {
				return true
			}
		}
		return false
	})
	poll.Close()

	t.Run("default", func(t *testing.T) {
		// A node with a signer key but no lock.init: lock.status reports not-enabled.
		bareKP, err := e2e.GenerateKeyPair()
		if err != nil {
			t.Fatalf("bareKP: %v", err)
		}
		bareSigner, err := trustlog.GenerateSigner()
		if err != nil {
			t.Fatalf("bareSigner: %v", err)
		}
		bareNode := node.New()
		bareNode.SetIdentity("ls-bare", "LS Bare")
		bareNode.SetVersion("itest")
		bareNode.SetIdentityKey(bareKP)
		bareNode.SetSignerKey(bareSigner)
		bareNode.SetTrustChainPath(filepath.Join(t.TempDir(), "bare-chain"))

		bareSocket := filepath.Join(dir, "ls-bare.sock")
		bareCtx, bareCancel := context.WithCancel(ctx)
		defer bareCancel()
		bareDone := make(chan error, 1)
		go func() { bareDone <- bareNode.Run(bareCtx, bareSocket) }()
		t.Cleanup(func() {
			bareCancel()
			<-bareDone
		})
		waitFor(t, "bare socket ready", func() bool {
			_, err := os.Stat(bareSocket)
			return err == nil
		})

		lc, err := api.Dial(bareSocket)
		if err != nil {
			t.Fatalf("dial bare socket: %v", err)
		}
		defer lc.Close()

		var st api.LockStatusResult
		if err := lc.Call(api.MethodLockStatus, nil, &st); err != nil {
			t.Fatalf("lock.status: %v", err)
		}
		if st.Enabled {
			t.Error("Enabled must be false when lock.init has not been called")
		}
	})

	// Dial the node's local socket for all subsequent signing RPCs.
	lc, err := api.Dial(socketPath)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	defer lc.Close()

	// lock.init: genesis with this node's signer + authorized client and node identity.
	// nodeKP.Public must be authorized so a locked client recognizes the node as trusted.
	// clientKP.Public must be authorized so the node accepts channels from the client.
	var initResult api.LockInitResult
	if err := lc.Call(api.MethodLockInit, api.LockInitParams{
		Signers:         [][]byte{signerKey.Public},
		Devices:         [][]byte{nodeKP.Public, clientKP.Public},
		GenDisablements: 1,
	}, &initResult); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	genesisHash := initResult.Tip

	var st api.LockStatusResult
	if err := lc.Call(api.MethodLockStatus, nil, &st); err != nil {
		t.Fatalf("lock.status after init: %v", err)
	}
	if !st.Enabled {
		t.Fatal("Enabled must be true after lock.init")
	}

	// Seed the client chain from the node's persisted chain file. This avoids a
	// race where the client syncs via the gateway before the node has pushed.
	chainBytes, err := os.ReadFile(nodeChainPath)
	if err != nil {
		t.Fatalf("read node chain: %v", err)
	}
	clientChainPath := filepath.Join(dir, "client-chain")
	if err := os.WriteFile(clientChainPath, chainBytes, 0o600); err != nil {
		t.Fatalf("write client chain: %v", err)
	}

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}

	t.Run("authorized", func(t *testing.T) {
		c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, clientKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		if err := c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "ls-node"}, &agents); err != nil {
			t.Fatalf("agents.list (authorized): %v", err)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		client.SetHandshakeTimeoutForTest(500 * time.Millisecond)
		t.Cleanup(func() { client.SetHandshakeTimeoutForTest(10 * time.Second) })

		unauthKP, err := e2e.GenerateKeyPair()
		if err != nil {
			t.Fatalf("unauthKP: %v", err)
		}
		// clientChainPath has nodeKP.Public authorized, so the client opens the channel;
		// but unauthKP.Public is absent from the trust store, so the node rejects it.
		c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, unauthKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		err = c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "ls-node"}, &agents)
		if err == nil {
			t.Fatal("unauthorized client must be rejected; agents.list succeeded")
		}
		if !isTransportErr(err) {
			t.Fatalf("enforcement did not fire — sealed call reached node handler: %v", err)
		}
	})

	t.Run("sign", func(t *testing.T) {
		var signResult api.LockDeviceResult
		if err := lc.Call(api.MethodLockSign, api.LockDeviceParams{Device: secondKP.Public}, &signResult); err != nil {
			t.Fatalf("lock.sign: %v", err)
		}
		if !signResult.Changed {
			t.Error("Changed must be true for a new device authorization")
		}

		// clientChainPath has nodeKP.Public authorized, so the client opens the channel;
		// the node now accepts secondKP.Public after lock.sign.
		c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, secondKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked (second): %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		if err := c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "ls-node"}, &agents); err != nil {
			t.Fatalf("agents.list (second device after sign): %v", err)
		}
	})

	t.Run("revoke", func(t *testing.T) {
		client.SetHandshakeTimeoutForTest(500 * time.Millisecond)
		t.Cleanup(func() { client.SetHandshakeTimeoutForTest(10 * time.Second) })

		var revokeResult api.LockDeviceResult
		if err := lc.Call(api.MethodLockRevoke, api.LockDeviceParams{Device: secondKP.Public}, &revokeResult); err != nil {
			t.Fatalf("lock.revoke: %v", err)
		}
		if !revokeResult.Changed {
			t.Error("Changed must be true for a device revocation")
		}

		// clientChainPath has nodeKP.Public authorized, so the client opens the channel;
		// but secondKP.Public is now revoked — the node rejects the handshake.
		c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, secondKP, clientChainPath)
		if err != nil {
			t.Fatalf("NewReconnectingE2EClientLocked (post-revoke): %v", err)
		}
		defer c.Close()

		var agents api.AgentsListResult
		err = c.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "ls-node"}, &agents)
		if err == nil {
			t.Fatal("revoked device must be rejected; agents.list succeeded")
		}
		if !isTransportErr(err) {
			t.Fatalf("enforcement did not fire after revoke — sealed call reached handler: %v", err)
		}
	})

	t.Run("disable", func(t *testing.T) {
		secret := initResult.DisablementSecrets[0]
		var disResult api.LockDisableResult
		if err := lc.Call(api.MethodLockDisable, api.LockDisableParams{Secret: secret}, &disResult); err != nil {
			t.Fatalf("lock.disable: %v", err)
		}
		if !disResult.Disabled {
			t.Error("Disabled must be true after lock.disable")
		}

		var st api.LockStatusResult
		if err := lc.Call(api.MethodLockStatus, nil, &st); err != nil {
			t.Fatalf("lock.status after disable: %v", err)
		}
		if !st.Disabled {
			t.Error("status.Disabled must be true after lock.disable")
		}
	})
}
