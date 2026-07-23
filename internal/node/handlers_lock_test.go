package node

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func newLockTestNode(t *testing.T) *Node {
	t.Helper()
	d := New()
	d.trustPath = filepath.Join(t.TempDir(), "trustlog-chain")
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	d.SetSignerKey(signer)
	return d
}

func callLockInit(t *testing.T, d *Node, p api.LockInitParams) (api.LockInitResult, error) {
	t.Helper()
	raw, _ := json.Marshal(p)
	res, err := d.handleLockInit(context.Background(), raw)
	if err != nil {
		return api.LockInitResult{}, err
	}
	return res.(api.LockInitResult), nil
}

func TestLockInitBuildsGenesisAndAuthorizes(t *testing.T) {
	d := newLockTestNode(t)
	other, _ := trustlog.GenerateSigner()
	devA := bytes.Repeat([]byte{0xA1}, 32)
	res, err := callLockInit(t, d, api.LockInitParams{
		Signers: [][]byte{other.Public},
		Devices: [][]byte{devA},
	})
	if err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	if res.SignerCount != 2 {
		t.Fatalf("SignerCount = %d, want 2 (self + other)", res.SignerCount)
	}
	ts := d.TrustStore()
	// res.Tip is the genesis head (for clients to pin); ts.Tip() is the current head
	// (after device entries), which differs once any device is authorized.
	if ts == nil || len(res.Tip) == 0 {
		t.Fatal("trust store not activated or no head returned")
	}
	if !ts.DeviceAuthorized(devA) {
		t.Fatal("device A should be authorized")
	}
	if !ts.SignerTrusted(other.Public) {
		t.Fatal("the additional signer should be trusted")
	}
}

func TestLockInitSingleSignerSucceeds(t *testing.T) {
	d := newLockTestNode(t)
	res, err := callLockInit(t, d, api.LockInitParams{}) // no additional signer, no devices
	if err != nil {
		t.Fatalf("lock.init single: %v", err)
	}
	if res.SignerCount != 1 {
		t.Fatalf("SignerCount = %d, want 1", res.SignerCount)
	}
}

