package trustlog

import "testing"

func TestFingerprintWordListLen(t *testing.T) {
	if len(fingerprintWordList) != 256 {
		t.Fatalf("word list length = %d, want 256", len(fingerprintWordList))
	}
}

func TestSignerSetFingerprintDeterministicAndOrderIndependent(t *testing.T) {
	a := []byte{1, 2, 3}
	b := []byte{4, 5, 6}
	f1 := SignerSetFingerprint([][]byte{a, b})
	f2 := SignerSetFingerprint([][]byte{b, a}) // reversed order → same (sorted internally)
	if len(f1) != 8 {
		t.Fatalf("want 8 words, got %d", len(f1))
	}
	for i := range f1 {
		if f1[i] != f2[i] {
			t.Fatalf("order-dependence: %v vs %v", f1, f2)
		}
	}
	if SignerSetFingerprint([][]byte{a})[0] == SignerSetFingerprint([][]byte{b})[0] {
		t.Skip("different sets may coincide on word[0]; not asserting inequality")
	}
}
