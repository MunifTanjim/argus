package node

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// newLockNode builds a Node with a temp trust-chain path for handler tests.
func newLockNode(t *testing.T) (*Node, string) {
	t.Helper()
	d := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d.SetTrustChainPath(path)
	return d, path
}

// buildGenesisChain returns chain bytes, genesis hash, and the signer key.
func buildGenesisChain(t *testing.T) ([]byte, []byte, trustlog.SignerKey) {
	t.Helper()
	sk, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{sk.Public}, sk, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesis := log.Tip()
	return trustlog.MarshalChain(log.Entries()), genesis, sk
}

func TestHandleLockPin(t *testing.T) {
	chain, genesis, _ := buildGenesisChain(t)
	d, path := newLockNode(t)
	d.SetTrustChainPath(path)

	// Serve the chain via a fake peer so the retained store has entries.
	d.pullTrustOnce(&fakePeer{pullChain: chain})
	if !d.Quarantined() {
		t.Fatal("expected quarantine before pin")
	}

	raw, _ := json.Marshal(api.LockPinParams{Genesis: genesis})
	if _, err := d.handleLockPin(context.Background(), raw); err != nil {
		t.Fatalf("handleLockPin: %v", err)
	}
	if d.TrustStore() == nil {
		t.Fatal("TrustStore must be non-nil after pin")
	}
	if d.Quarantined() {
		t.Fatal("must not be quarantined after adopting pin")
	}
}

func TestHandleLockUnpin(t *testing.T) {
	chain, genesis, _ := buildGenesisChain(t)
	d, path := newLockNode(t)
	if err := d.EnableTrustLog(genesis, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	d.syncTrustOnce(&fakePeer{pullChain: chain})

	if _, err := d.handleLockUnpin(context.Background(), nil); err != nil {
		t.Fatalf("handleLockUnpin: %v", err)
	}
	if d.TrustStore() != nil {
		t.Fatal("TrustStore must be nil after unpin")
	}
}

func TestHandleLockLocalDisable(t *testing.T) {
	d, path := newLockNode(t)
	_ = path
	if d.localDisabled() {
		t.Fatal("should not be locally disabled initially")
	}
	if _, err := d.handleLockLocalDisable(context.Background(), nil); err != nil {
		t.Fatalf("handleLockLocalDisable: %v", err)
	}
	if !d.localDisabled() {
		t.Fatal("localDisabled must be true after handleLockLocalDisable")
	}
}

func TestHandleLockStatus_NotEnabled(t *testing.T) {
	d, _ := newLockNode(t)

	res, err := d.handleLockStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleLockStatus: %v", err)
	}
	st := res.(api.LockStatusResult)
	if st.Enabled {
		t.Error("Enabled must be false when trust is nil")
	}
	if st.Disabled {
		t.Error("Disabled must be false when not enabled")
	}
	if len(st.SignerPubKey) != 0 {
		t.Errorf("SignerPubKey must be empty when no signer set, got %x", st.SignerPubKey)
	}
}

func TestHandleLockStatus_Enforcing(t *testing.T) {
	chain, genesis, _ := buildGenesisChain(t)
	d, path := newLockNode(t)
	if err := d.EnableTrustLog(genesis, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	d.syncTrustOnce(&fakePeer{pullChain: chain})

	res, err := d.handleLockStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleLockStatus: %v", err)
	}
	st := res.(api.LockStatusResult)
	if !st.Enabled {
		t.Error("Enabled must be true when trust store is active")
	}
	if st.Disabled {
		t.Error("Disabled must be false when chain is not disabled")
	}
	if len(st.SignerPubKey) != 0 {
		t.Errorf("SignerPubKey must be empty when no signer loaded in 6a, got %x", st.SignerPubKey)
	}
}

func TestHandleLockStatus_SignerEmpty(t *testing.T) {
	d, _ := newLockNode(t)
	// No SetSignerKey call — signer stays zero.
	res, err := d.handleLockStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleLockStatus: %v", err)
	}
	st := res.(api.LockStatusResult)
	if len(st.SignerPubKey) != 0 {
		t.Errorf("SignerPubKey must be nil/empty when signer unset; got len=%d", len(st.SignerPubKey))
	}
}

