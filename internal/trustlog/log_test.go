package trustlog

import (
	"bytes"
	"strings"
	"testing"
)

// newGenesisTwoSigners creates a Log trusted by two signers (a and b) and returns both keys.
func newGenesisTwoSigners(t *testing.T) (*Log, SignerKey, SignerKey) {
	t.Helper()
	a, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner a: %v", err)
	}
	b, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner b: %v", err)
	}
	l, err := NewGenesis([][]byte{a.Public, b.Public}, a, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	return l, a, b
}

// mustNoErr fails the test immediately if err is non-nil.
func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

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
	h0 := l.Tip()
	if len(h0) != 32 {
		t.Fatalf("head len = %d", len(h0))
	}
	_ = l.AuthorizeDevice([]byte("d"), s)
	h1 := l.Tip()
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
	disableHead := log.Tip()
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
	if bytes.Equal(a.Tip(), b.Tip()) {
		t.Fatal("genesis head must depend on the disablement commitments")
	}
}

func TestRemoveSignerInvalidatesItsDevices(t *testing.T) {
	// genesis trusts signers A and B; A authorizes devA, B authorizes devB.
	l, a, b := newGenesisTwoSigners(t) // helper: returns *Log + two SignerKeys
	devA := bytes.Repeat([]byte{0xA}, 32)
	devB := bytes.Repeat([]byte{0xB}, 32)
	mustNoErr(t, l.AuthorizeDevice(devA, a))
	mustNoErr(t, l.AuthorizeDevice(devB, b))

	// remove signer A (signed by B) -> devA invalidated, devB stays.
	mustNoErr(t, l.RemoveSigner(a.Public, b))
	if l.DeviceAuthorized(devA) {
		t.Error("device authorized by removed signer A must be invalidated")
	}
	if !l.DeviceAuthorized(devB) {
		t.Error("device authorized by still-trusted signer B must remain")
	}

	// reload from bytes reproduces the same state (invalidation is in replay).
	l2, err := Load(l.Entries())
	mustNoErr(t, err)
	if l2.DeviceAuthorized(devA) || !l2.DeviceAuthorized(devB) {
		t.Error("reload must reproduce invalidation state")
	}
}

func TestReauthorizeBySurvivingSignerKeepsDevice(t *testing.T) {
	// Reachable path: authorize by A → revoke → authorize by B → remove A → still authorized.
	l, a, b := newGenesisTwoSigners(t)
	dev := bytes.Repeat([]byte{0xC}, 32)
	mustNoErr(t, l.AuthorizeDevice(dev, a)) // authorized by A
	mustNoErr(t, l.RevokeDevice(dev, b))    // revoke so B can re-authorize without double-authorize
	mustNoErr(t, l.AuthorizeDevice(dev, b)) // now authorized by B
	mustNoErr(t, l.RemoveSigner(a.Public, b))
	if !l.DeviceAuthorized(dev) {
		t.Error("device re-authorized by surviving signer B must remain")
	}
}

func TestDoubleAuthorizeRejected(t *testing.T) {
	l, a, b := newGenesisTwoSigners(t)
	dev := bytes.Repeat([]byte{0xD}, 32)
	mustNoErr(t, l.AuthorizeDevice(dev, a))
	if err := l.AuthorizeDevice(dev, b); err == nil {
		t.Error("a second AuthorizeDevice for an already-authorized device must return a non-nil error")
	}
	// Device must still be authorized (guard must not have cleared it).
	if !l.DeviceAuthorized(dev) {
		t.Error("device must remain authorized after a rejected re-authorization attempt")
	}
}

func TestApplyRevokeSignerRemovesSignersAndDevices(t *testing.T) {
	l, a, b := newGenesisTwoSigners(t)
	// add a third signer c so a 2-co-sign revoke of one signer is valid (2 > 1 revoked).
	c := mustGenSigner(t)
	mustNoErr(t, l.AddSigner(c.Public, a))
	devC := bytes.Repeat([]byte{0xCC}, 32)
	mustNoErr(t, l.AuthorizeDevice(devC, c)) // authorized by c

	// revoke c, co-signed by a and b (2 valid co-signs > 1 revoked signer).
	e := newRevokeSignerEntry(l.Tip(), [][]byte{c.Public}, nil, []SignerKey{a, b})
	if err := l.apply(&e); err != nil {
		t.Fatalf("apply revoke-signer: %v", err)
	}
	if l.SignerTrusted(c.Public) {
		t.Fatal("c must be revoked")
	}
	if l.DeviceAuthorized(devC) {
		t.Fatal("device authorized by revoked c must be invalidated")
	}

	// surviving signers a and b must still be trusted.
	if !l.SignerTrusted(a.Public) {
		t.Error("a must still be trusted")
	}
	if !l.SignerTrusted(b.Public) {
		t.Error("b must still be trusted")
	}

	// reload via Load reproduces the same state.
	l2, err := Load(l.Entries())
	mustNoErr(t, err)
	if l2.SignerTrusted(c.Public) {
		t.Error("reload: c must be revoked")
	}
	if l2.DeviceAuthorized(devC) {
		t.Error("reload: device authorized by revoked c must be invalidated")
	}
}

