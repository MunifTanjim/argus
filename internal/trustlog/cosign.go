package trustlog

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
)

// PendingRevoke holds a partial KindRevokeSigner entry assembled across signer nodes
// via copy-paste ceremony. The partial entry carries the fork point (Prev), the
// revoked signer pubkeys (Signers), optional replacement pubkeys (Replaces), and the
// accumulated co-signs (CoSigns). Shuttle it between nodes with Marshal/UnmarshalPendingRevoke.
type PendingRevoke struct {
	partial Entry
}

// ForkPoint returns the fork-point hash (the Prev that the finalized entry will carry).
func (pr PendingRevoke) ForkPoint() []byte { return cloneBytes(pr.partial.Prev) }

// Marshal encodes pr to a bounded wire blob using the existing entry codec. The blob
// is bounded by the same DoS caps as the chain codec (maxCoSigns, maxReplaces, etc.).
func (pr PendingRevoke) Marshal() []byte { return MarshalEntry(pr.partial) }

// UnmarshalPendingRevoke decodes a blob produced by PendingRevoke.Marshal. It rejects
// truncation, oversized counts, wrong kind, or a missing revoked-signer list.
func UnmarshalPendingRevoke(b []byte) (PendingRevoke, error) {
	e, err := UnmarshalEntry(b)
	if err != nil {
		return PendingRevoke{}, fmt.Errorf("trustlog: pending revoke: %w", err)
	}
	if e.Kind != KindRevokeSigner {
		return PendingRevoke{}, errors.New("trustlog: pending revoke: blob has wrong entry kind")
	}
	if len(e.Signers) == 0 {
		return PendingRevoke{}, errors.New("trustlog: pending revoke: no revoked signers in blob")
	}
	return PendingRevoke{partial: e}, nil
}

// selectForkPoint returns the fork-point hash for the ceremony.
// Default: Prev of the first non-genesis entry in log whose Signer is in the revoked
// set — re-parenting the revocation before the compromised signer's earliest action
// so those actions are erased by the fork. Falls back to the current tip if no such
// entry exists (revoked signer never signed anything).
func selectForkPoint(log *Log, revoked [][]byte) []byte {
	revokedSet := map[string]bool{}
	for _, r := range revoked {
		revokedSet[string(r)] = true
	}
	for _, e := range log.Entries() {
		if e.Kind == KindGenesis {
			continue // genesis is immutable; cannot fork before it
		}
		if revokedSet[string(e.Signer)] {
			return cloneBytes(e.Prev)
		}
	}
	return log.Tip()
}

// forkSignersForPR computes the trusted signer set at the fork point. It scans the
// log's entries for the entry whose hash equals pr.partial.Prev (the fork-point hash)
// and replays the chain up to and including that entry — mirroring what foldSignersAt
// and forkChoice use so that co-sign validity is consistent with apply.
func forkSignersForPR(pr PendingRevoke, log *Log) (map[string]bool, error) {
	if pr.partial.Prev == nil {
		return nil, errors.New("trustlog: pending revoke has nil fork point")
	}
	entries := log.Entries()
	for i := range entries {
		if bytes.Equal(hashEntry(&entries[i]), pr.partial.Prev) {
			return foldSignersAt(entries, i+1)
		}
	}
	return nil, errors.New("trustlog: fork point not found in log")
}

// StartRevoke begins a co-signing ceremony that will produce a KindRevokeSigner entry.
//
//   - revoked: pubkeys of the signers to be revoked (must be non-empty).
//   - replaces: optional pubkeys atomically added as replacement signers.
//   - forkFrom: overrides the fork-point hash; nil = use the default (Prev of the
//     earliest non-genesis entry signed by any revoked signer; or the current tip).
//   - by: the initiating signer — must be trusted at the fork point.
//
// The returned PendingRevoke carries one co-sign. Pass it to AddCoSign on other nodes
// until Complete is satisfied, then call Finalize.
func StartRevoke(log *Log, revoked, replaces [][]byte, forkFrom []byte, by SignerKey) (PendingRevoke, error) {
	if len(revoked) == 0 {
		return PendingRevoke{}, errors.New("trustlog: start revoke: no signers to revoke")
	}

	var forkPoint []byte
	if forkFrom != nil {
		forkPoint = cloneBytes(forkFrom)
	} else {
		forkPoint = selectForkPoint(log, revoked)
	}

	pr := PendingRevoke{
		partial: Entry{
			Kind:     KindRevokeSigner,
			Prev:     forkPoint,
			Signers:  cloneSigners(revoked),
			Replaces: cloneSigners(replaces),
		},
	}

	forkSigners, err := forkSignersForPR(pr, log)
	if err != nil {
		return PendingRevoke{}, fmt.Errorf("trustlog: start revoke: %w", err)
	}
	if !forkSigners[string(by.Public)] {
		return PendingRevoke{}, errors.New("trustlog: start revoke: initiator not trusted at fork point")
	}

	sb := sigBytes(&pr.partial)
	pr.partial.CoSigns = []CoSign{{
		Signer: cloneBytes(by.Public),
		Sig:    ed25519.Sign(by.Private, sb),
	}}
	return pr, nil
}