func TestHandleLockLog(t *testing.T) {
	chain, genesis, _ := buildGenesisChain(t)
	d, path := newLockNode(t)
	if err := d.EnableTrustLog(genesis, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	d.syncTrustOnce(&fakePeer{pullChain: chain})

	res, err := d.handleLockLog(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleLockLog: %v", err)
	}
	lr := res.(api.LockLogResult)
	if len(lr.Entries) == 0 {
		t.Fatal("expected at least one log entry (genesis)")
	}
	if lr.Entries[0].Kind != "genesis" {
		t.Errorf("first entry kind = %q, want genesis", lr.Entries[0].Kind)
	}
	if len(lr.Signers) == 0 {
		t.Error("expected non-empty signers in log result")
	}
}

func TestHandleLockLog_NotEnabled(t *testing.T) {
	d, _ := newLockNode(t)
	_, err := d.handleLockLog(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when locked mode not enabled")
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("expected CodeInvalidRequest, got %v", err)
	}
}

// asRPCError extracts an *api.RPCError from err and stores it in target.
func asRPCError(err error, target **api.RPCError) bool {
	if e, ok := err.(*api.RPCError); ok {
		*target = e
		return true
	}
	return false
}

func TestDispatchFuncFiltersLockMethods(t *testing.T) {
	d, _ := newLockNode(t)
	dispatch := d.DispatchFunc()
	ctx := context.Background()

	// lock.pin must be rejected by the co-located gateway dispatch.
	raw, _ := json.Marshal(api.LockPinParams{Genesis: make([]byte, 32)})
	_, err := dispatch(ctx, api.MethodLockPin, raw)
	if err == nil {
		t.Fatal("DispatchFunc must reject lock.pin")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("want method-not-found error, got: %v", err)
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeMethodNotFound {
		t.Errorf("want CodeMethodNotFound, got %v", err)
	}

	// lock.status must also be filtered.
	_, err = dispatch(ctx, api.MethodLockStatus, nil)
	if err == nil {
		t.Fatal("DispatchFunc must reject lock.status")
	}
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeMethodNotFound {
		t.Errorf("want CodeMethodNotFound for lock.status, got %v", err)
	}

	// ping must still dispatch normally.
	_, err = dispatch(ctx, api.MethodPing, nil)
	if err != nil {
		t.Errorf("ping must dispatch normally via DispatchFunc, got %v", err)
	}
}

func TestHandleLockPinAndUnpin(t *testing.T) {
	chain, genesis, _ := buildGenesisChain(t)
	d, path := newLockNode(t)
	d.SetTrustChainPath(path)

	d.pullTrustOnce(&fakePeer{pullChain: chain})

	// Pin.
	raw, _ := json.Marshal(api.LockPinParams{Genesis: genesis})
	if _, err := d.handleLockPin(context.Background(), raw); err != nil {
		t.Fatalf("handleLockPin: %v", err)
	}
	if d.TrustStore() == nil {
		t.Fatal("TrustStore must be set after pin")
	}

	// Unpin.
	if _, err := d.handleLockUnpin(context.Background(), nil); err != nil {
		t.Fatalf("handleLockUnpin: %v", err)
	}
	if d.TrustStore() != nil {
		t.Fatal("TrustStore must be nil after unpin")
	}
}

// TestRemoteDispatchFiltersLockMethods verifies that remoteDispatch — used by
// both DispatchFunc (co-located gateway) and the plaintext uplink — rejects
// lock.* while passing non-lock methods through.
func TestRemoteDispatchFiltersLockMethods(t *testing.T) {
	d, _ := newLockNode(t)
	dispatch := d.remoteDispatch()
	ctx := context.Background()

	raw, _ := json.Marshal(api.LockPinParams{Genesis: make([]byte, 32)})
	_, err := dispatch(ctx, api.MethodLockPin, raw)
	if err == nil {
		t.Fatal("remoteDispatch must reject lock.pin")
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeMethodNotFound {
		t.Errorf("want CodeMethodNotFound for lock.pin, got %v", err)
	}

	_, err = dispatch(ctx, api.MethodLockStatus, nil)
	if err == nil {
		t.Fatal("remoteDispatch must reject lock.status")
	}
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeMethodNotFound {
		t.Errorf("want CodeMethodNotFound for lock.status, got %v", err)
	}

	if _, err := dispatch(ctx, api.MethodPing, nil); err != nil {
		t.Errorf("ping must pass through remoteDispatch, got %v", err)
	}
}

// TestKindString covers all recognized kind codes.
func TestKindString(t *testing.T) {
	cases := []struct {
		k    trustlog.Kind
		want string
	}{
		{trustlog.KindGenesis, "genesis"},
		{trustlog.KindAddSigner, "add-signer"},
		{trustlog.KindRemoveSigner, "remove-signer"},
		{trustlog.KindAuthorizeDevice, "authorize-device"},
		{trustlog.KindRevokeDevice, "revoke-device"},
		{trustlog.KindRevokeSigner, "revoke-signer"},
		{trustlog.KindDisable, "disable"},
	}
	for _, c := range cases {
		if got := kindString(c.k); got != c.want {
			t.Errorf("kindString(%d) = %q, want %q", c.k, got, c.want)
		}
	}
	// Unknown kinds produce a fallback.
	if s := kindString(trustlog.Kind(99)); !strings.HasPrefix(s, "kind(") {
		t.Errorf("unexpected fallback: %q", s)
	}
}

// newSignerLockNode builds a Node with a signer key and a temp trust-chain path.
func newSignerLockNode(t *testing.T) (*Node, trustlog.SignerKey) {
	t.Helper()
	sk, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	d, _ := newLockNode(t)
	d.SetSignerKey(sk)
	return d, sk
}

// initLockNode calls handleLockInit on d with sk as the sole signer and no
// additional devices or disablements. Returns the LockInitResult.
func initLockNode(t *testing.T, d *Node, sk trustlog.SignerKey) api.LockInitResult {
	t.Helper()
	raw, _ := json.Marshal(api.LockInitParams{Signers: [][]byte{sk.Public}})
	res, err := d.handleLockInit(context.Background(), raw)
	if err != nil {
		t.Fatalf("handleLockInit: %v", err)
	}
	return res.(api.LockInitResult)
}

func TestHandleLockInit_CreatesGenesis(t *testing.T) {
	d, sk := newSignerLockNode(t)
	ir := initLockNode(t, d, sk)
	if len(ir.Tip) == 0 {
		t.Fatal("expected non-empty genesis tip")
	}
	if ir.SignerCount != 1 {
		t.Errorf("signer count = %d, want 1", ir.SignerCount)
	}
	st := d.trust.Load()
	if st == nil {
		t.Fatal("trust store must be set after init")
	}
	if !st.SignerTrusted(sk.Public) {
		t.Error("this node's signer must be trusted after init")
	}
}

func TestHandleLockInit_AlreadyEnabled(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)
	raw, _ := json.Marshal(api.LockInitParams{Signers: [][]byte{sk.Public}})
	_, err := d.handleLockInit(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error on double-init")
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest, got %v", err)
	}
}

func TestHandleLockInit_NoSignerKey(t *testing.T) {
	d, _ := newLockNode(t)
	sk, _ := trustlog.GenerateSigner()
	raw, _ := json.Marshal(api.LockInitParams{Signers: [][]byte{sk.Public}})
	_, err := d.handleLockInit(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error when no signer key set")
	}
}

func TestHandleLockInit_OwnKeyMustBeExplicit(t *testing.T) {
	d, _ := newSignerLockNode(t)
	other, _ := trustlog.GenerateSigner()
	raw, _ := json.Marshal(api.LockInitParams{Signers: [][]byte{other.Public}})
	_, err := d.handleLockInit(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error when own key not in signer list")
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest, got %v", err)
	}
}

func TestHandleLockSign_AuthorizesDevice(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)

	dev := make([]byte, 32)
	dev[0] = 0xAB
	raw, _ := json.Marshal(api.LockDeviceParams{Device: dev})
	res, err := d.handleLockSign(context.Background(), raw)
	if err != nil {
		t.Fatalf("handleLockSign: %v", err)
	}
	dr := res.(api.LockDeviceResult)
	if !dr.Changed {
		t.Error("Changed must be true for a new authorization")
	}
	if !d.trust.Load().DeviceAuthorized(dev) {
		t.Error("device must be authorized after handleLockSign")
	}
}

func TestHandleLockRevoke_RevokesDevice(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)

	dev := make([]byte, 32)
	dev[0] = 0xCD

	// Authorize first.
	raw, _ := json.Marshal(api.LockDeviceParams{Device: dev})
	if _, err := d.handleLockSign(context.Background(), raw); err != nil {
		t.Fatalf("handleLockSign: %v", err)
	}

	// Now revoke.
	if _, err := d.handleLockRevoke(context.Background(), raw); err != nil {
		t.Fatalf("handleLockRevoke: %v", err)
	}
	if d.trust.Load().DeviceAuthorized(dev) {
		t.Error("device must not be authorized after handleLockRevoke")
	}
}

