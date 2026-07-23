package trustlog

import (
	"bytes"
	"testing"
)

func sampleChain(t *testing.T) []Entry {
	t.Helper()
	s1, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s1.Public}, s1, nil)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	s2, _ := GenerateSigner()
	if err := l.AddSigner(s2.Public, s1); err != nil {
		t.Fatalf("add signer: %v", err)
	}
	if err := l.AuthorizeDevice([]byte("device-key-1"), s2); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	return l.Entries()
}

func TestMarshalUnmarshalEntryRoundTrips(t *testing.T) {
	entries := sampleChain(t)
	for i, e := range entries {
		b := MarshalEntry(e)
		got, err := UnmarshalEntry(b)
		if err != nil {
			t.Fatalf("entry %d unmarshal: %v", i, err)
		}
		// Re-marshal must be byte-identical, and the chain hash must be preserved.
		if !bytes.Equal(MarshalEntry(got), b) {
			t.Errorf("entry %d did not round-trip byte-identically", i)
		}
		if !bytes.Equal(hashEntry(&got), hashEntry(&e)) {
			t.Errorf("entry %d hash changed across codec", i)
		}
	}
}

func TestMarshalEntryEqualsFullBytes(t *testing.T) {
	entries := sampleChain(t)
	e := entries[0]
	// MarshalEntry must equal the internal fullBytes so hashing is consistent.
	sum1 := hashEntry(&e)
	got, _ := UnmarshalEntry(MarshalEntry(e))
	if !bytes.Equal(hashEntry(&got), sum1) {
		t.Error("MarshalEntry/UnmarshalEntry breaks hash consistency")
	}
}

func TestChainRoundTrips(t *testing.T) {
	entries := sampleChain(t)
	b := MarshalChain(entries)
	got, err := UnmarshalChain(b)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("len = %d, want %d", len(got), len(entries))
	}
	// A decoded chain must Load cleanly and reproduce the same head.
	l, err := Load(got)
	if err != nil {
		t.Fatalf("Load(decoded): %v", err)
	}
	orig, _ := Load(entries)
	if !bytes.Equal(l.Tip(), orig.Tip()) {
		t.Error("decoded chain head differs from original")
	}
}

func TestUnmarshalRejectsGarbageAndTruncation(t *testing.T) {
	if _, err := UnmarshalEntry([]byte{0x01, 0x02}); err == nil {
		t.Error("UnmarshalEntry must reject truncated input")
	}
	if _, err := UnmarshalChain([]byte{0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Error("UnmarshalChain must reject a bogus/oversized count")
	}
	// Trailing bytes after a valid entry must be rejected.
	entries := sampleChain(t)
	b := append(MarshalEntry(entries[0]), 0x00)
	if _, err := UnmarshalEntry(b); err == nil {
		t.Error("UnmarshalEntry must reject trailing bytes")
	}
}

func TestUnmarshalRejectsOversizedLengthPrefix(t *testing.T) {
	// A field length prefix claiming a huge size must error, not attempt a huge alloc.
	// Kind byte (1) + Prev length prefix = 0xFFFFFFFF, then nothing.
	b := []byte{byte(KindGenesis), 0xff, 0xff, 0xff, 0xff}
	if _, err := UnmarshalEntry(b); err == nil {
		t.Error("UnmarshalEntry must reject an oversized length prefix")
	}
}

func TestUnmarshalChainRejectsTrailingBytes(t *testing.T) {
	entries := sampleChain(t)
	b := append(MarshalChain(entries), 0x00)
	if _, err := UnmarshalChain(b); err == nil {
		t.Error("UnmarshalChain must reject trailing bytes after the chain")
	}
}

// TestChainRoundTripWithDisablement builds a genesis with a disablement commitment,
// appends a KindDisable entry, marshals→unmarshals→Load, and asserts the reloaded
// log reports Disabled()=true and reproduces the original head (decode→hash stable).
func TestChainRoundTripWithDisablement(t *testing.T) {
	s, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	secret, err := GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("GenerateDisablementSecret: %v", err)
	}
	commitment := DisablementCommitment(secret)
	l, err := NewGenesis([][]byte{s.Public}, s, [][]byte{commitment})
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	if err := l.Disable(secret, s); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	originalHead := l.Tip()

	wire := MarshalChain(l.Entries())
	decoded, err := UnmarshalChain(wire)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	reloaded, err := Load(decoded)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reloaded.Disabled() {
		t.Error("reloaded log should report Disabled()=true")
	}
	if !bytes.Equal(reloaded.Tip(), originalHead) {
		t.Error("reloaded log head differs from original (decode→hash not stable)")
	}
}
