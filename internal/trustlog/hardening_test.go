package trustlog

import "testing"

// TestGenesisRejectsDuplicateSigners: a genesis listing the same signer twice must
// be rejected. fold dedups into a set, but SignerSetFingerprint counts raw entries,
// so a duplicate would produce a misleading human fingerprint for the same set.
func TestGenesisRejectsDuplicateSigners(t *testing.T) {
	a := mustGenSigner(t)
	if _, err := NewGenesis([][]byte{a.Public, a.Public}, a, nil); err == nil {
		t.Fatal("genesis with duplicate signers must be rejected")
	}
}

// TestRevokeRejectsSignerReplaceOverlap: a revoke that lists the same pubkey in both
// the revoked set (Signers) and Replaces is contradictory (revoke-then-re-add) and
// must be rejected rather than silently resolving to a no-op.
func TestRevokeRejectsSignerReplaceOverlap(t *testing.T) {
	a, b := mustGenSigner(t), mustGenSigner(t)
	l, err := NewGenesis([][]byte{a.Public, b.Public}, a, nil)
	mustNoErr(t, err)
	if err := l.RevokeSigner([][]byte{b.Public}, [][]byte{b.Public}, []SignerKey{a, b}); err == nil {
		t.Fatal("revoke with a pubkey in both Signers and Replaces must be rejected")
	}
}

// TestNonRevokeRejectsRevokeOnlyFields: a non-revoke entry that carries the
// revoke-only Replaces/CoSigns fields must be rejected (defense-in-depth; not
// wire-reachable via the codec, but reachable through the Go API).
func TestNonRevokeRejectsRevokeOnlyFields(t *testing.T) {
	a := mustGenSigner(t)
	l, err := NewGenesis([][]byte{a.Public}, a, nil)
	mustNoErr(t, err)
	b := mustGenSigner(t)

	t.Run("replaces", func(t *testing.T) {
		e := Entry{Kind: KindAddSigner, Prev: l.Tip(), Key: b.Public, Replaces: [][]byte{b.Public}}
		sign(&e, a)
		if err := l.clone().apply(&e); err == nil {
			t.Fatal("a non-revoke entry carrying Replaces must be rejected")
		}
	})
	t.Run("cosigns", func(t *testing.T) {
		e := Entry{Kind: KindAddSigner, Prev: l.Tip(), Key: b.Public, CoSigns: []CoSign{{Signer: a.Public, Sig: []byte("x")}}}
		sign(&e, a)
		if err := l.clone().apply(&e); err == nil {
			t.Fatal("a non-revoke entry carrying CoSigns must be rejected")
		}
	})
}
