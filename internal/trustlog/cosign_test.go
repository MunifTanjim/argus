package trustlog

import (
	"bytes"
	"testing"
)

// newGenesisThreeSigners creates a Log trusted by three signers (a, b, c) with a as the
// genesis signer.
func newGenesisThreeSigners(t *testing.T) (*Log, SignerKey, SignerKey, SignerKey) {
	t.Helper()
	a, b, c := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	l, err := NewGenesis([][]byte{a.Public, b.Public, c.Public}, a, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	return l, a, b, c
}

// TestCosignCeremony: genesis {a,b,c}; StartRevoke(revoked={c}, by=a) → not complete
// (1 co-sign, need >1); AddCoSign(by=b) → Complete==true; Finalize → entry with 2
// co-signs; ingest into a Store and assert c is revoked.
func TestCosignCeremony(t *testing.T) {
	log, a, b, c := newGenesisThreeSigners(t)
	genesisHash := log.Tip() // only genesis in log; Tip = hash of genesis entry

	// StartRevoke: c has not signed anything; default fork point = current tip = genesis hash.
	pr, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, a)
	if err != nil {
		t.Fatalf("StartRevoke: %v", err)
	}

	// 1 co-sign (a) for 1 revoked (c): 1 > 1 is false → not complete.
	if Complete(pr, log) {
		t.Fatal("should not be complete with only 1 co-sign for 1 revoked signer")
	}

	// Marshal/unmarshal round-trip (simulates blob passing between nodes).
	blob := pr.Marshal()
	pr, err = UnmarshalPendingRevoke(blob)
	if err != nil {
		t.Fatalf("UnmarshalPendingRevoke: %v", err)
	}

	// AddCoSign by b (on another node).
	pr, err = AddCoSign(pr, log, b)
	if err != nil {
		t.Fatalf("AddCoSign by b: %v", err)
	}

	// 2 co-signs (a, b) for 1 revoked (c): 2 > 1 → complete; post-apply signers = {a,b} ≥ 1.
	if !Complete(pr, log) {
		t.Fatal("should be complete with 2 co-signs for 1 revoked signer")
	}

	entry, err := Finalize(pr)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if entry.Kind != KindRevokeSigner {
		t.Fatalf("want KindRevokeSigner, got kind %d", entry.Kind)
	}
	if len(entry.CoSigns) != 2 {
		t.Fatalf("want 2 co-signs in finalized entry, got %d", len(entry.CoSigns))
	}

	// Build the revoke chain [genesis, revoke(c)] and ingest into a fresh Store.
	chain := append(log.Entries(), entry) // [genesis, KindRevokeSigner]
	st := NewStore(genesisHash)
	if err := st.Ingest(MarshalChain(chain)); err != nil {
		t.Fatalf("Store.Ingest: %v", err)
	}
	if st.SignerTrusted(c.Public) {
		t.Error("c must be revoked after ingesting the finalized entry")
	}
	if !st.SignerTrusted(a.Public) {
		t.Error("a must remain trusted")
	}
	if !st.SignerTrusted(b.Public) {
		t.Error("b must remain trusted")
	}
}

// TestCosignRejectsUntrustedCoSigner: a key not in the fork-point signer set must be
// rejected by both StartRevoke and AddCoSign.
func TestCosignRejectsUntrustedCoSigner(t *testing.T) {
	log, a, _, c := newGenesisThreeSigners(t)
	x := mustGenSigner(t) // never in genesis → not trusted at fork point

	pr, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, a)
	mustNoErr(t, err)

	// AddCoSign by untrusted x must be rejected.
	if _, err := AddCoSign(pr, log, x); err == nil {
		t.Fatal("AddCoSign by a signer not trusted at the fork point must be rejected")
	}

	// StartRevoke by untrusted x must also be rejected.
	if _, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, x); err == nil {
		t.Fatal("StartRevoke by a signer not trusted at the fork point must be rejected")
	}
}

// TestCosignForkPointDefaultErasesRevokedSignerActions: c (compromised) adds a puppet
// signer; the default fork point is the Prev of c's first non-genesis entry (genesis
// hash), so Finalize produces a chain that erases the puppet via fork resolution.
func TestCosignForkPointDefaultErasesRevokedSignerActions(t *testing.T) {
	log, a, b, c := newGenesisThreeSigners(t)
	genesisHash := log.Tip() // hash of genesis entry
	puppet := mustGenSigner(t)

	// c (compromised) adds puppet as a new signer.
	mustNoErr(t, log.AddSigner(puppet.Public, c))

	// Default fork point: scan for c's first non-genesis signed entry (the AddSigner);
	// fork from its Prev = genesis hash.
	pr, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, a)
	mustNoErr(t, err)

	// Verify the fork point is the genesis hash, not the current tip.
	if !bytes.Equal(pr.ForkPoint(), genesisHash) {
		t.Fatalf("fork point should be genesis hash (c's AddSigner Prev), got something else")
	}

	pr, err = AddCoSign(pr, log, b)
	mustNoErr(t, err)

	if !Complete(pr, log) {
		t.Fatal("2 co-signs {a,b} for 1 revoked {c} must be complete")
	}

	entry, err := Finalize(pr)
	mustNoErr(t, err)

	// Revoke chain: [genesis, revoke(c)] — forks from genesis hash.
	revokeChain := []Entry{log.Entries()[0], entry}

	st := NewStore(genesisHash)
	// Ingest compromised chain first: [genesis, addSigner(puppet)].
	mustIngest(t, st, MarshalChain(log.Entries()))
	// Ingest revoke chain: weight 2 (a+b co-signs) vs weight 1 (c's single-signed addSigner)
	// → revoke chain wins.
	mustIngest(t, st, MarshalChain(revokeChain))

	if st.SignerTrusted(c.Public) {
		t.Error("c must be revoked")
	}
	if st.SignerTrusted(puppet.Public) {
		t.Error("puppet added by c must be erased after the revoke chain wins the fork")
	}
	if !st.SignerTrusted(a.Public) {
		t.Error("a must remain trusted")
	}
	if !st.SignerTrusted(b.Public) {
		t.Error("b must remain trusted")
	}
}