func TestHandleLockSign_UntrustedSignerRefused(t *testing.T) {
	// Init with a different signer; this node's key is not in the trust set.
	other, _ := trustlog.GenerateSigner()
	otherLog, _ := trustlog.NewGenesis([][]byte{other.Public}, other, nil)
	genesis := otherLog.Tip()

	d, _ := newLockNode(t)
	myKey, _ := trustlog.GenerateSigner()
	d.SetSignerKey(myKey)

	if err := d.EnableTrustLog(genesis, d.trustPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	d.syncTrustOnce(&fakePeer{pullChain: trustlog.MarshalChain(otherLog.Entries())})

	dev := make([]byte, 32)
	raw, _ := json.Marshal(api.LockDeviceParams{Device: dev})
	_, err := d.handleLockSign(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error when signer not trusted")
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest, got %v", err)
	}
}

func TestHandleLockAddSigner_AddsToSet(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)

	newSigner, _ := trustlog.GenerateSigner()
	raw, _ := json.Marshal(api.LockSignerParams{Signer: newSigner.Public})
	res, err := d.handleLockAddSigner(context.Background(), raw)
	if err != nil {
		t.Fatalf("handleLockAddSigner: %v", err)
	}
	dr := res.(api.LockDeviceResult)
	if !dr.Changed {
		t.Error("Changed must be true when a new signer is added")
	}
	if !d.trust.Load().SignerTrusted(newSigner.Public) {
		t.Error("new signer must be trusted after handleLockAddSigner")
	}
}

