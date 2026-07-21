package trustlog

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenesisAndDeviceAuthorization(t *testing.T) {
	s, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s.Public}, s, nil)
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
	l, _ := NewGenesis([][]byte{s.Public}, s, nil)
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
	if _, err := NewGenesis([][]byte{s.Public}, other, nil); err == nil {
		t.Error("genesis signed by a non-member must fail")
	}
	if _, err := NewGenesis(nil, s, nil); err == nil {
		t.Error("genesis with no signers must fail")
	}
}

func TestUntrustedSignerCannotAuthorize(t *testing.T) {
	s, _ := GenerateSigner()
	rogue, _ := GenerateSigner()
	l, _ := NewGenesis([][]byte{s.Public}, s, nil)
	if err := l.AuthorizeDevice([]byte("d"), rogue); err == nil {
		t.Error("an untrusted signer must not be able to authorize a device")
	}
	if l.DeviceAuthorized([]byte("d")) {
		t.Error("device must not be authorized via a rogue signer")
	}
}

func TestDisableWithValidSecret(t *testing.T) {
	signer, _ := GenerateSigner()
	secret, _ := GenerateDisablementSecret()
	log, err := NewGenesis([][]byte{signer.Public}, signer, [][]byte{DisablementCommitment(secret)})
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	if log.Disabled() {
		t.Fatal("fresh log must not be disabled")
	}
	// A KindDisable authorized by the secret does NOT require a trusted signer:
	// sign it with a throwaway key.
	rogue, _ := GenerateSigner()
	if err := log.Disable(secret, rogue); err != nil {
		t.Fatalf("Disable with a valid secret: %v", err)
	}
	if !log.Disabled() {
		t.Fatal("log should be disabled after a valid secret")
	}
}

func TestDisableRejectsUnknownSecret(t *testing.T) {
	signer, _ := GenerateSigner()
	secret, _ := GenerateDisablementSecret()
	log, _ := NewGenesis([][]byte{signer.Public}, signer, [][]byte{DisablementCommitment(secret)})

	other, _ := GenerateDisablementSecret() // commitment NOT in the genesis
	if err := log.Disable(other, signer); err == nil {
		t.Fatal("Disable with an unknown secret must be rejected")
	}
	if log.Disabled() {
		t.Fatal("log must not be disabled by an unknown secret")
	}
}

func TestDisableIsTerminal(t *testing.T) {
	signer, _ := GenerateSigner()
	secret, _ := GenerateDisablementSecret()
	rogue, _ := GenerateSigner()

	// Build a chain: genesis → KindDisable (valid).
	log, err := NewGenesis([][]byte{signer.Public}, signer, [][]byte{DisablementCommitment(secret)})
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	if err := log.Disable(secret, rogue); err != nil {
		t.Fatalf("Disable with valid secret: %v", err)
	}
	if !log.Disabled() {
		t.Fatal("log should be disabled after a valid secret")
	}

	// Any further entry must be rejected without running the KDF again.
	// Try replaying the same KindDisable.
	entries := log.Entries()
	entries = append(entries, entries[len(entries)-1]) // duplicate the KindDisable
	if _, err := Load(entries); err == nil {
		t.Fatal("Load must reject an entry after KindDisable (replayed disable)")
	}

	// Try appending a normal entry (AuthorizeDevice) after disable.
	dev := []byte("device-pubkey-after-disable")
	if err := log.AuthorizeDevice(dev, signer); err == nil {
		t.Fatal("AuthorizeDevice after disable must be rejected")
	}
	if log.DeviceAuthorized(dev) {
		t.Fatal("device must not be authorized on a disabled log")
	}

	// Load path: a properly-chained entry after a disable must be rejected with
	// the disabled error, not a Prev-mismatch error (the duplicate-KindDisable
	// sub-test above hits the Prev-mismatch path instead of the disabled guard).
	disableHead := log.Head()
	after := Entry{
		Kind: KindAuthorizeDevice,
		Prev: disableHead,
		Key:  []byte("device-after-disable-load-path"),
	}
	sign(&after, signer)
	if _, loadErr := Load(append(log.Entries(), after)); loadErr == nil {
		t.Fatal("Load must reject a properly-chained entry after KindDisable")
	} else if !strings.Contains(loadErr.Error(), "disabled") {
		t.Fatalf("expected disabled error on Load path, got: %v", loadErr)
	}
}

func TestGenesisCommitmentsAreInHead(t *testing.T) {
	signer, _ := GenerateSigner()
	secret, _ := GenerateDisablementSecret()
	commit := DisablementCommitment(secret)
	a, _ := NewGenesis([][]byte{signer.Public}, signer, [][]byte{commit})
	// A genesis with a DIFFERENT commitment has a different head (commitments are signed
	// + hashed → tamper-evident, part of the pin).
	other, _ := GenerateDisablementSecret()
	b, _ := NewGenesis([][]byte{signer.Public}, signer, [][]byte{DisablementCommitment(other)})
	if bytes.Equal(a.Head(), b.Head()) {
		t.Fatal("genesis head must depend on the disablement commitments")
	}
}