// AddCoSign appends by's co-sign to pr after verifying the partial entry against the
// fork-point signer set. It rejects: by not trusted at the fork point; by already
// present in CoSigns; by in the revoked set without Replaces (a departing signer may
// only co-sign for voluntary succession); any existing co-sign that fails signature
// verification (tamper detection). Returns a new PendingRevoke; pr is unchanged.
func AddCoSign(pr PendingRevoke, log *Log, by SignerKey) (PendingRevoke, error) {
	forkSigners, err := forkSignersForPR(pr, log)
	if err != nil {
		return pr, fmt.Errorf("trustlog: add co-sign: %w", err)
	}
	if !forkSigners[string(by.Public)] {
		return pr, errors.New("trustlog: add co-sign: signer not trusted at fork point")
	}
	for _, cs := range pr.partial.CoSigns {
		if bytes.Equal(cs.Signer, by.Public) {
			return pr, errors.New("trustlog: add co-sign: signer already co-signed")
		}
	}
	// A departing signer may only co-sign when Replaces is set (voluntary succession).
	// Without Replaces, a revoked signer co-signing would let a compromised key influence
	// its own expulsion — reject it explicitly regardless of trust at the fork point.
	for _, r := range pr.partial.Signers {
		if bytes.Equal(r, by.Public) {
			if len(pr.partial.Replaces) == 0 {
				return pr, errors.New("trustlog: add co-sign: revoked signer may not co-sign without a replacement (voluntary succession requires Replaces)")
			}
			// Replaces is set: voluntary succession — the departing signer blesses its own
			// replacement, which validCoSigns(allowRevoked=true) intentionally counts.
			break
		}
	}
	// Verify existing co-signs over the partial entry's sigBytes to detect tampering.
	sb := sigBytes(&pr.partial)
	for _, cs := range pr.partial.CoSigns {
		if len(cs.Signer) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(cs.Signer), sb, cs.Sig) {
			return pr, errors.New("trustlog: add co-sign: existing co-sign is invalid")
		}
	}
	result := PendingRevoke{partial: cloneEntry(pr.partial)}
	result.partial.CoSigns = append(result.partial.CoSigns, CoSign{
		Signer: cloneBytes(by.Public),
		Sig:    ed25519.Sign(by.Private, sb),
	})
	return result, nil
}

// Complete reports whether pr has reached the co-sign threshold required for
// finalization. Two conditions must both hold:
//  1. Distinct valid co-signs from fork-point signers (excluding revoked signers, unless
//     Replaces is set — mirroring what apply enforces) > len(revoked).
//  2. Post-apply signer count ≥ 1 (same guard apply uses).
func Complete(pr PendingRevoke, log *Log) bool {
	forkSigners, err := forkSignersForPR(pr, log)
	if err != nil {
		return false
	}
	trusted := func(p []byte) bool { return forkSigners[string(p)] }
	if _, ok := validCoSigns(&pr.partial, trusted, len(pr.partial.Replaces) > 0); !ok {
		return false
	}
	// Simulate post-apply signer set: start from fork-point signers, add replacements,
	// remove revoked. Mirrors the logic in apply(KindRevokeSigner).
	sim := map[string]bool{}
	for k := range forkSigners {
		sim[k] = true
	}
	for _, r := range pr.partial.Replaces {
		sim[string(r)] = true
	}
	return remainingAfterRevoke(sim, pr.partial.Signers) >= 1
}

// Finalize returns the complete KindRevokeSigner entry ready to be appended to the chain.
// It errors if the pending revoke is not complete-shaped (missing required fields).
// Callers should call Complete first; Finalize does not re-check the co-sign quorum.
func Finalize(pr PendingRevoke) (Entry, error) {
	if pr.partial.Kind != KindRevokeSigner {
		return Entry{}, errors.New("trustlog: finalize: wrong entry kind")
	}
	if pr.partial.Prev == nil {
		return Entry{}, errors.New("trustlog: finalize: missing fork point (Prev)")
	}
	if len(pr.partial.Signers) == 0 {
		return Entry{}, errors.New("trustlog: finalize: no revoked signers")
	}
	if len(pr.partial.CoSigns) == 0 {
		return Entry{}, errors.New("trustlog: finalize: no co-signs")
	}
	return cloneEntry(pr.partial), nil
}

// BuildRevokeChain constructs the chain bytes ready for Store.Ingest from a
// completed PendingRevoke and the node's current chain entries. It scans entries
// for the fork-point entry (the entry whose hash equals pr.ForkPoint()), takes all
// entries up to and including that point, appends the finalized revoke entry, and
// marshals to wire bytes.
//
// Callers MUST call Complete before BuildRevokeChain; quorum is not re-checked here.
func BuildRevokeChain(pr PendingRevoke, entries []Entry) ([]byte, error) {
	finalEntry, err := Finalize(pr)
	if err != nil {
		return nil, err
	}
	forkPoint := pr.ForkPoint()
	var forkEntries []Entry
	found := false
	for _, e := range entries {
		forkEntries = append(forkEntries, e)
		if bytes.Equal(hashEntry(&e), forkPoint) {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("trustlog: build revoke chain: fork point not found in provided entries")
	}
	forkEntries = append(forkEntries, finalEntry)
	return MarshalChain(forkEntries), nil
}
