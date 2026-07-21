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
	// res.Head is the genesis head (for clients to pin); ts.Head() is the current head
	// (after device entries), which differs once any device is authorized.
	if ts == nil || len(res.Head) == 0 {
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
// (Fails before Fix A: store.Head() is the current head after device entries, not
// the genesis head, so NewSyncStore(res.Head).Ingest rejects the chain.)
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
	clientStore := trustlog.NewSyncStore(res.Head)
	if _, err := clientStore.Ingest(d.TrustStore().Bytes()); err != nil {
		t.Fatalf("client Ingest failed (genesis head mismatch?): %v", err)
	}
	if !clientStore.DeviceAuthorized(devA) {
		t.Fatal("client store should see devA authorized after ingest")
	}
}

// TestLockInitRebootRoundTrip verifies that the persisted genesis head allows a
// rebooted node to re-enable locked mode via EnableTrustLog.
// (Fails before Fix B: the persisted head is store.Head() which is the current
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
	head1 := d.TrustStore().Head()
	// Re-signing an already-authorized device is a no-op: HEAD must not advance.
	if _, err := callLockDevice(t, d, api.MethodLockSign, dev); err != nil {
		t.Fatalf("re-sign: %v", err)
	}
	if !bytes.Equal(d.TrustStore().Head(), head1) {
		t.Fatal("re-signing an authorized device must not change HEAD")
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

func TestLockStatusReflectsState(t *testing.T) {
	d := newLockTestNode(t)
	// Before init: disabled, but self keys reported.
	raw0, _ := d.handleLockStatus(context.Background(), nil)
	st0 := raw0.(api.LockStatusResult)
	if st0.Enabled || len(st0.SignerPubKey) == 0 {
		t.Fatalf("pre-init status = %+v", st0)
	}
	if _, err := callLockInit(t, d, api.LockInitParams{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	raw1, _ := d.handleLockStatus(context.Background(), nil)
	st1 := raw1.(api.LockStatusResult)
	if !st1.Enabled || len(st1.Head) == 0 || !st1.SignerTrusted {
		t.Fatalf("post-init status = %+v", st1)
	}
}