func TestRevokeSignerCannotRevokeEntireSignerSet(t *testing.T) {
	l, a, b := newGenesisTwoSigners(t)
	// Try to revoke both a and b with co-signs from a and b.
	// Because co-signers in the revoked set don't count, validCoSigns returns 0,
	// which fails the quorum (0 > 2 is false) — the error is the co-sign check,
	// not the last-signer guard.
	e := newRevokeSignerEntry(l.Tip(), [][]byte{a.Public, b.Public}, nil, []SignerKey{a, b})
	err := l.apply(&e)
	if err == nil {
		t.Fatal("revoking the entire signer set must be rejected")
	}
	if !strings.Contains(err.Error(), "lacks enough valid co-signs") {
		t.Fatalf("expected co-sign error, got: %v", err)
	}
}

func TestRevokeSignerInsufficientCoSignsRejected(t *testing.T) {
	l, a, _ := newGenesisTwoSigners(t)
	c := mustGenSigner(t)
	mustNoErr(t, l.AddSigner(c.Public, a))
	// Only one co-sign (a) for revoking c — but 1 is not > 1 revoked signer.
	e := newRevokeSignerEntry(l.Tip(), [][]byte{c.Public}, nil, []SignerKey{a})
	if err := l.apply(&e); err == nil {
		t.Fatal("revoke-signer with insufficient co-signs must be rejected")
	}
}

// TestRevokeSignerWithReplacement: genesis {a,b}; revoke a, atomically add c as
// replacement, co-signed by both a and b (2 > 1). After apply: a untrusted,
// c trusted, b trusted, a's devices invalidated.
func TestRevokeSignerWithReplacement(t *testing.T) {
	l, a, b := newGenesisTwoSigners(t)
	c := mustGenSigner(t)
	devA := bytes.Repeat([]byte{0xAA}, 32)
	mustNoErr(t, l.AuthorizeDevice(devA, a))

	// revoke a, replace with c, co-signed by a and b.
	// With Replaces set, validCoSignsWithReplaces allows the revoked signer (a) to count.
	e := newRevokeSignerEntry(l.Tip(), [][]byte{a.Public}, [][]byte{c.Public}, []SignerKey{a, b})
	if err := l.apply(&e); err != nil {
		t.Fatalf("apply revoke-signer with replacement: %v", err)
	}

	if l.SignerTrusted(a.Public) {
		t.Error("a must be revoked")
	}
	if !l.SignerTrusted(b.Public) {
		t.Error("b must remain trusted")
	}
	if !l.SignerTrusted(c.Public) {
		t.Error("c (replacement) must be trusted")
	}
	if l.DeviceAuthorized(devA) {
		t.Error("device authorized by revoked a must be invalidated")
	}

	// reload via Load reproduces the same state.
	l2, err := Load(l.Entries())
	mustNoErr(t, err)
	if l2.SignerTrusted(a.Public) {
		t.Error("reload: a must be revoked")
	}
	if !l2.SignerTrusted(c.Public) {
		t.Error("reload: c must be trusted")
	}
	if l2.DeviceAuthorized(devA) {
		t.Error("reload: a's device must be invalidated")
	}
}

// TestRevokeAllSignersWithoutReplacementRejected: revoking all signers without a
// replacement must fail — the co-sign check fails because all co-signers are
// in the revoked set and thus don't count.
func TestRevokeAllSignersWithoutReplacementRejected(t *testing.T) {
	l, a, b := newGenesisTwoSigners(t)
	// Both a and b are revoked; neither can be a valid co-signer → 0 > 2 = false.
	e := newRevokeSignerEntry(l.Tip(), [][]byte{a.Public, b.Public}, nil, []SignerKey{a, b})
	err := l.apply(&e)
	if err == nil {
		t.Fatal("revoking all signers without replacement must be rejected")
	}
	if !strings.Contains(err.Error(), "lacks enough valid co-signs") {
		t.Fatalf("expected co-sign error, got: %v", err)
	}
}