// TestCosignDuplicateCoSignRejected: the same signer co-signing twice must be rejected.
func TestCosignDuplicateCoSignRejected(t *testing.T) {
	log, a, _, c := newGenesisThreeSigners(t)

	pr, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, a)
	mustNoErr(t, err)

	if _, err := AddCoSign(pr, log, a); err == nil {
		t.Fatal("adding a duplicate co-sign by the same signer must be rejected")
	}
}

// TestCosignMarshalRoundTrip: Marshal/Unmarshal preserves all fields of a PendingRevoke.
func TestCosignMarshalRoundTrip(t *testing.T) {
	log, a, b, c := newGenesisThreeSigners(t)

	pr, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, a)
	mustNoErr(t, err)

	pr, err = AddCoSign(pr, log, b)
	mustNoErr(t, err)

	blob := pr.Marshal()
	pr2, err := UnmarshalPendingRevoke(blob)
	if err != nil {
		t.Fatalf("UnmarshalPendingRevoke: %v", err)
	}

	if !bytes.Equal(pr.ForkPoint(), pr2.ForkPoint()) {
		t.Error("ForkPoint mismatch after round-trip")
	}
	if len(pr2.partial.CoSigns) != 2 {
		t.Errorf("want 2 co-signs after round-trip, got %d", len(pr2.partial.CoSigns))
	}
	// The round-tripped blob must still be complete and finalizable.
	if !Complete(pr2, log) {
		t.Error("round-tripped PendingRevoke must still be complete")
	}
	if _, err := Finalize(pr2); err != nil {
		t.Fatalf("Finalize after round-trip: %v", err)
	}
}

// TestCosignFinalizeNotCompleteShapeErrors: Finalize on an entry missing required fields
// must return an error.
func TestCosignFinalizeNotCompleteShapeErrors(t *testing.T) {
	// Zero-value PendingRevoke (no signers, no co-signs, no fork point) must error.
	var zero PendingRevoke
	if _, err := Finalize(zero); err == nil {
		t.Error("Finalize on a zero PendingRevoke must return an error")
	}
}

// TestCosignCeremonyWithReplacement: genesis {a,b,c}; a authorizes dev1; ceremony
// StartRevoke(revoked={a}, replaces={d}, by=b) → AddCoSign(by=c) → Complete==true →
// Finalize → ingest into a Store → assert a untrusted, d trusted, b/c trusted, and
// a's device (dev1) no longer authorized.
func TestCosignCeremonyWithReplacement(t *testing.T) {
	log, a, b, c := newGenesisThreeSigners(t)
	d := mustGenSigner(t)
	genesisHash := hashEntry(&log.Entries()[0])

	// a authorizes dev1; this makes selectForkPoint choose genesis hash as fork point
	// (Prev of a's first non-genesis action), so the revoke chain forks from genesis.
	dev1 := []byte("dev1-pubkey")
	mustNoErr(t, log.AuthorizeDevice(dev1, a))

	// StartRevoke: revoke a, replace with d, initiated by b.
	pr, err := StartRevoke(log, [][]byte{a.Public}, [][]byte{d.Public}, nil, b)
	if err != nil {
		t.Fatalf("StartRevoke: %v", err)
	}
	if !bytes.Equal(pr.ForkPoint(), genesisHash) {
		t.Fatalf("fork point should be genesis hash (Prev of a's authDevice), got different hash")
	}

	// 1 co-sign (b) for 1 revoked (a) using validCoSignsWithReplaces → 1 > 1 is false → not complete.
	if Complete(pr, log) {
		t.Fatal("should not be complete with only 1 co-sign for 1 revoked signer")
	}

	pr, err = AddCoSign(pr, log, c)
	if err != nil {
		t.Fatalf("AddCoSign by c: %v", err)
	}

	// 2 co-signs (b, c) for 1 revoked (a) → complete; post-apply {b,c,d} ≥ 1.
	if !Complete(pr, log) {
		t.Fatal("should be complete with 2 co-signs for 1 revoked signer")
	}

	entry, err := Finalize(pr)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(entry.CoSigns) != 2 {
		t.Fatalf("want 2 co-signs in finalized entry, got %d", len(entry.CoSigns))
	}

	// Revoke chain forks from genesis hash: [genesis, revoke(a→d)].
	revokeChain := []Entry{log.Entries()[0], entry}
	st := NewStore(genesisHash)
	// Ingest original chain first so dev1 is authorized; revoke chain should win the fork.
	mustIngest(t, st, MarshalChain(log.Entries()))
	if !st.DeviceAuthorized(dev1) {
		t.Fatal("dev1 must be authorized before revoke chain is ingested")
	}
	mustIngest(t, st, MarshalChain(revokeChain))

	if st.SignerTrusted(a.Public) {
		t.Error("a must be revoked")
	}
	if !st.SignerTrusted(d.Public) {
		t.Error("d (replacement) must be trusted")
	}
	if !st.SignerTrusted(b.Public) {
		t.Error("b must remain trusted")
	}
	if !st.SignerTrusted(c.Public) {
		t.Error("c must remain trusted")
	}
	if st.DeviceAuthorized(dev1) {
		t.Error("dev1 authorized by a must be invalidated after revoke chain wins")
	}
}

