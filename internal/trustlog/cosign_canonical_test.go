package trustlog

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

// TestCoSignsCanonicalOrderStableHash verifies that a co-signed revoke entry's
// chain hash and wire encoding are independent of the order the co-signs were
// gathered in. Co-signs are covered by hashEntry (the chain link) but their
// ORDER is committed by nothing — so without canonicalization two honest nodes
// assembling the same co-sign set in different orders compute different Tips
// (spurious fork / fingerprint mismatch), and a relay can reorder co-signs to
// grind the entry hash.
func TestCoSignsCanonicalOrderStableHash(t *testing.T) {
	a, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner a: %v", err)
	}
	b, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner b: %v", err)
	}

	base := Entry{
		Kind:     KindRevokeSigner,
		Prev:     bytes.Repeat([]byte{0x01}, 32),
		Signers:  [][]byte{bytes.Repeat([]byte{0x09}, 32)}, // some revoked signer
		Replaces: [][]byte{bytes.Repeat([]byte{0x0a}, 32)},
	}
	sb := sigBytes(&base)
	csA := CoSign{Signer: a.Public, Sig: ed25519.Sign(a.Private, sb)}
	csB := CoSign{Signer: b.Public, Sig: ed25519.Sign(b.Private, sb)}

	e1 := base
	e1.CoSigns = []CoSign{csA, csB}
	e2 := base
	e2.CoSigns = []CoSign{csB, csA} // same set, reversed order

	if !bytes.Equal(hashEntry(&e1), hashEntry(&e2)) {
		t.Fatalf("hashEntry differs by co-sign order:\n [A,B] = %x\n [B,A] = %x",
			hashEntry(&e1), hashEntry(&e2))
	}
	if !bytes.Equal(MarshalEntry(e1), MarshalEntry(e2)) {
		t.Fatal("MarshalEntry differs by co-sign order")
	}
}
