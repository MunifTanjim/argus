package trustlog

import (
	"bytes"
	"testing"
)

// TestWeightAtForkCountsDepartingCoSignerForSuccession verifies that a voluntary-
// succession revoke (revoke A, replace with C, co-signed by the departing A plus B)
// is scored with the SAME weight it needed for quorum. verify/Complete count the
// departing signer's co-sign (allowRevoked = len(Replaces) > 0), so fork-choice must
// too — otherwise the revoke is undercounted (weight 1 instead of 2) and a competing
// RemoveSigner from a compromised departing key can tie it and win a lowest-hash
// coin-flip, stripping the honest replacement.
func TestWeightAtForkCountsDepartingCoSignerForSuccession(t *testing.T) {
	a, b, c := mustGenSigner(t), mustGenSigner(t), mustGenSigner(t)
	forkSigners := map[string]bool{string(a.Public): true, string(b.Public): true}

	prev := bytes.Repeat([]byte{0x01}, 32)
	// revoke A, replace with C, co-signed by A (departing) and B.
	e := newRevokeSignerEntry(prev, [][]byte{a.Public}, [][]byte{c.Public}, []SignerKey{a, b})

	if w := weightAtFork(&e, forkSigners); w != 2 {
		t.Fatalf("voluntary-succession revoke fork weight = %d, want 2 "+
			"(the departing co-signer counts, matching the quorum rule)", w)
	}
}
