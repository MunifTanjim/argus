package trustlog

import (
	"bytes"
	"testing"
)

// mustIngest ingests chain into s or fails the test.
func mustIngest(t *testing.T, s *Store, chain []byte) {
	t.Helper()
	if err := s.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

// buildForkGenesis returns two independent logs that share an identical
// (deterministic, since Ed25519 signing is deterministic) genesis trusted by the
// returned signers, plus the shared genesis hash. Appending different entries to
// each log produces same-genesis divergent branches.
func buildForkGenesis(t *testing.T, signers ...SignerKey) (la, lb *Log, gh []byte) {
	t.Helper()
	pubs := make([][]byte, len(signers))
	for i, s := range signers {
		pubs[i] = s.Public
	}
	var err error
	la, err = NewGenesis(pubs, signers[0], nil)
	mustNoErr(t, err)
	lb, err = NewGenesis(pubs, signers[0], nil)
	mustNoErr(t, err)
	if !bytes.Equal(la.Tip(), lb.Tip()) {
		t.Fatal("genesis must be deterministic for both branches")
	}
	return la, lb, la.Tip()
}

// forkFixtureCompromisedSigner builds:
//   - cur: genesis(a,b,c) -> c authorizes two attacker devices (LONGER, single-signed by c)
//   - cand: genesis(a,b,c) -> revoke-signer(c) co-signed by a,b (SHORTER)
//
// cand must win despite being shorter. Returns marshalled chains, the shared
// genesis hash, and the compromised signer c.
func forkFixtureCompromisedSigner(t *testing.T) (cur, cand, gh []byte, c SignerKey) {
	t.Helper()
	a, b := mustGenSigner(t), mustGenSigner(t)
	c = mustGenSigner(t)
	la, lb, ghash := buildForkGenesis(t, a, b, c)
	// current: compromised c authorizes attacker devices — a longer, plain branch.
	mustNoErr(t, la.AuthorizeDevice(bytes.Repeat([]byte{0xA1}, 32), c))
	mustNoErr(t, la.AuthorizeDevice(bytes.Repeat([]byte{0xA2}, 32), c))
	// candidate: a,b co-sign a revocation of c — a shorter branch (2 co-signs > 1 revoked).
	e := newRevokeSignerEntry(lb.Tip(), [][]byte{c.Public}, nil, []SignerKey{a, b})
	mustNoErr(t, lb.apply(&e))
	return MarshalChain(la.Entries()), MarshalChain(lb.Entries()), ghash, c
}

func TestForkCoSignedShorterBranchBeatsLongerPlain(t *testing.T) {
	cur, cand, gh, c := forkFixtureCompromisedSigner(t)
	s := NewStore(gh)
	mustIngest(t, s, cur) // adopt the (longer) attacker branch first
	if err := s.Ingest(cand); err != nil {
		t.Fatalf("co-signed shorter branch must be adopted: %v", err)
	}
	if s.SignerTrusted(c.Public) {
		t.Fatal("after adopting the revoke branch, c must be untrusted")
	}
	if s.DeviceAuthorized(bytes.Repeat([]byte{0xA1}, 32)) || s.DeviceAuthorized(bytes.Repeat([]byte{0xA2}, 32)) {
		t.Fatal("attacker devices must not be authorized after adopting the revoke branch")
	}
	if !bytes.Equal(s.Bytes(), cand) {
		t.Fatal("store must hold the co-signed revoke branch")
	}
}

// forkFixturePlain builds two plain single-signed branches from the same genesis,
// neither carrying a co-signed revocation.
func forkFixturePlain(t *testing.T) (cur, cand, gh []byte) {
	t.Helper()
	a, b := mustGenSigner(t), mustGenSigner(t)
	la, lb, ghash := buildForkGenesis(t, a, b)
	mustNoErr(t, la.AuthorizeDevice(bytes.Repeat([]byte{0xD1}, 32), a))
	mustNoErr(t, lb.AuthorizeDevice(bytes.Repeat([]byte{0xD2}, 32), b))
	return MarshalChain(la.Entries()), MarshalChain(lb.Entries()), ghash
}

// TestForkPlainDivergenceResolved: two plain (no co-signed removal) same-genesis
// branches now resolve deterministically at the fork point rather than erroring.
// Both first-diverging entries are normal single-signed entries (weight 1, not a
// removal), so the winner is the lowest first-diverging-entry hash — identical in
// both ingest orders.
func TestForkPlainDivergenceResolved(t *testing.T) {
	cur, cand, gh := forkFixturePlain(t)

	s1 := NewStore(gh)
	mustIngest(t, s1, cur)
	if err := s1.Ingest(cand); err != nil {
		t.Fatalf("a plain fork must resolve deterministically, not error: %v", err)
	}
	s2 := NewStore(gh)
	mustIngest(t, s2, cand)
	if err := s2.Ingest(cur); err != nil {
		t.Fatalf("a plain fork must resolve deterministically, not error: %v", err)
	}
	if !bytes.Equal(s1.Bytes(), s2.Bytes()) {
		t.Fatal("plain fork winner must be identical regardless of ingest order")
	}

	ce, err := UnmarshalChain(cur)
	mustNoErr(t, err)
	de, err := UnmarshalChain(cand)
	mustNoErr(t, err)
	hCur := hashEntry(&ce[1])
	hCand := hashEntry(&de[1])
	winner := cur
	if bytes.Compare(hCand, hCur) < 0 {
		winner = cand
	}
	if !bytes.Equal(s1.Bytes(), winner) {
		t.Fatal("plain fork must adopt the lexicographically-lower-first-entry branch")
	}
}

func TestLinearExtendStillAdopted(t *testing.T) {
	gh, short, extended, dev := buildStoreChain(t)
	s := NewStore(gh)
	mustIngest(t, s, short)
	mustIngest(t, s, extended) // prefix-preserving longer extension
	if s.DeviceAuthorized(dev) {
		t.Error("linear extension must be adopted (device revoked)")
	}
	if !bytes.Equal(s.Bytes(), extended) {
		t.Error("store must hold the extended chain")
	}
}

func TestShorterPlainPrefixRejectedAsRollback(t *testing.T) {
	gh, short, extended, dev := buildStoreChain(t)
	s := NewStore(gh)
	mustIngest(t, s, extended)
	// candidate is a strict prefix -> keep current (no-op, no rollback).
	if err := s.Ingest(short); err != nil {
		t.Fatalf("strict-prefix candidate must be a no-op, got: %v", err)
	}
	if s.DeviceAuthorized(dev) {
		t.Error("state must stay on the longer chain (device stays revoked)")
	}
	if !bytes.Equal(s.Bytes(), extended) {
		t.Error("store must keep the current (longer) chain")
	}
}

// forkFixtureTieBreak builds two co-signed revoke branches with EQUAL valid
// co-sign counts (2 each) but different revoked targets → different tip hashes.
// The winner is the lexicographically-lower tip, deterministically.
func forkFixtureTieBreak(t *testing.T) (x, y, gh []byte) {
	t.Helper()
	a, b, c, d := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	lx, ly, ghash := buildForkGenesis(t, a, b, c, d)
	ex := newRevokeSignerEntry(lx.Tip(), [][]byte{c.Public}, nil, []SignerKey{a, b})
	mustNoErr(t, lx.apply(&ex))
	ey := newRevokeSignerEntry(ly.Tip(), [][]byte{d.Public}, nil, []SignerKey{a, b})
	mustNoErr(t, ly.apply(&ey))
	return MarshalChain(lx.Entries()), MarshalChain(ly.Entries()), ghash
}

func TestForkTieBreakDeterministic(t *testing.T) {
	x, y, gh := forkFixtureTieBreak(t)

	// Ingest x then y.
	s1 := NewStore(gh)
	mustIngest(t, s1, x)
	if err := s1.Ingest(y); err != nil {
		t.Fatalf("tie-break ingest y over x must not error: %v", err)
	}
	// Ingest y then x.
	s2 := NewStore(gh)
	mustIngest(t, s2, y)
	if err := s2.Ingest(x); err != nil {
		t.Fatalf("tie-break ingest x over y must not error: %v", err)
	}
	if !bytes.Equal(s1.Bytes(), s2.Bytes()) {
		t.Fatal("tie-break winner must be identical regardless of ingest order")
	}

	// The winner must be the branch with the lexicographically-lower tip hash.
	xe, err := UnmarshalChain(x)
	mustNoErr(t, err)
	ye, err := UnmarshalChain(y)
	mustNoErr(t, err)
	xTip := hashEntry(&xe[len(xe)-1])
	yTip := hashEntry(&ye[len(ye)-1])
	winner := y
	if bytes.Compare(xTip, yTip) < 0 {
		winner = x
	}
	if !bytes.Equal(s1.Bytes(), winner) {
		t.Fatal("tie-break must adopt the lexicographically-lower-tip branch")
	}
}

// TestForkHigherCoSignCountWins: a revocation with more valid co-signs beats one
// with fewer, regardless of ingest order (and regardless of tip ordering).
func TestForkHigherCoSignCountWins(t *testing.T) {
	a, b, c, d, e := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	lp, lq, gh := buildForkGenesis(t, a, b, c, d, e)
	// P: revoke e, co-signed by a,b,c → 3 valid co-signs.
	ep := newRevokeSignerEntry(lp.Tip(), [][]byte{e.Public}, nil, []SignerKey{a, b, c})
	mustNoErr(t, lp.apply(&ep))
	// Q: revoke d, co-signed by a,b → 2 valid co-signs.
	eq := newRevokeSignerEntry(lq.Tip(), [][]byte{d.Public}, nil, []SignerKey{a, b})
	mustNoErr(t, lq.apply(&eq))
	p := MarshalChain(lp.Entries())
	q := MarshalChain(lq.Entries())

	s1 := NewStore(gh)
	mustIngest(t, s1, q)
	mustIngest(t, s1, p) // higher-count P must win over current Q
	s2 := NewStore(gh)
	mustIngest(t, s2, p)
	mustIngest(t, s2, q) // lower-count Q must lose to current P (no-op)

	if !bytes.Equal(s1.Bytes(), p) || !bytes.Equal(s2.Bytes(), p) {
		t.Fatal("the higher co-sign-count branch must win regardless of ingest order")
	}
}

// TestForkPuppetSignersCannotWin is the core attack the fork-point rule defends
// against: a compromised signer c forks, adds puppet signers p1,p2,p3 (which it can
// do — c is trusted), then has {c,p1,p2,p3} co-sign a revocation of the honest
// signers a,b. That attacker branch is LONGER and carries MORE total valid co-signs,
// yet it must LOSE to the honest branch's 2-co-sign revoke of c.
//
// Why: weight is read from the FIRST diverging entry, counting only signers trusted
// AT THE FORK POINT ({a,b,c}). On the attacker branch the first diverging entry is
// addSigner(p1) by c — weight 1, not a removal. The puppets are added AFTER the fork,
// so they are not in the fork-point set and contribute 0 to any later revoke. The
// honest branch's first diverging entry IS the co-signed revoke(c) — weight 2 (a,b),
// a removal — so it wins in both ingest orders.
func TestForkPuppetSignersCannotWin(t *testing.T) {
	a, b, c := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	p1, p2, p3 := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	lh, la, gh := buildForkGenesis(t, a, b, c)

	// Honest branch: a,b co-sign a revocation of c — the first diverging entry.
	eh := newRevokeSignerEntry(lh.Tip(), [][]byte{c.Public}, nil, []SignerKey{a, b})
	mustNoErr(t, lh.apply(&eh))
	honest := MarshalChain(lh.Entries())

	// Attacker branch: compromised c adds three puppet signers, then {c,p1,p2,p3}
	// co-sign a revocation of a,b — longer, 4 total co-signs, but the first diverging
	// entry is addSigner(p1) (weight 1, not a removal).
	mustNoErr(t, la.AddSigner(p1.Public, c))
	mustNoErr(t, la.AddSigner(p2.Public, c))
	mustNoErr(t, la.AddSigner(p3.Public, c))
	ea := newRevokeSignerEntry(la.Tip(), [][]byte{a.Public, b.Public}, nil, []SignerKey{c, p1, p2, p3})
	mustNoErr(t, la.apply(&ea))
	attacker := MarshalChain(la.Entries())

	assertHonestWins := func(t *testing.T, first, second []byte) {
		t.Helper()
		s := NewStore(gh)
		mustIngest(t, s, first)
		if err := s.Ingest(second); err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if !s.SignerTrusted(a.Public) || !s.SignerTrusted(b.Public) {
			t.Fatal("honest signers a,b must stay trusted (attacker branch must lose)")
		}
		if s.SignerTrusted(c.Public) {
			t.Fatal("compromised signer c must be revoked (honest branch must win)")
		}
		if s.SignerTrusted(p1.Public) || s.SignerTrusted(p2.Public) || s.SignerTrusted(p3.Public) {
			t.Fatal("puppet signers must never be trusted (attacker branch must lose)")
		}
		if !bytes.Equal(s.Bytes(), honest) {
			t.Fatal("store must hold the honest revoke-c branch")
		}
	}

	assertHonestWins(t, honest, attacker) // honest first, attacker ingested over it
	assertHonestWins(t, attacker, honest) // attacker first, honest ingested over it
}
