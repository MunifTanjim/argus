package e2etest

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/node"
)

// sockDir creates a short-path temporary directory for unix socket files.
// macOS caps sockaddr_un.sun_path at 104 bytes; t.TempDir() embeds the full
// test name, which exceeds that limit for names longer than ~32 characters.
func sockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tq")
	if err != nil {
		t.Fatalf("sockDir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("sockDir cleanup: %v", err)
		}
	})
	return dir
}

// startLockNode builds a node with signer+identity keys and a trust chain path,
// uplinked to the gateway, serving its unix socket at socketPath.
func startLockNode(t *testing.T, ctx context.Context, id, gwURL, socketPath, chainPath string) *node.Node {
	t.Helper()
	n := node.New()
	n.SetIdentity(id, id)
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("identity keypair: %v", err)
	}
	n.SetIdentityKey(kp)
	n.SetE2EE(true)
	signerDir, err := os.MkdirTemp("", "tqs")
	if err != nil {
		t.Fatalf("signer dir: %v", err)
	}
	sk, err := node.LoadOrCreateSigner(filepath.Join(signerDir, "signer-key.json"))
	if err != nil {
		_ = os.RemoveAll(signerDir)
		t.Fatalf("signer keypair: %v", err)
	}
	n.SetSignerKey(sk)
	n.SetTrustChainPath(chainPath)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = n.Run(ctx, socketPath)
	}()
	t.Cleanup(func() {
		<-runDone
		if err := os.RemoveAll(signerDir); err != nil {
			t.Errorf("signer dir cleanup: %v", err)
		}
	})
	go n.ConnectGateway(ctx, wsURL(gwURL, "/node"), "", nil)
	return n
}

// resolveSignersForTest returns the base64-decoded SignerPubKey of each roster
// node whose ID matches one of the given ids.
func resolveSignersForTest(nodes []api.NodeDescriptor, ids ...string) ([][]byte, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out [][]byte
	for _, nd := range nodes {
		if !want[nd.ID] || nd.SignerPubKey == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(nd.SignerPubKey)
		if err != nil {
			return nil, fmt.Errorf("node %s: bad signer key: %w", nd.ID, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// gatherDevicesForTest returns the base64-decoded IdentityPubKey of every node
// that has one.
func gatherDevicesForTest(nodes []api.NodeDescriptor) [][]byte {
	var out [][]byte
	for _, nd := range nodes {
		if nd.IdentityPubKey == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil || len(b) != 32 {
			continue
		}
		out = append(out, b)
	}
	return out
}
