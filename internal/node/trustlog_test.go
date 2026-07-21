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
	head = log.Head()
	device = bytes.Repeat([]byte{0x11}, 32)
	if withDevice {
		if err := log.AuthorizeDevice(device, signer); err != nil {
			t.Fatalf("AuthorizeDevice: %v", err)
		}
	}
	return trustlog.MarshalChain(log.Entries()), head, device, signer
}

func TestEnableTrustLogLoadsFromDisk(t *testing.T) {
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
	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("device from disk chain should be authorized")
	}
}

// A fakePeer records offered chains and serves a canned pull, standing in for the
// gateway uplink so runTrustSync can be exercised without a network.
type fakePeer struct {
	pullChain []byte
	offered   [][]byte
}

func (f *fakePeer) Call(method string, params, out any) error {
	switch method {
	case api.MethodTrustLogOffer:
		f.offered = append(f.offered, params.(api.TrustLogChain).Chain)
	case api.MethodTrustLogPull:
		*(out.(*api.TrustLogChain)) = api.TrustLogChain{Chain: f.pullChain}
	}
	return nil
}

func TestSyncOnceOffersAndIngests(t *testing.T) {
	// Node starts with a genesis-only chain; gateway offers a longer one.
	shortChain, head, device, signer := seedChain(t, false)
	log, err := trustlog.Load(mustUnmarshalNode(t, shortChain))
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

	fp := &fakePeer{pullChain: longChain}
	d.syncTrustOnce(fp) // offer our short chain, pull+ingest the long one

	if len(fp.offered) != 1 || !bytes.Equal(fp.offered[0], shortChain) {
		t.Fatalf("expected our short chain offered, got %d offers", len(fp.offered))
	}
	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("device from pulled chain should be authorized")
	}
	// Persisted to disk on advance.
	onDisk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(onDisk, longChain) {
		t.Fatalf("chain not persisted after ingest advance")
	}
}

func TestSyncRejectsRollback(t *testing.T) {
	// Disk has the long chain; a malicious gateway offers the short (stale) one.
	shortChain, head, device, signer := seedChain(t, false)
	log, err := trustlog.Load(mustUnmarshalNode(t, shortChain))
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

	fp := &fakePeer{pullChain: shortChain}
	d.syncTrustOnce(fp)

	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("rollback must be rejected; device should stay authorized")
	}
}

func mustUnmarshalNode(t *testing.T, b []byte) []trustlog.Entry {
	t.Helper()
	e, err := trustlog.UnmarshalChain(b)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	return e
}

// Compile-time check: fakePeer satisfies trustCaller.
var _ trustCaller = (*fakePeer)(nil)

// Compile-time check: runTrustSync exists and takes *api.Peer (used with context).
var _ = (*Node).runTrustSync

func TestLoadPinnedGenesisRoundTrip(t *testing.T) {
	dir := t.TempDir()
	d := New()
	d.trustPath = filepath.Join(dir, "trustlog-chain")
	head := bytes.Repeat([]byte{0x7E}, 32)
	if err := d.writeGenesisHead(head); err != nil {
		t.Fatalf("writeGenesisHead: %v", err)
	}
	got, err := LoadPinnedGenesis(filepath.Join(dir, "trustlog-genesis"))
	if err != nil {
		t.Fatalf("LoadPinnedGenesis: %v", err)
	}
	if !bytes.Equal(got, head) {
		t.Fatalf("LoadPinnedGenesis = %x, want %x", got, head)
	}
	// Absent file: open mode is legitimate — must return (nil, nil).
	absent, aerr := LoadPinnedGenesis(filepath.Join(dir, "absent"))
	if aerr != nil {
		t.Fatalf("absent genesis should return nil error, got: %v", aerr)
	}
	if absent != nil {
		t.Fatal("absent genesis file should return nil head")
	}
	// Present but wrong-length: corrupt — must return an error (fail-closed).
	corruptPath := filepath.Join(dir, "trustlog-genesis-corrupt")
	if err := os.WriteFile(corruptPath, []byte("tooshort"), 0o600); err != nil {
		t.Fatalf("write corrupt genesis: %v", err)
	}
	_, cerr := LoadPinnedGenesis(corruptPath)
	if cerr == nil {
		t.Fatal("corrupt genesis file (wrong length) should return an error")
	}
}

func TestRunTrustSyncPollsLiveEnable(t *testing.T) {
	trustSyncInterval.Store(int64(10 * time.Millisecond))
	t.Cleanup(func() { trustSyncInterval.Store(int64(30 * time.Second)) })

	// Build a chain + a fake peer serving it.
	chain, head, device, _ := seedChain(t, true) // genesis+authorize (existing helper)
	dir := t.TempDir()
	d := New()
	d.trustPath = filepath.Join(dir, "trustlog-chain")

	fp := &fakePeer{pullChain: chain}
	// runTrustSync must NOT early-return when trust is nil; start it, then enable.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.runTrustSyncLoop(ctx, fp) // test-only loop over trustCaller (see note)

	// Enable after the loop is already running.
	ss := trustlog.NewSyncStore(head)
	if err := d.activateTrust(ss, head, d.trustPath); err != nil {
		t.Fatalf("activateTrust: %v", err)
	}
	waitFor(t, "device authorized after live enable", func() bool {
		return d.TrustStore() != nil && d.TrustStore().DeviceAuthorized(device)
	})
}

func TestEnableTrustLogIgnoresCorruptDisk(t *testing.T) {
	_, head, _, _ := seedChain(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, []byte("garbage not a chain"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog returned error on corrupt disk chain: %v", err)
	}
	if d.TrustStore() == nil {
		t.Fatal("TrustStore should be non-nil after EnableTrustLog")
	}
	if d.TrustStore().Head() != nil {
		t.Fatal("Head should be nil when bad chain was ignored")
	}
	device := bytes.Repeat([]byte{0x22}, 32)
	if d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("DeviceAuthorized should be false when no chain was loaded")
	}
}
