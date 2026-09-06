package trustlog

import (
	"strings"
	"testing"
)

// HashFingerprint encodes the full 32-byte hash as a standard BIP39 mnemonic (24
// words). The all-zero 256-bit vector is the canonical BIP39 test vector.
func TestHashFingerprintBIP39(t *testing.T) {
	got := HashFingerprint(make([]byte, 32))
	if len(got) != 24 {
		t.Fatalf("want 24 BIP39 words, got %d: %v", len(got), got)
	}
	const want = "abandon abandon abandon abandon abandon abandon abandon abandon abandon " +
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon " +
		"abandon abandon abandon abandon abandon art"
	if got := strings.Join(got, " "); got != want {
		t.Fatalf("all-zero fingerprint = %q\n want the standard BIP39 vector", got)
	}
	if HashFingerprint(nil) != nil {
		t.Fatal("nil hash should produce no words")
	}
}

func TestSignerSetFingerprintDeterministicAndOrderIndependent(t *testing.T) {
	a := []byte{1, 2, 3}
	b := []byte{4, 5, 6}
	f1 := SignerSetFingerprint([][]byte{a, b})
	f2 := SignerSetFingerprint([][]byte{b, a}) // reversed order → same (sorted internally)
	if len(f1) != 24 {
		t.Fatalf("want 24 words, got %d", len(f1))
	}
	if strings.Join(f1, " ") != strings.Join(f2, " ") {
		t.Fatalf("order-dependence: %v vs %v", f1, f2)
	}
}
