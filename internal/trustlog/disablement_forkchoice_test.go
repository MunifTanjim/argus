package trustlog

import (
	"bytes"
	"testing"
)

// TestForkDisabledChainDominates verifies that a Load-verified disabled chain
// (break-glass) dominates fork-choice over any non-disabled competitor sharing the
// genesis — regardless of the competitor's signer weight. A disable is authorized
// by a secret preimage committed in the genesis and is terminal; without fork-choice
// dominance an attacker holding a trusted signer key could fork before the disable
// and roll it back (argus persists the chain rather than purging on disable, so the
// "off" state is a chain fact that must not be out-competed).
//
// The disable here is signed by a NON-signer keypair, so its fork weight is 0 and
// the attacker's addSigner (weight 1) would win under the plain weight rule — proving
// dominance, not weight, is what protects the disablement.
func TestForkDisabledChainDominates(t *testing.T) {
	a := mustGenSigner(t)
	secret, err := GenerateDisablementSecret()
	mustNoErr(t, err)
	commit := DisablementCommitment(secret)

	ld, err := NewGenesis([][]byte{a.Public}, a, [][]byte{commit})
	mustNoErr(t, err)
	lp, err := NewGenesis([][]byte{a.Public}, a, [][]byte{commit})
	mustNoErr(t, err)
	if !bytes.Equal(ld.Tip(), lp.Tip()) {
		t.Fatal("genesis must be deterministic for both branches")
	}
	gh := ld.Tip()

	nonSigner := mustGenSigner(t)
	mustNoErr(t, ld.Disable(secret, nonSigner))
	disabled := MarshalChain(ld.Entries())

	b := mustGenSigner(t)
	mustNoErr(t, lp.AddSigner(b.Public, a))
	attacker := MarshalChain(lp.Entries())

	s1 := NewStore(gh)
	mustIngest(t, s1, attacker)
	mustIngest(t, s1, disabled)
	if !s1.Disabled() {
		t.Fatal("a disabled chain must dominate fork-choice over a non-disabled branch (adopt)")
	}

	s2 := NewStore(gh)
	mustIngest(t, s2, disabled)
	if err := s2.Ingest(attacker); err != nil {
		t.Fatalf("ingesting a non-disabled branch over a disabled one must be a no-op, not error: %v", err)
	}
	if !s2.Disabled() {
		t.Fatal("a non-disabled branch must not roll back a disablement (disable dominates)")
	}
}