func TestLockInitRefusesReinit(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callLockInit(t, d, api.LockInitParams{}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if _, err := callLockInit(t, d, api.LockInitParams{}); err == nil {
		t.Fatal("second init should be refused (already locked)")
	}
}

// TestLockInitClientSyncRoundTrip verifies that the head returned by lock.init is
// the genesis head, so a fresh client-pinned SyncStore can Ingest the chain.
// (Fails before Fix A: store.Tip() is the current head after device entries, not
// the genesis head, so NewSyncStore(res.Tip).Ingest rejects the chain.)
func TestLockInitClientSyncRoundTrip(t *testing.T) {
	d := newLockTestNode(t)
	devA := bytes.Repeat([]byte{0xA1}, 32)
	res, err := callLockInit(t, d, api.LockInitParams{
		Devices: [][]byte{devA},
	})
	if err != nil {
		t.Fatalf("lock.init: %v", err)
	}

	// Simulate a client that pinned the head from the lock.init result.
	clientStore := trustlog.NewSyncStore(res.Tip)
	if _, err := clientStore.Ingest(d.TrustStore().Bytes()); err != nil {
		t.Fatalf("client Ingest failed (genesis head mismatch?): %v", err)
	}
	if !clientStore.DeviceAuthorized(devA) {
		t.Fatal("client store should see devA authorized after ingest")
	}
}

// TestLockInitRebootRoundTrip verifies that the persisted genesis head allows a
// rebooted node to re-enable locked mode via EnableTrustLog.
// (Fails before Fix B: the persisted head is store.Tip() which is the current
// head, so EnableTrustLog's Ingest rejects the chain, leaving the store empty.)
func TestLockInitRebootRoundTrip(t *testing.T) {
	d := newLockTestNode(t)
	devA := bytes.Repeat([]byte{0xA1}, 32)
	if _, err := callLockInit(t, d, api.LockInitParams{
		Devices: [][]byte{devA},
	}); err != nil {
		t.Fatalf("lock.init: %v", err)
	}

	// Read the persisted genesis head from disk (sibling to the chain file).
	genesisFile := filepath.Join(filepath.Dir(d.trustPath), "trustlog-genesis")
	persistedHead, err := os.ReadFile(genesisFile)
	if err != nil {
		t.Fatalf("reading persisted genesis head: %v", err)
	}

	// Simulate reboot: fresh node, same trust path, pinned to the persisted genesis.
	d2 := New()
	if err := d2.EnableTrustLog(persistedHead, d.trustPath); err != nil {
		t.Fatalf("EnableTrustLog on reboot: %v", err)
	}
	if !d2.TrustStore().DeviceAuthorized(devA) {
		t.Fatal("rebooted node should see devA authorized from persisted chain")
	}
}

func mustSigner(t *testing.T) trustlog.SignerKey {
	t.Helper()
	sk, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	return sk
}

func TestLockInitGeneratesDisablements(t *testing.T) {
	d := newLockTestNode(t)
	res, err := callLockInit(t, d, api.LockInitParams{GenDisablements: 2})
	if err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	if len(res.DisablementSecrets) != 2 {
		t.Fatalf("got %d secrets, want 2", len(res.DisablementSecrets))
	}
	// A returned secret validly disables the resulting store (commitment is in genesis).
	if changed, err := d.TrustStore().Disable(res.DisablementSecrets[0], mustSigner(t)); err != nil || !changed {
		t.Fatalf("returned secret should disable: changed=%v err=%v", changed, err)
	}
	if !d.TrustStore().Disabled() {
		t.Fatal("store should be disabled after a valid returned secret")
	}
}

func TestLockInitNegativeGenDisablementsErrors(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callLockInit(t, d, api.LockInitParams{GenDisablements: -1}); err == nil {
		t.Fatal("negative gen_disablements must error")
	}
}

func mustGenSigner(t *testing.T) trustlog.SignerKey {
	t.Helper()
	sk, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	return sk
}

func newLockedSignerNode(t *testing.T) *Node {
	t.Helper()
	d := newLockTestNode(t)
	if _, err := callLockInit(t, d, api.LockInitParams{}); err != nil {
		t.Fatalf("lock.init: %v", err)
	}
	return d
}

func callLockSigner(t *testing.T, d *Node, method string, pub []byte) (api.LockDeviceResult, error) {
	t.Helper()
	raw, _ := json.Marshal(api.LockSignerParams{Signer: pub})
	var h func(context.Context, json.RawMessage) (any, error)
	switch method {
	case api.MethodLockAddSigner:
		h = d.handleLockAddSigner
	case api.MethodLockRemoveSigner:
		h = d.handleLockRemoveSigner
	default:
		t.Fatalf("bad method %q", method)
	}
	res, err := h(context.Background(), raw)
	if err != nil {
		return api.LockDeviceResult{}, err
	}
	return res.(api.LockDeviceResult), nil
}

func TestHandleLockAddRemoveSigner(t *testing.T) {
	d := newLockedSignerNode(t)
	newSigner := mustGenSigner(t)
	// add
	if _, err := callLockSigner(t, d, api.MethodLockAddSigner, newSigner.Public); err != nil {
		t.Fatalf("addSigner: %v", err)
	}
	if !d.TrustStore().SignerTrusted(newSigner.Public) {
		t.Fatal("signer must be trusted after addSigner")
	}
	// remove
	if _, err := callLockSigner(t, d, api.MethodLockRemoveSigner, newSigner.Public); err != nil {
		t.Fatalf("removeSigner: %v", err)
	}
	if d.TrustStore().SignerTrusted(newSigner.Public) {
		t.Fatal("signer must not be trusted after removeSigner")
	}
}

func callLockDevice(t *testing.T, d *Node, method string, dev []byte) (api.LockDeviceResult, error) {
	t.Helper()
	raw, _ := json.Marshal(api.LockDeviceParams{Device: dev})
	var h func(context.Context, json.RawMessage) (any, error)
	switch method {
	case api.MethodLockSign:
		h = d.handleLockSign
	case api.MethodLockRevoke:
		h = d.handleLockRevoke
	default:
		t.Fatalf("bad method %q", method)
	}
	res, err := h(context.Background(), raw)
	if err != nil {
		return api.LockDeviceResult{}, err
	}
	return res.(api.LockDeviceResult), nil
}

func TestLockSignAndRevoke(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callLockInit(t, d, api.LockInitParams{}); err != nil { // single-signer (self)
		t.Fatalf("init: %v", err)
	}
	dev := bytes.Repeat([]byte{0xC3}, 32)

	if _, err := callLockDevice(t, d, api.MethodLockSign, dev); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !d.TrustStore().DeviceAuthorized(dev) {
		t.Fatal("device should be authorized after sign")
	}
	if _, err := callLockDevice(t, d, api.MethodLockRevoke, dev); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if d.TrustStore().DeviceAuthorized(dev) {
		t.Fatal("device should be revoked")
	}
}

func TestLockSignIdempotent(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callLockInit(t, d, api.LockInitParams{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	dev := bytes.Repeat([]byte{0xC3}, 32)
	if _, err := callLockDevice(t, d, api.MethodLockSign, dev); err != nil {
		t.Fatalf("sign: %v", err)
	}
	head1 := d.TrustStore().Tip()
	// Re-signing an already-authorized device is a no-op: tip must not advance.
	if _, err := callLockDevice(t, d, api.MethodLockSign, dev); err != nil {
		t.Fatalf("re-sign: %v", err)
	}
	if !bytes.Equal(d.TrustStore().Tip(), head1) {
		t.Fatal("re-signing an authorized device must not change tip")
	}
}

func TestLockSignRequiresLocked(t *testing.T) {
	// Not locked → error.
	d := newLockTestNode(t)
	dev := bytes.Repeat([]byte{0xC3}, 32)
	if _, err := callLockDevice(t, d, api.MethodLockSign, dev); err == nil {
		t.Fatal("sign on an unlocked node should error")
	}
	// Wrong-length device → error (after init).
	if _, err := callLockInit(t, d, api.LockInitParams{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := callLockDevice(t, d, api.MethodLockSign, []byte{1, 2, 3}); err == nil {
		t.Fatal("wrong-length device should error")
	}
}

func TestLockDisable(t *testing.T) {
	d := newLockTestNode(t)
	initRes, err := callLockInit(t, d, api.LockInitParams{GenDisablements: 1})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	// Valid secret → disabled.
	raw, _ := json.Marshal(api.LockDisableParams{Secret: initRes.DisablementSecrets[0]})
	out, err := d.handleLockDisable(context.Background(), raw)
	if err != nil {
		t.Fatalf("lock.disable: %v", err)
	}
	res := out.(api.LockDisableResult)
	if !res.Disabled || !d.TrustStore().Disabled() {
		t.Fatal("store should be disabled")
	}
	if len(res.Tip) == 0 {
		t.Fatal("disable result Head must be non-empty")
	}
	// Second disable with the same secret must error: the log is terminal-disabled.
	if _, err2 := d.handleLockDisable(context.Background(), raw); err2 == nil {
		t.Fatal("second disable on an already-disabled log must error")
	}
}

func TestLockDisableRejectsUnknownSecret(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callLockInit(t, d, api.LockInitParams{GenDisablements: 1}); err != nil {
		t.Fatalf("init: %v", err)
	}
	bad, _ := trustlog.GenerateDisablementSecret()
	raw, _ := json.Marshal(api.LockDisableParams{Secret: bad})
	if _, err := d.handleLockDisable(context.Background(), raw); err == nil {
		t.Fatal("unknown secret must be rejected")
	}
	if d.TrustStore().Disabled() {
		t.Fatal("store must not be disabled by an unknown secret")
	}
}

func TestLockDisableRequiresLocked(t *testing.T) {
	d := newLockTestNode(t)
	raw, _ := json.Marshal(api.LockDisableParams{Secret: []byte("x")})
	if _, err := d.handleLockDisable(context.Background(), raw); err == nil {
		t.Fatal("disable on an unlocked node must error")
	}
}

func TestLocalDisablePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "trustlog-chain")

	d := New()
	d.SetTrustChainPath(chainPath)
	if d.localDisabled() {
		t.Fatal("fresh node must not be local-disabled")
	}
	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}
	if !d.localDisabled() {
		t.Fatal("node should be local-disabled")
	}
	// A fresh node with the same state dir picks it up at boot.
	d2 := New()
	d2.SetTrustChainPath(chainPath)
	d2.LoadLocalDisabled()
	if !d2.localDisabled() {
		t.Fatal("local-disable marker should survive reboot")
	}
}

// newRevokeSignerNode creates a Node with the given SignerKey whose trust store is
// pre-loaded with the provided chain bytes (pinned to genesisHash). It sets up a
// trustPath in a temp dir so persist calls succeed.
func newRevokeSignerNode(t *testing.T, sk trustlog.SignerKey, genesisHash, chain []byte) *Node {
	t.Helper()
	d := New()
	d.SetSignerKey(sk)
	d.trustPath = filepath.Join(t.TempDir(), "trustlog-chain")
	st := trustlog.NewSyncStore(genesisHash)
	if _, err := st.Ingest(chain); err != nil {
		t.Fatalf("newRevokeSignerNode Ingest: %v", err)
	}
	d.trust.Store(st)
	return d
}

// callRevokeSignerStart calls lock.revokeSignerStart on d with the given params.
func callRevokeSignerStart(t *testing.T, d *Node, p api.LockRevokeSignerStartParams) (api.LockRevokeSignerBlobResult, error) {
	t.Helper()
	raw, _ := json.Marshal(p)
	res, err := d.handleLockRevokeSignerStart(context.Background(), raw)
	if err != nil {
		return api.LockRevokeSignerBlobResult{}, err
	}
	return res.(api.LockRevokeSignerBlobResult), nil
}

// callRevokeSignerCosign calls lock.revokeSignerCosign on d with the given blob.
func callRevokeSignerCosign(t *testing.T, d *Node, blob []byte) (api.LockRevokeSignerBlobResult, error) {
	t.Helper()
	raw, _ := json.Marshal(api.LockRevokeSignerCosignParams{Blob: blob})
	res, err := d.handleLockRevokeSignerCosign(context.Background(), raw)
	if err != nil {
		return api.LockRevokeSignerBlobResult{}, err
	}
	return res.(api.LockRevokeSignerBlobResult), nil
}

// callRevokeSignerFinish calls lock.revokeSignerFinish on d with the given blob.
func callRevokeSignerFinish(t *testing.T, d *Node, blob []byte) (api.LockRevokeSignerFinishResult, error) {
	t.Helper()
	raw, _ := json.Marshal(api.LockRevokeSignerFinishParams{Blob: blob})
	res, err := d.handleLockRevokeSignerFinish(context.Background(), raw)
	if err != nil {
		return api.LockRevokeSignerFinishResult{}, err
	}
	return res.(api.LockRevokeSignerFinishResult), nil
}

// TestHandleRevokeSignerCeremony exercises the full Start→Cosign→Finish ceremony on a
// locked signer node that holds a 3-signer trust log {a,b,c}. Two node instances
// (dA signer=a, dB signer=b) share the same genesis; c is revoked. The test asserts
// that after Finish the store reflects the revocation and c's authorized device is
// erased (the fork point defaults to before c's earliest action).
func TestHandleRevokeSignerCeremony(t *testing.T) {
	skA, skB, skC := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)

	// Build genesis {a,b,c} with a as the genesis signer.
	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public, skC.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip() // hash of the genesis entry (chain pin)

	// c (compromised) authorizes a device; this action should be erased by the fork.
	cDevice := bytes.Repeat([]byte{0xCC}, 32)
	if err := tlog.AuthorizeDevice(cDevice, skC); err != nil {
		t.Fatalf("AuthorizeDevice by c: %v", err)
	}
	chain := trustlog.MarshalChain(tlog.Entries())

	// Set up two nodes sharing the same genesis-pinned trust store.
	dA := newRevokeSignerNode(t, skA, genesisHash, chain)
	dB := newRevokeSignerNode(t, skB, genesisHash, chain)

	// Verify both nodes see the same initial state.
	if !dA.TrustStore().SignerTrusted(skC.Public) {
		t.Fatal("setup: c must initially be trusted on node A")
	}
	if !dA.TrustStore().DeviceAuthorized(cDevice) {
		t.Fatal("setup: c's device must be authorized before revocation")
	}

	// --- Step 1: revokeSignerStart on A (revoke c; default fork point = before c's action) ---
	startRes, err := callRevokeSignerStart(t, dA, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{skC.Public},
	})
	if err != nil {
		t.Fatalf("revokeSignerStart: %v", err)
	}
	blob1 := startRes.Blob
	if len(blob1) == 0 {
		t.Fatal("start: blob must not be empty")
	}

	// 1 co-sign (a) for 1 revoked (c) → not yet complete (need >1).
	// Verify blob is a valid PendingRevoke.
	pr, err := trustlog.UnmarshalPendingRevoke(blob1)
	if err != nil {
		t.Fatalf("start: blob is not a valid PendingRevoke: %v", err)
	}
	if trustlog.Complete(pr, tlog) {
		t.Fatal("should not be complete after only 1 co-sign")
	}

	// --- Step 2: revokeSignerCosign on B (adds b's co-sign) ---
	cosignRes, err := callRevokeSignerCosign(t, dB, blob1)
	if err != nil {
		t.Fatalf("revokeSignerCosign: %v", err)
	}
	blob2 := cosignRes.Blob
	if len(blob2) == 0 {
		t.Fatal("cosign: blob must not be empty")
	}

	// 2 co-signs (a,b) for 1 revoked (c) → complete.
	pr2, err := trustlog.UnmarshalPendingRevoke(blob2)
	if err != nil {
		t.Fatalf("cosign: blob2 is not a valid PendingRevoke: %v", err)
	}
	if !trustlog.Complete(pr2, tlog) {
		t.Fatal("should be complete with 2 co-signs for 1 revoked signer")
	}

	// --- Step 3: revokeSignerFinish on A (ingest + persist) ---
	finishRes, err := callRevokeSignerFinish(t, dA, blob2)
	if err != nil {
		t.Fatalf("revokeSignerFinish: %v", err)
	}
	if len(finishRes.Tip) == 0 {
		t.Fatal("finish: tip must not be empty")
	}

	// Assert: c is no longer trusted.
	if dA.TrustStore().SignerTrusted(skC.Public) {
		t.Error("c must be revoked after ceremony")
	}
	// Assert: a and b remain trusted.
	if !dA.TrustStore().SignerTrusted(skA.Public) {
		t.Error("a must remain trusted")
	}
	if !dA.TrustStore().SignerTrusted(skB.Public) {
		t.Error("b must remain trusted")
	}
	// Assert: c's device is no longer authorized (fork erased c's AuthorizeDevice).
	if dA.TrustStore().DeviceAuthorized(cDevice) {
		t.Error("c's device must be revoked after fork erased c's action")
	}
}

