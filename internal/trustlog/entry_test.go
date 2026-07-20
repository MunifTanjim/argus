package trustlog

import (
	"bytes"
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	k, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	e := Entry{Kind: KindAuthorizeDevice, Prev: []byte("prevhash"), Key: []byte("devicepub")}
	sign(&e, k)
	if len(e.Sig) == 0 || !bytes.Equal(e.Signer, k.Public) {
		t.Fatalf("sign did not set Signer/Sig")
	}
	if !verifySig(&e) {
		t.Error("verifySig failed on a freshly signed entry")
	}
}

func TestVerifyRejectsTamper(t *testing.T) {
	k, _ := GenerateSigner()
	e := Entry{Kind: KindAuthorizeDevice, Prev: []byte("p"), Key: []byte("dev1")}
	sign(&e, k)
	e.Key = []byte("dev2") // tamper a signed field
	if verifySig(&e) {
		t.Error("verifySig must fail after the signed content changed")
	}
}

func TestSigBytesDeterministicAndFieldSensitive(t *testing.T) {
	a := Entry{Kind: KindAddSigner, Prev: []byte("h"), Key: []byte("s1"), Signer: []byte("who")}
	b := a
	if !bytes.Equal(sigBytes(&a), sigBytes(&b)) {
		t.Error("sigBytes must be deterministic for equal entries")
	}
	b.Key = []byte("s2")
	if bytes.Equal(sigBytes(&a), sigBytes(&b)) {
		t.Error("sigBytes must differ when a field differs")
	}
	// Sig is excluded from sigBytes (it signs over sigBytes).
	c := a
	c.Sig = []byte("sig")
	if !bytes.Equal(sigBytes(&a), sigBytes(&c)) {
		t.Error("sigBytes must not include Sig")
	}
}

func TestHashChainSensitive(t *testing.T) {
	k, _ := GenerateSigner()
	e := Entry{Kind: KindGenesis, Signers: [][]byte{k.Public}}
	sign(&e, k)
	h1 := hashEntry(&e)
	if len(h1) != 32 {
		t.Fatalf("hash len = %d, want 32", len(h1))
	}
	e2 := e
	e2.Sig = append([]byte(nil), e.Sig...)
	e2.Sig[0] ^= 1 // a different signature must change the chain hash
	if bytes.Equal(hashEntry(&e), hashEntry(&e2)) {
		t.Error("hashEntry must cover Sig (chain commits to the signed entry)")
	}
}
