package trustlog

import (
	"bytes"
	"testing"
)

func TestDisablementCommitmentDeterministicAndOneWay(t *testing.T) {
	s1, err := GenerateDisablementSecret()
	if err != nil || len(s1) != 32 {
		t.Fatalf("GenerateDisablementSecret: len=%d err=%v", len(s1), err)
	}
	c1 := DisablementCommitment(s1)
	if len(c1) != 32 {
		t.Fatalf("commitment len = %d, want 32", len(c1))
	}
	// Deterministic: same secret → same commitment (verification depends on this).
	if !bytes.Equal(c1, DisablementCommitment(s1)) {
		t.Fatal("commitment not deterministic")
	}
	// One-way: commitment != secret.
	if bytes.Equal(c1, s1) {
		t.Fatal("commitment must not equal the secret")
	}
	// Distinct secrets → distinct commitments.
	s2, _ := GenerateDisablementSecret()
	if bytes.Equal(s1, s2) {
		t.Fatal("two generated secrets collided (astronomically unlikely)")
	}
	if bytes.Equal(c1, DisablementCommitment(s2)) {
		t.Fatal("distinct secrets produced the same commitment")
	}
}