func TestHandleLockRemoveSigner_RemovesFromSet(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)

	extra, _ := trustlog.GenerateSigner()
	addRaw, _ := json.Marshal(api.LockSignerParams{Signer: extra.Public})
	if _, err := d.handleLockAddSigner(context.Background(), addRaw); err != nil {
		t.Fatalf("handleLockAddSigner: %v", err)
	}

	rmRaw, _ := json.Marshal(api.LockSignerParams{Signer: extra.Public})
	if _, err := d.handleLockRemoveSigner(context.Background(), rmRaw); err != nil {
		t.Fatalf("handleLockRemoveSigner: %v", err)
	}
	if d.trust.Load().SignerTrusted(extra.Public) {
		t.Error("signer must not be trusted after handleLockRemoveSigner")
	}
}

func TestHandleLockDisable_WithCorrectSecret(t *testing.T) {
	d, sk := newSignerLockNode(t)
	raw, _ := json.Marshal(api.LockInitParams{
		Signers:         [][]byte{sk.Public},
		GenDisablements: 1,
	})
	res, err := d.handleLockInit(context.Background(), raw)
	if err != nil {
		t.Fatalf("handleLockInit: %v", err)
	}
	ir := res.(api.LockInitResult)
	if len(ir.DisablementSecrets) != 1 {
		t.Fatalf("expected 1 disablement secret, got %d", len(ir.DisablementSecrets))
	}

	disableRaw, _ := json.Marshal(api.LockDisableParams{Secret: ir.DisablementSecrets[0]})
	dres, err := d.handleLockDisable(context.Background(), disableRaw)
	if err != nil {
		t.Fatalf("handleLockDisable: %v", err)
	}
	dr := dres.(api.LockDisableResult)
	if !dr.Disabled {
		t.Error("log must be disabled after handleLockDisable with correct secret")
	}
	if !d.trust.Load().Disabled() {
		t.Error("trust store must report Disabled after handleLockDisable")
	}
}