// TestHandleRevokeSignerFinishRejectsIncomplete verifies that Finish fails if the blob
// does not yet have a co-sign quorum.
func TestHandleRevokeSignerFinishRejectsIncomplete(t *testing.T) {
	skA, skB, skC := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public, skC.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()
	chain := trustlog.MarshalChain(tlog.Entries())
	dA := newRevokeSignerNode(t, skA, genesisHash, chain)

	// Start with 1 co-sign (not enough to out-vote 1 revoked signer).
	startRes, err := callRevokeSignerStart(t, dA, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{skC.Public},
	})
	if err != nil {
		t.Fatalf("revokeSignerStart: %v", err)
	}

	// Finish must reject because quorum is not met.
	if _, err := callRevokeSignerFinish(t, dA, startRes.Blob); err == nil {
		t.Fatal("finish with incomplete blob must return an error")
	}
}

// TestHandleRevokeSignerStartRequiresLocked verifies that the Start handler errors when
// locked mode is not enabled.
func TestHandleRevokeSignerStartRequiresLocked(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callRevokeSignerStart(t, d, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{bytes.Repeat([]byte{0x01}, 32)},
	}); err == nil {
		t.Fatal("revokeSignerStart on unlocked node must error")
	}
}

// callLockLog calls handleLockLog on d and returns the result.
func callLockLog(t *testing.T, d *Node) (api.LockLogResult, error) {
	t.Helper()
	res, err := d.handleLockLog(context.Background(), nil)
	if err != nil {
		return api.LockLogResult{}, err
	}
	return res.(api.LockLogResult), nil
}

