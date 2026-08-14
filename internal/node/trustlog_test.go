package node

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// seedChain builds genesis[+authorize] and returns marshaled bytes + pieces.
func seedChain(t *testing.T, withDevice bool) (chain, head, device []byte, signer trustlog.SignerKey) {
	t.Helper()
	var err error
	signer, err = trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	head = log.Tip()
	device = bytes.Repeat([]byte{0x11}, 32)
	if withDevice {
		if err := log.AuthorizeDevice(device, signer); err != nil {
			t.Fatalf("AuthorizeDevice: %v", err)
		}
	}
	return trustlog.MarshalChain(log.Entries()), head, device, signer
}

// fakePeer serves a canned chain as entries on trustlog.sync, standing in for the
// gateway uplink so the sync path can be exercised without a network.
type fakePeer struct {
	pullChain []byte
}

func (f *fakePeer) Call(method string, params, out any) error {
	switch method {
	case api.MethodTrustLogSync:
		res := out.(*api.TrustLogSyncResult)
		if f.pullChain != nil {
			if raw, err := trustlog.ChainEntries(f.pullChain); err == nil {
				res.Entries = raw
			}
		}
	}
	return nil
}

// Compile-time checks: fakePeer satisfies trustCaller and runTrustSync takes *api.Peer.
var _ trustCaller = (*fakePeer)(nil)
var _ = (*Node).runTrustSync

func TestEnableTrustLogGatesAuthorizedDevices(t *testing.T) {
	chain, head, device, _ := seedChain(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, chain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	if d.TrustStore() == nil {
		t.Fatal("TrustStore should be non-nil after EnableTrustLog")
	}
	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("authorized device should be authorized")
	}
	unauthorized := bytes.Repeat([]byte{0x22}, 32)
	if d.TrustStore().DeviceAuthorized(unauthorized) {
		t.Fatal("unauthorized device must not be authorized")
	}
}

func TestEnableTrustLogRejectsForeignGenesisChain(t *testing.T) {
	// A valid chain authorizing a device, but built under its own genesis.
	chain, chainGenesis, device, _ := seedChain(t, true)

	// Pin a DIFFERENT genesis than the on-disk chain roots to.
	pinnedGenesis := bytes.Repeat([]byte{0x33}, len(chainGenesis))
	if bytes.Equal(pinnedGenesis, chainGenesis) {
		t.Fatal("test setup: pinned genesis must differ from the chain's")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, chain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	d := New()
	// EnableTrustLog must not error: the foreign chain is logged and ignored, the
	// node continues with an empty store rather than adopting untrusted data.
	if err := d.EnableTrustLog(pinnedGenesis, path); err != nil {
		t.Fatalf("EnableTrustLog should not error on a foreign-genesis chain: %v", err)
	}
	if d.TrustStore() == nil {
		t.Fatal("TrustStore should be non-nil (store created) after EnableTrustLog")
	}
	if d.TrustStore().Tip() != nil {
		t.Fatal("foreign chain must be rejected on ingest; store must start empty")
	}
	if d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("a device from a foreign-genesis chain must not be authorized")
	}
}

func TestEnableTrustLogRejectsTamperedChain(t *testing.T) {
	chain, head, device, _ := seedChain(t, true)
	// Corrupt the persisted chain: bitflip a byte so genesis-pinned ingest rejects it.
	tampered := append([]byte(nil), chain...)
	tampered[len(tampered)/2] ^= 0xFF

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog should not error on a tampered chain: %v", err)
	}
	if d.TrustStore() == nil {
		t.Fatal("TrustStore should be non-nil (store created) after EnableTrustLog")
	}
	if d.TrustStore().Tip() != nil {
		t.Fatal("tampered chain must be rejected on ingest; store must start empty")
	}
	if d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("a device from a tampered chain must not be authorized")
	}
}