func TestHandleLockDisable_WrongSecretFails(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)

	wrongSecret := make([]byte, 32)
	raw, _ := json.Marshal(api.LockDisableParams{Secret: wrongSecret})
	_, err := d.handleLockDisable(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error with wrong disablement secret")
	}
}

// TestHandleLockDisable_NoSignerKeyReturnsError guards against the panic path
// where st != nil but d.signer is zero (LoadOrCreateSigner failed at startup).
// ed25519.Sign panics on an empty private key, so the missing-signer guard must
// fire before st.Disable is ever called.
func TestHandleLockDisable_NoSignerKeyReturnsError(t *testing.T) {
	// Build a trust store with a different signer, leaving this node's signer empty.
	other, _ := trustlog.GenerateSigner()
	otherLog, _ := trustlog.NewGenesis([][]byte{other.Public}, other, nil)
	genesis := otherLog.Tip()

	d, _ := newLockNode(t)
	// No SetSignerKey — signer stays zero.

	if err := d.EnableTrustLog(genesis, d.trustPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	d.syncTrustOnce(&fakePeer{pullChain: trustlog.MarshalChain(otherLog.Entries())})

	raw, _ := json.Marshal(api.LockDisableParams{Secret: make([]byte, 32)})
	// Must NOT panic; must return CodeInvalidRequest.
	res, err := d.handleLockDisable(context.Background(), raw)
	if res != nil {
		t.Error("expected nil result on error path")
	}
	if err == nil {
		t.Fatal("expected RPCError when no signer key is set")
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest, got %v", err)
	}
}

// TestSignerPublicNilSafe confirms SignerPublic returns nil (not a panic) when no key is set.
func TestSignerPublicNilSafe(t *testing.T) {
	d := New()
	if pub := d.SignerPublic(); pub != nil {
		t.Errorf("SignerPublic with no key set must return nil, got %x", pub)
	}
	if key := d.SignerPubKey(); key != "" {
		t.Errorf("SignerPubKey with no key set must return empty, got %q", key)
	}
}

// TestSetSignerKey verifies both accessors work after a key is loaded.
func TestSetSignerKey(t *testing.T) {
	sk, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	d := New()
	d.SetSignerKey(sk)
	if pub := d.SignerPublic(); len(pub) == 0 {
		t.Error("SignerPublic must be non-empty after SetSignerKey")
	}
	if key := d.SignerPubKey(); key == "" {
		t.Error("SignerPubKey must be non-empty after SetSignerKey")
	}
	// Cleanup: the node may have created a temp path; no file I/O needed here.
	_ = os.RemoveAll(t.TempDir())
}

func mustGenSigner(t *testing.T) trustlog.SignerKey {
	t.Helper()
	sk, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	return sk
}

// newRevokeSignerNode creates a Node with sk loaded and its trust store pre-seeded
// with the provided genesis-pinned chain. The trustPath is set so persist succeeds.
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

func callRevokeSignerStart(t *testing.T, d *Node, p api.LockRevokeSignerStartParams) (api.LockRevokeSignerBlobResult, error) {
	t.Helper()
	raw, _ := json.Marshal(p)
	res, err := d.handleLockRevokeSignerStart(context.Background(), raw)
	if err != nil {
		return api.LockRevokeSignerBlobResult{}, err
	}
	return res.(api.LockRevokeSignerBlobResult), nil
}

func callRevokeSignerCosign(t *testing.T, d *Node, blob []byte) (api.LockRevokeSignerBlobResult, error) {
	t.Helper()
	raw, _ := json.Marshal(api.LockRevokeSignerCosignParams{Blob: blob})
	res, err := d.handleLockRevokeSignerCosign(context.Background(), raw)
	if err != nil {
		return api.LockRevokeSignerBlobResult{}, err
	}
	return res.(api.LockRevokeSignerBlobResult), nil
}

func callRevokeSignerFinish(t *testing.T, d *Node, blob []byte) (api.LockRevokeSignerFinishResult, error) {
	t.Helper()
	raw, _ := json.Marshal(api.LockRevokeSignerFinishParams{Blob: blob})
	res, err := d.handleLockRevokeSignerFinish(context.Background(), raw)
	if err != nil {
		return api.LockRevokeSignerFinishResult{}, err
	}
	return res.(api.LockRevokeSignerFinishResult), nil
}

// TestHandleRevokeSignerCeremony runs a full Start→Cosign→Finish ceremony on a
// 3-signer trust log {A,B,C}. A starts (revoking C with replacement D), B cosigns,
// A finishes. After finish: C is not trusted, D is trusted, A and B remain trusted.
func TestHandleRevokeSignerCeremony(t *testing.T) {
	skA, skB, skC, skD := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)

	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public, skC.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()

	// C authorizes a device; the fork will erase this action.
	cDevice := bytes.Repeat([]byte{0xCC}, 32)
	if err := tlog.AuthorizeDevice(cDevice, skC); err != nil {
		t.Fatalf("AuthorizeDevice by C: %v", err)
	}
	chain := trustlog.MarshalChain(tlog.Entries())

	dA := newRevokeSignerNode(t, skA, genesisHash, chain)
	dB := newRevokeSignerNode(t, skB, genesisHash, chain)

	if !dA.TrustStore().SignerTrusted(skC.Public) {
		t.Fatal("setup: C must be initially trusted")
	}
	if !dA.TrustStore().DeviceAuthorized(cDevice) {
		t.Fatal("setup: C's device must be initially authorized")
	}

	// Step 1: A starts the ceremony revoking C and adding D as replacement.
	startRes, err := callRevokeSignerStart(t, dA, api.LockRevokeSignerStartParams{
		Revoked:  [][]byte{skC.Public},
		Replaces: [][]byte{skD.Public},
	})
	if err != nil {
		t.Fatalf("revokeSignerStart: %v", err)
	}
	blob1 := startRes.Blob
	if len(blob1) == 0 {
		t.Fatal("start: blob must not be empty")
	}

	// 1 co-sign (A) for 1 revoked (C) is not yet complete.
	pr, err := trustlog.UnmarshalPendingRevoke(blob1)
	if err != nil {
		t.Fatalf("blob1 is not a valid PendingRevoke: %v", err)
	}
	if trustlog.Complete(pr, tlog) {
		t.Fatal("should not be complete after only 1 co-sign")
	}

	// Step 2: B cosigns.
	cosignRes, err := callRevokeSignerCosign(t, dB, blob1)
	if err != nil {
		t.Fatalf("revokeSignerCosign: %v", err)
	}
	blob2 := cosignRes.Blob

	pr2, err := trustlog.UnmarshalPendingRevoke(blob2)
	if err != nil {
		t.Fatalf("blob2 is not a valid PendingRevoke: %v", err)
	}
	if !trustlog.Complete(pr2, tlog) {
		t.Fatal("should be complete with 2 co-signs for 1 revoked signer")
	}

	// Step 3: A finishes — ingest and persist.
	finishRes, err := callRevokeSignerFinish(t, dA, blob2)
	if err != nil {
		t.Fatalf("revokeSignerFinish: %v", err)
	}
	if len(finishRes.Tip) == 0 {
		t.Fatal("finish: tip must not be empty")
	}

	st := dA.TrustStore()
	if st.SignerTrusted(skC.Public) {
		t.Error("C must be revoked after ceremony")
	}
	if !st.SignerTrusted(skD.Public) {
		t.Error("replacement D must be trusted after ceremony")
	}
	if !st.SignerTrusted(skA.Public) {
		t.Error("A must remain trusted")
	}
	if !st.SignerTrusted(skB.Public) {
		t.Error("B must remain trusted")
	}
	if st.DeviceAuthorized(cDevice) {
		t.Error("C's device must be revoked after fork erased C's action")
	}
}