func TestHandleLockLogReturnsHistory(t *testing.T) {
	skA, skB := mustGenSigner(t), mustGenSigner(t)

	// Build genesis {a,b}, authorize a device, then call lock.log.
	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()
	dev := bytes.Repeat([]byte{0xD1}, 32)
	if err := tlog.AuthorizeDevice(dev, skA); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	chain := trustlog.MarshalChain(tlog.Entries())
	d := newRevokeSignerNode(t, skA, genesisHash, chain)

	res, err := callLockLog(t, d)
	if err != nil {
		t.Fatalf("lock.log: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(res.Entries))
	}
	// Entry 0: genesis
	if res.Entries[0].Kind != "genesis" {
		t.Errorf("entry[0].Kind = %q, want %q", res.Entries[0].Kind, "genesis")
	}
	if len(res.Entries[0].Signers) != 2 {
		t.Errorf("genesis signers = %d, want 2", len(res.Entries[0].Signers))
	}
	// Entry 1: authorize-device
	if res.Entries[1].Kind != "authorize-device" {
		t.Errorf("entry[1].Kind = %q, want %q", res.Entries[1].Kind, "authorize-device")
	}
	if !bytes.Equal(res.Entries[1].Target, dev) {
		t.Errorf("entry[1].Target = %x, want %x", res.Entries[1].Target, dev)
	}
	// Tip must be non-empty.
	if len(res.Tip) == 0 {
		t.Error("Tip must be non-empty")
	}
	// Signers must reflect the current trusted set.
	if len(res.Signers) != 2 {
		t.Errorf("Signers = %d, want 2", len(res.Signers))
	}
}

func TestHandleLockLogNotEnabled(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callLockLog(t, d); err == nil {
		t.Fatal("lock.log on an unlocked node must error")
	}
}

func TestHandleLockLogIncludesRevokeSignerEntry(t *testing.T) {
	skA, skB, skC := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)

	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public, skC.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()
	chain := trustlog.MarshalChain(tlog.Entries())
	dA := newRevokeSignerNode(t, skA, genesisHash, chain)
	dB := newRevokeSignerNode(t, skB, genesisHash, chain)

	// Run the ceremony: revoke C (both A and B co-sign).
	startRes, err := callRevokeSignerStart(t, dA, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{skC.Public},
	})
	if err != nil {
		t.Fatalf("revokeSignerStart: %v", err)
	}
	cosignRes, err := callRevokeSignerCosign(t, dB, startRes.Blob)
	if err != nil {
		t.Fatalf("revokeSignerCosign: %v", err)
	}
	if _, err := callRevokeSignerFinish(t, dA, cosignRes.Blob); err != nil {
		t.Fatalf("revokeSignerFinish: %v", err)
	}

	// lock.log should include the revoke-signer entry.
	res, err := callLockLog(t, dA)
	if err != nil {
		t.Fatalf("lock.log after ceremony: %v", err)
	}
	var found bool
	for _, e := range res.Entries {
		if e.Kind == "revoke-signer" {
			found = true
			if e.CoSignCount != 2 {
				t.Errorf("revoke-signer CoSignCount = %d, want 2", e.CoSignCount)
			}
			if len(e.Revoked) != 1 {
				t.Errorf("revoke-signer Revoked len = %d, want 1", len(e.Revoked))
			}
		}
	}
	if !found {
		t.Error("lock.log must include a revoke-signer entry after the ceremony")
	}
	// Signers after revocation: {A, B} (C removed).
	if len(res.Signers) != 2 {
		t.Errorf("Signers after revocation = %d, want 2", len(res.Signers))
	}
}