func TestSyncTrustOnceIngestsAndPersists(t *testing.T) {
	// Node starts with a genesis-only chain; gateway offers a longer one authorizing a device.
	shortChain, head, device, signer := seedChain(t, false)
	log, err := trustlog.Load(mustUnmarshalChain(t, shortChain))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := log.AuthorizeDevice(device, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	longChain := trustlog.MarshalChain(log.Entries())

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, shortChain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	if d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("device should not be authorized before sync")
	}

	d.syncTrustOnce(&fakePeer{pullChain: longChain})

	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("device from pulled chain should be authorized after sync")
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(onDisk, longChain) {
		t.Fatal("advanced chain must be persisted to disk")
	}
}

func TestSyncTrustRejectsRollback(t *testing.T) {
	shortChain, head, device, signer := seedChain(t, false)
	log, err := trustlog.Load(mustUnmarshalChain(t, shortChain))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := log.AuthorizeDevice(device, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	longChain := trustlog.MarshalChain(log.Entries())

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, longChain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}

	// Malicious gateway offers the short (stale) chain.
	d.syncTrustOnce(&fakePeer{pullChain: shortChain})

	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("rollback must be rejected; device should stay authorized")
	}
}

func TestRunTrustSyncLoopConverges(t *testing.T) {
	trustSyncInterval.Store(int64(10 * time.Millisecond))
	t.Cleanup(func() { trustSyncInterval.Store(int64(5 * time.Minute)) })

	chain, head, device, _ := seedChain(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	// Wait the loop out before TempDir cleanup: a persist still in flight when the
	// context ends would otherwise recreate the chain file under RemoveAll.
	t.Cleanup(func() { cancel(); <-loopDone })
	go func() {
		defer close(loopDone)
		d.runTrustSyncLoop(ctx, &fakePeer{pullChain: chain})
	}()

	waitFor(t, func() bool {
		return d.TrustStore() != nil && d.TrustStore().DeviceAuthorized(device)
	})
}

func mustUnmarshalChain(t *testing.T, b []byte) []trustlog.Entry {
	t.Helper()
	e, err := trustlog.UnmarshalChain(b)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	return e
}

// TestUnpinnedNodeQuarantinesOnServedChain verifies that a node with no pin
// (SetTrustChainPath only, d.trust nil) trips Quarantined()+rejectsChannels()
// when the gateway serves a non-empty trust chain.
func TestUnpinnedNodeQuarantinesOnServedChain(t *testing.T) {
	chain, _, _, _ := seedChain(t, true)

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	d.SetTrustChainPath(path)
	// d.trust is nil — unpinned

	d.pullTrustOnce(&fakePeer{pullChain: chain})

	if !d.Quarantined() {
		t.Fatal("unpinned node seeing a served chain must be quarantined")
	}
	if !d.rejectsChannels() {
		t.Fatal("quarantined node must reject channels")
	}
}

// TestPinnedAuthorizedNodeDoesNotQuarantine verifies that a pinned node that
// successfully ingests its chain does not trip the quarantine gate.
func TestPinnedAuthorizedNodeDoesNotQuarantine(t *testing.T) {
	chain, head, _, _ := seedChain(t, true)

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}

	d.pullTrustOnce(&fakePeer{pullChain: chain})

	if d.Quarantined() {
		t.Fatal("pinned node that ingested its chain must not be quarantined")
	}
	if d.rejectsChannels() {
		t.Fatal("authorized pinned node must not reject channels")
	}
}

// TestUnpinnedNodeWithNoServedChainDoesNotQuarantine verifies that pure TOFU
// (no chain served) never trips the gate.
func TestUnpinnedNodeWithNoServedChainDoesNotQuarantine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	d.SetTrustChainPath(path)

	d.pullTrustOnce(&fakePeer{pullChain: nil}) // empty — no chain served

	if d.Quarantined() {
		t.Fatal("no served chain must never quarantine")
	}
	if d.rejectsChannels() {
		t.Fatal("TOFU node must not reject channels")
	}
}