// TestHandleRevokeSignerFinishRejectsIncomplete verifies that Finish fails when the
// co-sign quorum has not been reached.
func TestHandleRevokeSignerFinishRejectsIncomplete(t *testing.T) {
	skA, skB, skC := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public, skC.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()
	chain := trustlog.MarshalChain(tlog.Entries())
	dA := newRevokeSignerNode(t, skA, genesisHash, chain)

	startRes, err := callRevokeSignerStart(t, dA, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{skC.Public},
	})
	if err != nil {
		t.Fatalf("revokeSignerStart: %v", err)
	}

	// Only 1 co-sign for 1 revoked signer — quorum not reached.
	if _, err := callRevokeSignerFinish(t, dA, startRes.Blob); err == nil {
		t.Fatal("finish with incomplete blob must return an error")
	}
}

// TestHandleRevokeSignerUntrustedSignerRefused confirms that a node whose signer key
// is not in the trust log is rejected by all three ceremony handlers. The finish
// sub-case uses a QUORUM-COMPLETE blob to prove the SignerTrusted guard (not the
// quorum guard) is what blocks an untrusted node from finalizing the ceremony.
func TestHandleRevokeSignerUntrustedSignerRefused(t *testing.T) {
	skA, skB, skExtra := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	skOther := mustGenSigner(t) // not in genesis

	// Build 3-signer genesis {A, B, Extra}; skOther is not a member.
	tlog, err := trustlog.NewGenesis([][]byte{skA.Public, skB.Public, skExtra.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()
	chain := trustlog.MarshalChain(tlog.Entries())

	// dBad holds skOther — not trusted in this genesis.
	dBad := newRevokeSignerNode(t, skOther, genesisHash, chain)

	// Start must be refused for dBad.
	_, err = callRevokeSignerStart(t, dBad, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{skExtra.Public},
	})
	if err == nil {
		t.Fatal("start by untrusted signer must be refused")
	}
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest for untrusted start, got %v", err)
	}

	// Build a quorum-complete blob: A starts, B cosigns (2 co-signs > 1 revoked).
	dA := newRevokeSignerNode(t, skA, genesisHash, chain)
	dB := newRevokeSignerNode(t, skB, genesisHash, chain)
	startRes, err := callRevokeSignerStart(t, dA, api.LockRevokeSignerStartParams{
		Revoked: [][]byte{skExtra.Public},
	})
	if err != nil {
		t.Fatalf("start by A: %v", err)
	}
	cosignRes, err := callRevokeSignerCosign(t, dB, startRes.Blob)
	if err != nil {
		t.Fatalf("cosign by B: %v", err)
	}
	completeBlob := cosignRes.Blob

	// Sanity-check: the blob really is quorum-complete.
	pr, err := trustlog.UnmarshalPendingRevoke(completeBlob)
	if err != nil {
		t.Fatalf("UnmarshalPendingRevoke: %v", err)
	}
	if !trustlog.Complete(pr, tlog) {
		t.Fatal("blob must be quorum-complete before testing untrusted finish")
	}

	// Cosign must be refused for dBad (SignerTrusted guard fires before cosign logic).
	_, err = callRevokeSignerCosign(t, dBad, startRes.Blob)
	if err == nil {
		t.Fatal("cosign by untrusted signer must be refused")
	}
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest for untrusted cosign, got %v", err)
	}

	// Finish must be refused for dBad even with a QUORUM-COMPLETE blob —
	// SignerTrusted fires first, before the quorum check.
	_, err = callRevokeSignerFinish(t, dBad, completeBlob)
	if err == nil {
		t.Fatal("finish by untrusted signer must be refused even with a quorum-complete blob")
	}
	if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest for untrusted finish, got %v", err)
	}
}