// TestRemainingAfterRevokeWithMatchesMapReference verifies that remainingAfterRevokeWith
// equals the reference (copy signers into fresh map, add every replace deduplicating
// via map, then remainingAfterRevoke) for a range of inputs covering duplicate replaces,
// replaces overlapping existing signers, and revoked pubkeys that are also in replaces.
func TestRemainingAfterRevokeWithMatchesMapReference(t *testing.T) {
	reference := func(signers map[string]bool, replaces, revoked [][]byte) int {
		m := map[string]bool{}
		for k, v := range signers {
			m[k] = v
		}
		for _, r := range replaces {
			m[string(r)] = true
		}
		return remainingAfterRevoke(m, revoked)
	}

	pub := func(b byte) []byte { return []byte{b} }

	cases := []struct {
		name     string
		signers  map[string]bool
		replaces [][]byte
		revoked  [][]byte
	}{
		{
			name:    "no replaces no revoked",
			signers: map[string]bool{string(pub(1)): true, string(pub(2)): true},
		},
		{
			name:     "replaces not in signers no duplicates",
			signers:  map[string]bool{string(pub(1)): true},
			replaces: [][]byte{pub(2), pub(3)},
			revoked:  [][]byte{pub(1)},
		},
		{
			name:     "duplicate replaces not in signers",
			signers:  map[string]bool{string(pub(1)): true},
			replaces: [][]byte{pub(2), pub(2)},
			revoked:  [][]byte{pub(1)},
		},
		{
			name:     "duplicate replaces with revoke of duplicate",
			signers:  map[string]bool{string(pub(1)): true},
			replaces: [][]byte{pub(2), pub(2)},
			revoked:  [][]byte{pub(2), pub(1)},
		},
		{
			name:     "replaces overlapping existing signers",
			signers:  map[string]bool{string(pub(1)): true, string(pub(2)): true},
			replaces: [][]byte{pub(2), pub(3)},
			revoked:  [][]byte{pub(2)},
		},
		{
			name:     "revoked also in replaces",
			signers:  map[string]bool{string(pub(1)): true, string(pub(2)): true},
			replaces: [][]byte{pub(3)},
			revoked:  [][]byte{pub(3), pub(1)},
		},
		{
			name:     "duplicate replaces with revoked in replaces",
			signers:  map[string]bool{string(pub(1)): true},
			replaces: [][]byte{pub(2), pub(2), pub(3)},
			revoked:  [][]byte{pub(2), pub(1)},
		},
		{
			name:     "all replaces duplicated revoked in replaces",
			signers:  map[string]bool{string(pub(1)): true},
			replaces: [][]byte{pub(2), pub(2)},
			revoked:  [][]byte{pub(2)},
		},
		{
			name:     "empty signers with duplicate replaces",
			signers:  map[string]bool{},
			replaces: [][]byte{pub(1), pub(1), pub(2)},
			revoked:  [][]byte{pub(1)},
		},
		{
			name:     "no revoked many duplicate replaces",
			signers:  map[string]bool{string(pub(1)): true},
			replaces: [][]byte{pub(2), pub(2), pub(3), pub(3)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := reference(tc.signers, tc.replaces, tc.revoked)
			got := remainingAfterRevokeWith(tc.signers, tc.replaces, tc.revoked)
			if got != want {
				t.Errorf("remainingAfterRevokeWith = %d, want %d (reference)", got, want)
			}
		})
	}
}

func TestLogCloneIsDeepAndIndependent(t *testing.T) {
	// Build a small verified chain via the public helpers used elsewhere in tests.
	g := genChain(t, 1, 6) // from gen_test.go (Task 3 step 3): seed=1, ~6 ops
	orig, err := Load(g.entries)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cp := orig.clone()
	if !bytes.Equal(cp.Tip(), orig.Tip()) {
		t.Fatal("clone tip mismatch")
	}
	// Mutating the clone must not touch the original.
	cp.signers["zzz"] = true
	if orig.signers["zzz"] {
		t.Fatal("clone shares the signers map with the original")
	}
	cp.entries = append(cp.entries, Entry{})
	if len(cp.entries) == len(orig.entries) {
		t.Fatal("clone shares the entries slice with the original")
	}
}
