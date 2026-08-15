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
