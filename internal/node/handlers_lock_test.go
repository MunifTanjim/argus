package node

import (
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
