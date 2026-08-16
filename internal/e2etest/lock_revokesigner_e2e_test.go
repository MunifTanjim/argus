package e2etest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// TestLockRevokeSigner is the PR 6c acceptance test: a 3-signer network {A, B, C}
// runs the revoke-signer co-signing ceremony over local sockets. A starts (revoking
// C, with D as replacement), B co-signs (2 of 3 out-vote 1), and A finishes.
// After the ceremony: C is not trusted, D is trusted, A and B remain, and C's
// previously-authorized device is revoked by the fork.
func TestLockRevokeSigner(t *testing.T) {
	skA, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner A: %v", err)
	}
	skB, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner B: %v", err)
	}
	skC, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner C: %v", err)
	}
	skD, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner D: %v", err)
	}

	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public, skC.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()

	// C authorizes a device before the ceremony. The revoke-signer fork point is set
	// before C's first action, so this authorization is erased after the ceremony.
	cDevice := bytes.Repeat([]byte{0xCC}, 32)
	if err := tlog.AuthorizeDevice(cDevice, skC); err != nil {
		t.Fatalf("AuthorizeDevice by C: %v", err)
	}
	chain := trustlog.MarshalChain(tlog.Entries())

	// dir is the outer TempDir whose path is short enough for macOS's 104-byte
	// sockaddr_un limit.
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startNode := func(label, sockName string, sk trustlog.SignerKey) (sockPath string, lc *api.Client) {
		t.Helper()
		chainPath := filepath.Join(t.TempDir(), "chain-"+label)
		if err := os.WriteFile(chainPath, chain, 0o600); err != nil {
			t.Fatalf("write chain %s: %v", label, err)
		}
		n := node.New()
		n.SetIdentity("rs-"+label, "RS "+label)
		n.SetVersion("itest")
		n.SetSignerKey(sk)
		if err := n.EnableTrustLog(genesisHash, chainPath); err != nil {
			t.Fatalf("EnableTrustLog %s: %v", label, err)
		}
		sock := filepath.Join(dir, sockName+".sock")
		sockCtx, sockCancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- n.Run(sockCtx, sock) }()
		t.Cleanup(func() { sockCancel(); <-done })
		waitFor(t, label+" socket ready", func() bool {
			_, err := os.Stat(sock)
			return err == nil
		})
		c, err := api.Dial(sock)
		if err != nil {
			t.Fatalf("dial %s socket: %v", label, err)
		}
		t.Cleanup(func() { c.Close() })
		return sock, c
	}

	_, lcA := startNode("A", "a", skA)
	_, lcB := startNode("B", "b", skB)

	// Verify initial trust state via lock.status on node A.
	var stBefore api.LockStatusResult
	if err := lcA.Call(api.MethodLockStatus, nil, &stBefore); err != nil {
		t.Fatalf("lock.status before ceremony: %v", err)
	}
	if !stBefore.Enabled {
		t.Fatal("lock must be enabled on node A before ceremony")
	}
	if !stBefore.SignerTrusted {
		t.Fatal("signer A must be trusted before ceremony")
	}
	signerSet := func(status api.LockStatusResult) map[string]bool {
		m := make(map[string]bool, len(status.Signers))
		for _, s := range status.Signers {
			m[string(s)] = true
		}
		return m
	}
	before := signerSet(stBefore)
	if !before[string(skC.Public)] {
		t.Fatal("C must be trusted before ceremony")
	}

	// Step 1: Node A starts the ceremony — revoke C, add D as replacement.
	var startRes api.LockRevokeSignerBlobResult
	if err := lcA.Call(api.MethodLockRevokeSignerStart, api.LockRevokeSignerStartParams{
		Revoked:  [][]byte{skC.Public},
		Replaces: [][]byte{skD.Public},
	}, &startRes); err != nil {
		t.Fatalf("revokeSignerStart (A): %v", err)
	}
	if len(startRes.Blob) == 0 {
		t.Fatal("start: blob must not be empty")
	}

	// Assert quorum NOT yet reached — 1 co-sign is not enough to out-vote 1 revoked signer.
	pr1, err := trustlog.UnmarshalPendingRevoke(startRes.Blob)
	if err != nil {
		t.Fatalf("UnmarshalPendingRevoke blob1: %v", err)
	}
	if trustlog.Complete(pr1, tlog) {
		t.Fatal("quorum must not be reached after 1 co-sign (need > 1 to out-vote 1 revoked)")
	}

	// Step 2: Node B co-signs the blob.
	var cosignRes api.LockRevokeSignerBlobResult
	if err := lcB.Call(api.MethodLockRevokeSignerCosign, api.LockRevokeSignerCosignParams{
		Blob: startRes.Blob,
	}, &cosignRes); err != nil {
		t.Fatalf("revokeSignerCosign (B): %v", err)
	}
	if len(cosignRes.Blob) == 0 {
		t.Fatal("cosign: blob must not be empty")
	}

	// Assert quorum IS reached — 2 co-signs out-vote 1 revoked signer.
	pr2, err := trustlog.UnmarshalPendingRevoke(cosignRes.Blob)
	if err != nil {
		t.Fatalf("UnmarshalPendingRevoke blob2: %v", err)
	}
	if !trustlog.Complete(pr2, tlog) {
		t.Fatal("quorum must be reached with 2 co-signs for 1 revoked signer")
	}

	// Step 3: Node A finalizes and ingests the revoke chain.
	var finishRes api.LockRevokeSignerFinishResult
	if err := lcA.Call(api.MethodLockRevokeSignerFinish, api.LockRevokeSignerFinishParams{
		Blob: cosignRes.Blob,
	}, &finishRes); err != nil {
		t.Fatalf("revokeSignerFinish (A): %v", err)
	}
	if len(finishRes.Tip) == 0 {
		t.Fatal("finish: tip must not be empty")
	}

	// Assert post-ceremony trust state.
	var stAfter api.LockStatusResult
	if err := lcA.Call(api.MethodLockStatus, nil, &stAfter); err != nil {
		t.Fatalf("lock.status after ceremony: %v", err)
	}
	after := signerSet(stAfter)
	if after[string(skC.Public)] {
		t.Error("C must not be trusted after revoke-signer ceremony")
	}
	if !after[string(skD.Public)] {
		t.Error("replacement D must be trusted after ceremony")
	}
	if !after[string(skA.Public)] {
		t.Error("A must remain trusted after ceremony")
	}
	if !after[string(skB.Public)] {
		t.Error("B must remain trusted after ceremony")
	}

	// C's device was authorized by C before the fork point; the fork erases that action.
	// Verify via lock.status devices list.
	authorizedDevices := make(map[string]bool, len(stAfter.Devices))
	for _, d := range stAfter.Devices {
		authorizedDevices[string(d)] = true
	}
	if authorizedDevices[string(cDevice)] {
		t.Error("C's device must not be authorized after fork erased C's action")
	}
}