// TestHandleRevokeSignerNoSignerKey confirms that a node with no private signer key
// returns CodeInvalidRequest for all three ceremony handlers and does NOT panic.
func TestHandleRevokeSignerNoSignerKey(t *testing.T) {
	skA := mustGenSigner(t)
	tlog, err := trustlog.NewGenesis([][]byte{skA.Public}, skA, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := tlog.Tip()
	chain := trustlog.MarshalChain(tlog.Entries())

	// d has no signer key set (signer stays zero-value).
	d := New()
	d.trustPath = filepath.Join(t.TempDir(), "trustlog-chain")
	st := trustlog.NewSyncStore(genesisHash)
	if _, err := st.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	d.trust.Store(st)

	fakeBlob := bytes.Repeat([]byte{0x01}, 32) // not a valid blob, but guard fires first

	checkNoSignerKey := func(name string, fn func() error) {
		t.Helper()
		err := fn()
		if err == nil {
			t.Fatalf("%s: expected error with no signer key", name)
		}
		var rpcErr *api.RPCError
		if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
			t.Errorf("%s: want CodeInvalidRequest, got %v", name, err)
		}
	}

	checkNoSignerKey("start", func() error {
		raw, _ := json.Marshal(api.LockRevokeSignerStartParams{Revoked: [][]byte{skA.Public}})
		_, err := d.handleLockRevokeSignerStart(context.Background(), raw)
		return err
	})
	checkNoSignerKey("cosign", func() error {
		raw, _ := json.Marshal(api.LockRevokeSignerCosignParams{Blob: fakeBlob})
		_, err := d.handleLockRevokeSignerCosign(context.Background(), raw)
		return err
	})
	checkNoSignerKey("finish", func() error {
		raw, _ := json.Marshal(api.LockRevokeSignerFinishParams{Blob: fakeBlob})
		_, err := d.handleLockRevokeSignerFinish(context.Background(), raw)
		return err
	})
}

