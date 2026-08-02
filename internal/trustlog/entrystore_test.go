package trustlog

import (
	"bytes"
	"testing"
)

func twoEntryChainES(t *testing.T) (short, long []byte) {
	t.Helper()
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	short = MarshalChain(log.Entries())
	dev := make([]byte, 32)
	if err := log.AuthorizeDevice(dev, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	long = MarshalChain(log.Entries())
	return short, long
}

func divergentForksES(t *testing.T) (chainA, chainB []byte) {
	t.Helper()
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	genLog, err := NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisEntries := genLog.Entries()

	devA := bytes.Repeat([]byte{0xAA}, 32)
	logA, err := Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load fork A: %v", err)
	}
	if err := logA.AuthorizeDevice(devA, signer); err != nil {
		t.Fatalf("AuthorizeDevice A: %v", err)
	}
	chainA = MarshalChain(logA.Entries())

	devB := bytes.Repeat([]byte{0xBB}, 32)
	logB, err := Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load fork B: %v", err)
	}
	if err := logB.AuthorizeDevice(devB, signer); err != nil {
		t.Fatalf("AuthorizeDevice B: %v", err)
	}
	chainB = MarshalChain(logB.Entries())

	return chainA, chainB
}

func testChainPairES(t *testing.T) (short, extended []byte) {
	t.Helper()
	return twoEntryChainES(t)
}

func testChainES(t *testing.T) []byte {
	t.Helper()
	_, extended := testChainPairES(t)
	return extended
}

func entriesOfES(t *testing.T, chain []byte) [][]byte {
	t.Helper()
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	return raw
}

func TestEntryStoreDedupesByHash(t *testing.T) {
	s := NewEntryStore()
	raw := entriesOfES(t, testChainES(t))

	if got := s.PutAll(raw); got != len(raw) {
		t.Fatalf("first PutAll inserted %d, want %d", got, len(raw))
	}
	if got := s.PutAll(raw); got != 0 {
		t.Fatalf("re-offering the same entries inserted %d, want 0", got)
	}
}

func TestEntryStoreAppendDoesNotCreateASecondHead(t *testing.T) {
	s := NewEntryStore()
	short, extended := testChainPairES(t)
	s.PutAll(entriesOfES(t, short))
	s.PutAll(entriesOfES(t, extended))

	if got := len(s.Heads()); got != 1 {
		t.Fatalf("appending produced %d heads, want 1 — the old head must become an ancestor", got)
	}
}

func TestEntryStoreTwoBranchesShareGenesis(t *testing.T) {
	chainA, chainB := divergentForksES(t)
	rawA := entriesOfES(t, chainA)
	rawB := entriesOfES(t, chainB)

	s := NewEntryStore()
	s.PutAll(rawA)
	s.PutAll(rawB)

	heads := s.Heads()
	if len(heads) != 2 {
		t.Fatalf("two competing forks must produce exactly 2 heads, got %d", len(heads))
	}

	entries, want := s.Delta(nil)
	if len(want) != 0 {
		t.Fatalf("want should be empty, got %d", len(want))
	}
	wantCount := len(rawA) + len(rawB) - 1 // genesis counted once
	if len(entries) != wantCount {
		t.Fatalf("Delta(nil) must return all %d unique entries across both forks, got %d", wantCount, len(entries))
	}
}

func TestEntryStoreCeilingStopsRunawayGrowth(t *testing.T) {
	s := NewEntryStore()
	raw := entriesOfES(t, testChainES(t))
	s.count = maxRetainedEntries
	if s.Put(raw[0]) {
		t.Fatalf("insert past the ceiling must be refused")
	}
}

func TestEntryStoreDeltaWithholdsWhatTheCallerCanReach(t *testing.T) {
	s := NewEntryStore()
	raw := entriesOfES(t, testChainES(t))
	s.PutAll(raw)

	entries, want := s.Delta(s.Heads())
	if len(entries) != 0 {
		t.Fatalf("a caller holding the head must receive nothing, got %d entries", len(entries))
	}
	if len(want) != 0 {
		t.Fatalf("want should be empty, got %d", len(want))
	}

	entries, want = s.Delta(nil)
	if len(entries) != len(raw) {
		t.Fatalf("a caller with no heads must receive everything: got %d, want %d", len(entries), len(raw))
	}
	if len(want) != 0 {
		t.Fatalf("want should be empty, got %d", len(want))
	}
}

func TestEntryStoreDeltaReportsHeadsItDoesNotHold(t *testing.T) {
	s := NewEntryStore()
	s.PutAll(entriesOfES(t, testChainES(t)))

	unknown := []byte("0123456789abcdef0123456789abcdef")
	_, want := s.Delta([][]byte{unknown})
	if len(want) != 1 || !bytes.Equal(want[0], unknown) {
		t.Fatalf("unknown head must come back in want, got %v", want)
	}
}

func TestEntryStoreDeltaOrdersParentsBeforeChildren(t *testing.T) {
	s := NewEntryStore()
	raw := entriesOfES(t, testChainES(t))
	s.PutAll(raw)

	entries, _ := s.Delta(nil)
	seen := map[string]bool{}
	for _, r := range entries {
		e, err := UnmarshalEntry(r)
		if err != nil {
			t.Fatalf("UnmarshalEntry: %v", err)
		}
		if len(e.Prev) > 0 && !seen[string(e.Prev)] {
			t.Fatalf("child emitted before its parent")
		}
		seen[string(HashEntry(&e))] = true
	}
}

func TestEntryStoreRetainsAnOrphan(t *testing.T) {
	s := NewEntryStore()
	raw := entriesOfES(t, testChainES(t))
	if len(raw) < 2 {
		t.Skip("need a multi-entry chain")
	}

	s.PutAll(raw[1:]) // no genesis
	entries, _ := s.Delta(nil)
	if len(entries) != len(raw)-1 {
		t.Fatalf("orphans must be retained and served: got %d, want %d", len(entries), len(raw)-1)
	}
}

func TestEntryStoreRejectsUndecodableEntries(t *testing.T) {
	s := NewEntryStore()
	if s.Put([]byte("garbage")) {
		t.Fatalf("undecodable entry must not be stored")
	}
}
