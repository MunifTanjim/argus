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

func TestTrustLogDistributionThroughRealGateway(t *testing.T) {
	node.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	client.SetTrustSyncIntervalForTest(50 * time.Millisecond)
	t.Cleanup(func() {
		node.SetTrustSyncIntervalForTest(30 * time.Second)
		client.SetTrustSyncIntervalForTest(30 * time.Second)
	})

	// Seed a chain: genesis + authorize one device and the test client.
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := log.Tip()
	device := []byte("device-key-0000000000000000000000")[:32]
	if err := log.AuthorizeDevice(device, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	// The client needs a stable authorized key; node enforcement rejects ephemeral
	// keys once a trust store is active.
	clientKP, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}
	if err := log.AuthorizeDevice(clientKP.Public, signer); err != nil {
		t.Fatalf("AuthorizeDevice client: %v", err)
	}

	// Real gateway.
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Real node with the seeded chain persisted, uplinked to the gateway.
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "trustlog-chain")
	// Write after all AuthorizeDevice calls so the persisted chain is complete.
	if err := os.WriteFile(chainPath, trustlog.MarshalChain(log.Entries()), 0o600); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	n := node.New()
	n.SetIdentity("tl-node", "tl-node")
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	n.SetIdentityKey(kp)
	if err := n.EnableTrustLog(genesisHash, chainPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	// Real client pinned to the same genesis. clientKP is authorized in the
	// genesis chain so the locked node accepts its handshake.
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClientLocked(ctx, dial, genesisHash, clientKP, "")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer c.Close()

	// The node offered its chain; the gateway holds it; the client pulled it.
	waitFor(t, "device authorized at client", func() bool { return c.DeviceAuthorized(device) })

	// Revoke the device on the node's store and watch it propagate.
	if err := log.RevokeDevice(device, signer); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	revoked := trustlog.MarshalChain(log.Entries())
	if _, err := n.TrustStore().Ingest(revoked); err != nil {
		t.Fatalf("node ingest revoke: %v", err)
	}

	waitFor(t, "device revoked at client", func() bool { return !c.DeviceAuthorized(device) })
}
