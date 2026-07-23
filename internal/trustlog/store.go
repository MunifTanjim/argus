package trustlog

import (
	"bytes"
	"errors"
)

// Store holds a verified trust-log chain, pinned to a genesis hash learned
// out-of-band. Ingest adopts a candidate that is a same-genesis, fully-verified
// linear extension, or — on a true fork — the winner of the fork-point resolution
// rule (the sibling first-diverging entry with more weight from signers trusted at
// the fork point; a co-signed key revocation beats a plain branch even when
// shorter). This is the rollback/fork/tamper defense for chains relayed by an
// untrusted gateway. A Store is not safe for concurrent use.
type Store struct {
	genesisHash []byte
	log         *Log
}

// NewStore pins the out-of-band genesis hash. The store is empty until Ingest.
func NewStore(genesisHash []byte) *Store {
	return &Store{genesisHash: append([]byte(nil), genesisHash...)}
}

// Ingest decodes, verifies, and adopts a candidate chain. It adopts a linear
// extension, resolves a true fork via forkChoice (fork-point resolution — every
// divergence resolves deterministically), and is a no-op for an identical,
// strict-prefix, or losing candidate. The current state is never rolled back to a
// non-winner.
func (s *Store) Ingest(chainBytes []byte) error {
	entries, err := UnmarshalChain(chainBytes)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("trustlog: empty chain")
	}
	// Cheap genesis-pin check first — reject a wrong-genesis chain before the
	// expensive full-chain signature verification in Load.
	if !bytes.Equal(hashEntry(&entries[0]), s.genesisHash) {
		return errors.New("trustlog: candidate genesis does not match pinned hash")
	}
	cand, err := Load(entries) // verifies signatures, links, signer trust
	if err != nil {
		return err
	}
	if s.log != nil {
		cur := s.log.Entries()
		adopt, err := forkChoice(cur, entries)
		if err != nil {
			return err
		}
		if !adopt {
			return nil // keep current (no-op)
		}
	}
	s.log = cand
	return nil
}

// forkChoice decides whether to adopt cand over the already-verified cur. Both
// slices are fully-verified chains sharing the pinned genesis (cur[0]==cand[0]).
//
// Rules:
//   - Linear: cand prefix-preserves cur and is longer → adopt; identical → no-op;
//     cand is a strict prefix of cur (shorter, no divergence) → keep cur.
//   - True divergence (common prefix p < both lengths): resolved at the FORK POINT,
//     Tailscale tailnet-lock style. The two siblings Ecur = cur[p] and Ecand =
//     cand[p] are the first-diverging entries sharing parent cur[p-1]. Each is
//     weighed ONLY by signers trusted at the fork point (the signer set folded from
//     the shared prefix cur[0..p-1]). Higher weight wins; tie → prefer a removal
//     (revoke-signer / remove-signer); tie → lexicographically-lowest hashEntry of the
//     first-diverging entry. Every divergence resolves deterministically.
//
// Invariant this rule relies on: a KindRevokeSigner is authored so that it IS the
// first-diverging entry — its Prev is the chosen fork point, re-parented before the
// compromised signer's post-fork entries. Weight (its co-sign count) is therefore
// read from cur[p]/cand[p] directly. The Phase 4 co-signing ceremony guarantees this.
//
// Why puppets can't win: signers added AFTER the fork are not in the fork-point set,
// so their co-signs count 0. An attacker must add puppets before they can co-sign, so
// its first-diverging entry is an addSigner (weight 1, not a removal), never the
// high-co-sign revoke — which loses to an honest co-signed revoke at the fork point.
//
// Why order-independent: weight, prefer-removal, and lowest-hash are pure functions
// of the two first-diverging entries and the shared fork-point signer set, none of
// which depend on which branch is "current". A malicious gateway holds no signer keys
// and cannot forge co-signs or a validly-signed divergent branch (Load rejects it),
// so it cannot manufacture a winner.
func forkChoice(cur, cand []Entry) (adoptCandidate bool, err error) {
	p := 0
	for p < len(cur) && p < len(cand) && bytes.Equal(hashEntry(&cur[p]), hashEntry(&cand[p])) {
		p++
	}
	if p == len(cur) {
		// cand extends (or equals) cur — adopt iff strictly longer.
		return len(cand) > p, nil
	}
	if p == len(cand) {
		// cand is a strict prefix of cur (shorter, no divergence) — keep cur.
		return false, nil
	}
	// True divergence: p < len(cur) && p < len(cand). Fold the signer set trusted at
	// the fork point from the shared prefix (cur[0..p-1] == cand[0..p-1]).
	forkSigners, err := foldSignersAt(cur, p)
	if err != nil {
		return false, err
	}
	ecur, ecand := &cur[p], &cand[p]
	wcur := weightAtFork(ecur, forkSigners)
	wcand := weightAtFork(ecand, forkSigners)
	if wcand != wcur {
		return wcand > wcur, nil
	}
	// Tie on weight → prefer a removal (revocation/signer removal is the whole point
	// of a fork). If exactly one sibling is a removal, it wins.
	if rcur, rcand := isRemoval(ecur), isRemoval(ecand); rcur != rcand {
		return rcand, nil
	}
	// Final tie-break: globally-lowest first-diverging-entry hash. Independent of which
	// branch is "current", so both ingest orders converge on the same winner. The
	// hashes cannot be equal here (equal hash ⇒ same entry ⇒ no divergence at p).
	return bytes.Compare(hashEntry(ecand), hashEntry(ecur)) < 0, nil
}