// TestHandleRevokeSignerRequiresLocked verifies that all three ceremony handlers
// return CodeInvalidRequest when locked mode is not enabled (st == nil).
func TestHandleRevokeSignerRequiresLocked(t *testing.T) {
	d, _ := newSignerLockNode(t) // signer set, no trust store
	fakeBlob := bytes.Repeat([]byte{0x01}, 32)

	cases := []struct {
		name string
		fn   func() error
	}{
		{"start", func() error {
			raw, _ := json.Marshal(api.LockRevokeSignerStartParams{Revoked: [][]byte{fakeBlob}})
			_, err := d.handleLockRevokeSignerStart(context.Background(), raw)
			return err
		}},
		{"cosign", func() error {
			raw, _ := json.Marshal(api.LockRevokeSignerCosignParams{Blob: fakeBlob})
			_, err := d.handleLockRevokeSignerCosign(context.Background(), raw)
			return err
		}},
		{"finish", func() error {
			raw, _ := json.Marshal(api.LockRevokeSignerFinishParams{Blob: fakeBlob})
			_, err := d.handleLockRevokeSignerFinish(context.Background(), raw)
			return err
		}},
	}
	for _, c := range cases {
		err := c.fn()
		if err == nil {
			t.Fatalf("%s: expected error when locked mode not enabled", c.name)
		}
		var rpcErr *api.RPCError
		if !asRPCError(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
			t.Errorf("%s: want CodeInvalidRequest, got %v", c.name, err)
		}
	}
}
