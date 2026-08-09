package trustlog

import (
	"bytes"
	"testing"
)

func TestRevokeSignerEntryValidCoSigns(t *testing.T) {
	a, _ := GenerateSigner()
	b, _ := GenerateSigner()
	c, _ := GenerateSigner()
	trusted := map[string]bool{string(a.Public): true, string(b.Public): true, string(c.Public): true}
	at := func(pub []byte) bool { return trusted[string(pub)] }
	revoked := [][]byte{c.Public}
	e := newRevokeSignerEntry([]byte("prevhash-32-bytes-padding-000000"), revoked, nil, []SignerKey{a, b})
	n, ok := validCoSigns(&e, at, false)
	if !ok || n != 2 {
		t.Fatalf("want 2 valid co-signs > 1 revoked, got n=%d ok=%v", n, ok)
	}
}

func TestRevokeSignerInsufficientCoSigns(t *testing.T) {
	a, _ := GenerateSigner()
	c, _ := GenerateSigner()
	at := func(pub []byte) bool { return true }
	e := newRevokeSignerEntry([]byte("prevhash-32-bytes-padding-000000"), [][]byte{c.Public}, nil, []SignerKey{a}) // 1 co-sign, 1 revoked
	if _, ok := validCoSigns(&e, at, false); ok {
		t.Fatal("1 co-sign is not > 1 revoked; must be invalid")
	}
}

func TestRevokeSignerDuplicateAndUntrustedCoSignsDontCount(t *testing.T) {
	a, _ := GenerateSigner()
	c, _ := GenerateSigner()
	onlyA := func(pub []byte) bool { return string(pub) == string(a.Public) }
	// two co-signs but one is duplicate-A and one is untrusted -> only 1 distinct trusted
	e := newRevokeSignerEntry([]byte("prevhash-32-bytes-padding-000000"), [][]byte{c.Public}, nil, []SignerKey{a, a})
	if n, ok := validCoSigns(&e, onlyA, false); ok || n != 1 {
		t.Fatalf("duplicate co-signer must count once and fail threshold; n=%d ok=%v", n, ok)
	}
}

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