func TestLockStatusReflectsState(t *testing.T) {
	d := newLockTestNode(t)
	// Before init: not enabled, self keys reported, not locally disabled.
	raw0, _ := d.handleLockStatus(context.Background(), nil)
	st0 := raw0.(api.LockStatusResult)
	if st0.Enabled || len(st0.SignerPubKey) == 0 {
		t.Fatalf("pre-init status = %+v", st0)
	}
	if st0.LocalDisabled {
		t.Fatalf("pre-init: LocalDisabled should be false, got %+v", st0)
	}
	initRes, err := callLockInit(t, d, api.LockInitParams{GenDisablements: 1})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	raw1, _ := d.handleLockStatus(context.Background(), nil)
	st1 := raw1.(api.LockStatusResult)
	if !st1.Enabled || len(st1.Tip) == 0 || !st1.SignerTrusted {
		t.Fatalf("post-init status = %+v", st1)
	}
	if st1.Disabled {
		t.Fatalf("post-init: Disabled should be false, got %+v", st1)
	}
	// Disable the log; status should reflect Disabled == true.
	disableRaw, _ := json.Marshal(api.LockDisableParams{Secret: initRes.DisablementSecrets[0]})
	if _, err := d.handleLockDisable(context.Background(), disableRaw); err != nil {
		t.Fatalf("lock.disable: %v", err)
	}
	raw2, _ := d.handleLockStatus(context.Background(), nil)
	st2 := raw2.(api.LockStatusResult)
	if !st2.Disabled {
		t.Fatalf("post-disable: Disabled should be true, got %+v", st2)
	}
	// Local disable; status should reflect LocalDisabled == true.
	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}
	raw3, _ := d.handleLockStatus(context.Background(), nil)
	st3 := raw3.(api.LockStatusResult)
	if !st3.LocalDisabled {
		t.Fatalf("post-local-disable: LocalDisabled should be true, got %+v", st3)
	}
}