// TestStartRevokeForkFromOverride: passing a non-nil forkFrom hash uses that exact hash
// as the fork point, overriding the auto-selected default.
func TestStartRevokeForkFromOverride(t *testing.T) {
	log, a, b, c := newGenesisThreeSigners(t)
	_ = a
	genesisHash := hashEntry(&log.Entries()[0])

	// c signs an entry so the log tip advances past genesis.
	mustNoErr(t, log.AuthorizeDevice([]byte("dev"), c))
	currentTip := log.Tip()
	if bytes.Equal(genesisHash, currentTip) {
		t.Fatal("setup: tip must differ from genesis hash after the device auth")
	}

	// Auto-selected default for revoking c: Prev of c's authorizeDevice = genesisHash.
	prDefault, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, b)
	mustNoErr(t, err)
	if !bytes.Equal(prDefault.ForkPoint(), genesisHash) {
		t.Fatalf("default fork point should be genesis hash, got different hash")
	}

	// Override forkFrom to the current tip (after c's action) instead.
	prOverride, err := StartRevoke(log, [][]byte{c.Public}, nil, currentTip, b)
	mustNoErr(t, err)
	if !bytes.Equal(prOverride.ForkPoint(), currentTip) {
		t.Fatalf("overridden fork point should be currentTip, got different hash")
	}
	if bytes.Equal(prOverride.ForkPoint(), prDefault.ForkPoint()) {
		t.Fatal("override fork point must differ from the auto-selected default")
	}
}

// TestUnmarshalPendingRevokeRejectsGarbage: UnmarshalPendingRevoke must return an
// error (no panic) on empty bytes, truncated bytes, and a wrong-kind entry blob.
func TestUnmarshalPendingRevokeRejectsGarbage(t *testing.T) {
	// empty bytes
	if _, err := UnmarshalPendingRevoke([]byte{}); err == nil {
		t.Error("empty bytes must be rejected")
	}

	// truncated: build a valid blob then cut it in half
	log, a, _, c := newGenesisThreeSigners(t)
	pr, err := StartRevoke(log, [][]byte{c.Public}, nil, nil, a)
	mustNoErr(t, err)
	blob := pr.Marshal()
	if _, err := UnmarshalPendingRevoke(blob[:len(blob)/2]); err == nil {
		t.Error("truncated bytes must be rejected")
	}

	// wrong kind: a KindGenesis blob is not a KindRevokeSigner
	genesisBlob := MarshalEntry(log.Entries()[0])
	if _, err := UnmarshalPendingRevoke(genesisBlob); err == nil {
		t.Error("wrong-kind entry (KindGenesis) must be rejected")
	}
}

// TestAddCoSignRejectsRevokedCoSignerWithoutReplacement: if the pending revoke has no
// Replaces, a signer in the revoked set must be rejected by AddCoSign; but if Replaces
// IS set (voluntary succession), the same revoked signer must be accepted.
func TestAddCoSignRejectsRevokedCoSignerWithoutReplacement(t *testing.T) {
	log, a, b, _ := newGenesisThreeSigners(t)

	// No Replaces: a is in the revoked set → AddCoSign by a must fail.
	pr, err := StartRevoke(log, [][]byte{a.Public}, nil, nil, b)
	mustNoErr(t, err)
	if _, err := AddCoSign(pr, log, a); err == nil {
		t.Fatal("AddCoSign by a revoked signer without Replaces must be rejected")
	}

	// With Replaces: a is in the revoked set but this is voluntary succession → must succeed.
	d := mustGenSigner(t)
	prWithReplaces, err := StartRevoke(log, [][]byte{a.Public}, [][]byte{d.Public}, nil, b)
	mustNoErr(t, err)
	if _, err := AddCoSign(prWithReplaces, log, a); err != nil {
		t.Fatalf("AddCoSign by a revoked signer WITH Replaces must succeed: %v", err)
	}
}
