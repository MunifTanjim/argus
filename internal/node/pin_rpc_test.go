package node

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/trustpin"
)

func TestAdoptPinClearsQuarantineAndEnables(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if !d.Quarantined() {
		t.Fatal("precondition: node should be quarantined")
	}

	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	if d.Quarantined() {
		t.Fatal("AdoptPin must clear the gate")
	}
	if d.TrustStore() == nil {
		t.Fatal("AdoptPin must enable the trust store")
	}
	if !bytes.Equal(d.pinGenesis, genesis) {
		t.Fatalf("pinGenesis = %x, want %x", d.pinGenesis, genesis)
	}
	persisted, err := trustpinFileForTest(d).Load()
	if err != nil {
		t.Fatalf("reload pin: %v", err)
	}
	if !bytes.Equal(persisted, genesis) {
		t.Fatal("AdoptPin must persist the pin so a reboot stays pinned")
	}
}

func TestAdoptPinIsIdempotentAndRefusesADifferentGenesis(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	other := make([]byte, len(genesis))
	copy(other, genesis)
	other[0] ^= 0xFF

	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("re-pinning the same genesis must be a no-op, got: %v", err)
	}
	err := d.AdoptPin(other)
	if err == nil {
		t.Fatal("re-pinning a different genesis must be refused")
	}
	if !strings.Contains(err.Error(), "unpin") {
		t.Fatalf("error must name the recovery command, got: %v", err)
	}
}

func TestAdoptPinRejectsWrongLength(t *testing.T) {
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin([]byte{1, 2, 3}); err == nil {
		t.Fatal("a 3-byte genesis must be refused")
	}
}

func TestDropPinClearsEverything(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if d.TrustStore() != nil {
		t.Fatal("DropPin must clear the trust store")
	}
	if d.pinGenesis != nil {
		t.Fatal("DropPin must forget the pin")
	}
	got, err := trustpinFileForTest(d).Load()
	if err != nil || got != nil {
		t.Fatalf("DropPin must delete the pin file: got %v, %v", got, err)
	}
}

func TestDropPinLeavesLocalDisableAlone(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if !d.localDisabled() {
		t.Fatal("unpin and local-disable must stay orthogonal")
	}
}

func trustpinFileForTest(d *Node) *trustpin.File {
	return trustpin.New(genesisHashPath(d.trustPath))
}
