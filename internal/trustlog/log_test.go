package trustlog

import (
	"bytes"
	"testing"
)

func TestGenesisAndDeviceAuthorization(t *testing.T) {
	s, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s.Public}, s)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	if !l.SignerTrusted(s.Public) {
		t.Error("genesis signer should be trusted")
	}
	dev := []byte("device-pubkey-1")
	if l.DeviceAuthorized(dev) {
		t.Fatal("device authorized before any authorize entry")
	}
	if err := l.AuthorizeDevice(dev, s); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	if !l.DeviceAuthorized(dev) {
		t.Error("device not authorized after AuthorizeDevice")
	}
	if err := l.RevokeDevice(dev, s); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if l.DeviceAuthorized(dev) {
		t.Error("device still authorized after RevokeDevice")
	}
}

func TestHeadAdvancesPerEntry(t *testing.T) {
	s, _ := GenerateSigner()
	l, _ := NewGenesis([][]byte{s.Public}, s)
	h0 := l.Head()
	if len(h0) != 32 {
		t.Fatalf("head len = %d", len(h0))
	}
	_ = l.AuthorizeDevice([]byte("d"), s)
	h1 := l.Head()
	if bytes.Equal(h0, h1) {
		t.Error("head must advance after an entry")
	}
	if len(l.Entries()) != 2 {
		t.Errorf("entries = %d, want 2 (genesis + authorize)", len(l.Entries()))
	}
}

func TestGenesisRejectsSignerNotInSet(t *testing.T) {
	s, _ := GenerateSigner()
	other, _ := GenerateSigner()
	if _, err := NewGenesis([][]byte{s.Public}, other); err == nil {
		t.Error("genesis signed by a non-member must fail")
	}
	if _, err := NewGenesis(nil, s); err == nil {
		t.Error("genesis with no signers must fail")
	}
}

func TestUntrustedSignerCannotAuthorize(t *testing.T) {
	s, _ := GenerateSigner()
	rogue, _ := GenerateSigner()
	l, _ := NewGenesis([][]byte{s.Public}, s)
	if err := l.AuthorizeDevice([]byte("d"), rogue); err == nil {
		t.Error("an untrusted signer must not be able to authorize a device")
	}
	if l.DeviceAuthorized([]byte("d")) {
		t.Error("device must not be authorized via a rogue signer")
	}
}
