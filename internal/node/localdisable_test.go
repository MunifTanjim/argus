package node

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalDisableOverridesQuarantine(t *testing.T) {
	chain, _, _, _ := seedChain(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	d.SetTrustChainPath(path)

	d.pullTrustOnce(&fakePeer{pullChain: chain})
	if !d.Quarantined() {
		t.Fatal("should be quarantined after seeing an unrecognized chain")
	}
	if !d.rejectsChannels() {
		t.Fatal("quarantined node must reject channels before local-disable")
	}

	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}
	if d.rejectsChannels() {
		t.Fatal("local-disable must override quarantine: rejectsChannels must be false")
	}
}

func TestLoadLocalDisabledPicksUpPreExistingMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	markerPath := filepath.Join(dir, "trustlog-local-disabled")
	if err := os.WriteFile(markerPath, []byte("1"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	d := New()
	d.SetTrustChainPath(path)
	d.LoadLocalDisabled()

	if !d.localDisabled() {
		t.Fatal("LoadLocalDisabled must set the flag when the marker exists")
	}
}

func TestLoadLocalDisabledNoMarkerIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	d.SetTrustChainPath(path)
	d.LoadLocalDisabled()

	if d.localDisabled() {
		t.Fatal("LoadLocalDisabled without a marker must leave the flag false")
	}
}

func TestAdoptPinClearsQuarantine(t *testing.T) {
	// seedChain(false): genesis-only chain; head == genesis hash (single entry → Tip() == genesis)
	chain, genesis, _, _ := seedChain(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	d.SetTrustChainPath(path)

	d.pullTrustOnce(&fakePeer{pullChain: chain})
	if !d.Quarantined() {
		t.Fatal("unpinned node must be quarantined after seeing a served chain")
	}

	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	if d.Quarantined() {
		t.Fatal("AdoptPin must clear the quarantine gate")
	}
	if d.TrustStore() == nil {
		t.Fatal("AdoptPin must enable the trust store")
	}
}

func TestDropPinClearsPersistedPin(t *testing.T) {
	chain, head, _, _ := seedChain(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	d.syncTrustOnce(&fakePeer{pullChain: chain})
	if b, err := os.ReadFile(path); err != nil || len(b) == 0 {
		t.Fatal("chain must be persisted after sync")
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}
	if d.TrustStore() != nil {
		t.Fatal("DropPin must clear the trust store")
	}
	if !d.Quarantined() {
		t.Fatal("DropPin on a node that held a chain must quarantine it")
	}
	pinPath := genesisHashPath(path)
	if _, err := os.Stat(pinPath); !os.IsNotExist(err) {
		t.Fatal("DropPin must remove the persisted genesis pin file")
	}
}