// foldSignersAt replays entries[0..p-1] and returns the trusted signer set at the
// fork point — the shared prefix's folded signers. entries are already Load-verified,
// so apply cannot fail here; the error path is defense-in-depth.
func foldSignersAt(entries []Entry, p int) (map[string]bool, error) {
	l := newEmpty()
	for i := 0; i < p; i++ {
		if err := l.apply(&entries[i]); err != nil {
			return nil, err
		}
	}
	return l.signers, nil
}

// weightAtFork scores a first-diverging entry using ONLY signers trusted at the fork
// point. A co-signed revoke counts its distinct valid co-signers in that set (post-fork
// puppets are absent → 0). Any other entry weighs 1 iff its single signer is trusted
// at the fork point (a Load-verified first-diverging entry's signer normally is).
func weightAtFork(e *Entry, forkSigners map[string]bool) int {
	if e.Kind == KindRevokeSigner {
		// allowRevoked=false: deliberately conservative — the departing signer's
		// co-sign does not inflate fork weight, which is fail-safe.
		n, _ := validCoSigns(e, func(pub []byte) bool { return forkSigners[string(pub)] }, false)
		return n
	}
	if forkSigners[string(e.Signer)] {
		return 1
	}
	return 0
}

// isRemoval reports whether e removes trust (revoke-signer or remove-signer) — the
// preferred sibling on a weight tie.
func isRemoval(e *Entry) bool {
	return e.Kind == KindRevokeSigner || e.Kind == KindRemoveSigner
}

// Bytes serializes the current chain (nil if nothing ingested yet).
func (s *Store) Bytes() []byte {
	if s.log == nil {
		return nil
	}
	return MarshalChain(s.log.Entries())
}

// Tip returns the current chain tip, or nil if empty.
func (s *Store) Tip() []byte {
	if s.log == nil {
		return nil
	}
	return s.log.Tip()
}

// Disabled reports whether the log has been disabled by a valid disablement secret.
func (s *Store) Disabled() bool { return s.log != nil && s.log.Disabled() }

// DeviceAuthorized reports whether pub is authorized by the current chain.
func (s *Store) DeviceAuthorized(pub []byte) bool {
	return s.log != nil && s.log.DeviceAuthorized(pub)
}

// SignerTrusted reports whether pub is a trusted signer in the current chain.
func (s *Store) SignerTrusted(pub []byte) bool {
	return s.log != nil && s.log.SignerTrusted(pub)
}

// Signers returns the current trusted signer set (empty if nothing ingested).
func (s *Store) Signers() [][]byte {
	if s.log == nil {
		return [][]byte{}
	}
	return s.log.Signers()
}

// Devices returns the current authorized device set (empty if nothing ingested).
func (s *Store) Devices() [][]byte {
	if s.log == nil {
		return [][]byte{}
	}
	return s.log.Devices()
}
