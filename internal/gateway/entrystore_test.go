package gateway

import (
	"bytes"
	"testing"

	"github.com/MunifTanjim/argus/internal/trustlog"
)

// testChainPair builds a short (genesis-only) chain and an extended chain (same
// genesis plus one more entry). The extended chain is a strict append of the short
// one — they share the same head lineage.
func testChainPair(t *testing.T) (short, extended []byte) {
	t.Helper()
	short, extended = twoEntryChain(t)
	return
}

// testChain returns one valid multi-entry chain.
func testChain(t *testing.T) []byte { t.Helper(); short, extended := testChainPair(t); _ = short; return extended }

// entriesOf builds a valid chain via the package's existing test helper and
// returns its individually marshalled entries.
func entriesOf(t *testing.T, chain []byte) [][]byte {
	t.Helper()
	raw, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	return raw
}

func TestEntryStoreDedupesByHash(t *testing.T) {
	s := &entryStore{}
	raw := entriesOf(t, testChain(t))

	if got := s.putAll(raw); got != len(raw) {
		t.Fatalf("first putAll inserted %d, want %d", got, len(raw))
	}
	if got := s.putAll(raw); got != 0 {
		t.Fatalf("re-offering the same entries inserted %d, want 0", got)
	}
}

func TestEntryStoreHeadIsTheLastEntry(t *testing.T) {
	s := &entryStore{}
	raw := entriesOf(t, testChain(t))
	s.putAll(raw)

	heads := s.heads()
	if len(heads) != 1 {
		t.Fatalf("got %d heads, want 1", len(heads))
	}
	last, err := trustlog.UnmarshalEntry(raw[len(raw)-1])
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}
	if !bytes.Equal(heads[0], trustlog.HashEntry(&last)) {
		t.Fatalf("head is not the last entry")
	}
}

func TestEntryStoreAppendDoesNotCreateASecondHead(t *testing.T) {
	s := &entryStore{}
	short, extended := testChainPair(t)
	s.putAll(entriesOf(t, short))
	s.putAll(entriesOf(t, extended))

	if got := len(s.heads()); got != 1 {
		t.Fatalf("appending produced %d heads, want 1 — the old head must become an ancestor", got)
	}
}

func TestEntryStoreDeltaWithholdsWhatTheCallerCanReach(t *testing.T) {
	s := &entryStore{}
	raw := entriesOf(t, testChain(t))
	s.putAll(raw)

	entries, want := s.delta(s.heads())
	if len(entries) != 0 {
		t.Fatalf("a caller holding the head must receive nothing, got %d entries", len(entries))
	}
	if len(want) != 0 {
		t.Fatalf("want should be empty, got %d", len(want))
	}

	entries, want = s.delta(nil)
	if len(entries) != len(raw) {
		t.Fatalf("a caller with no heads must receive everything: got %d, want %d", len(entries), len(raw))
	}
	if len(want) != 0 {
		t.Fatalf("want should be empty, got %d", len(want))
	}
}

func TestEntryStoreDeltaReportsHeadsItDoesNotHold(t *testing.T) {
	s := &entryStore{}
	s.putAll(entriesOf(t, testChain(t)))

	unknown := []byte("0123456789abcdef0123456789abcdef")
	_, want := s.delta([][]byte{unknown})
	if len(want) != 1 || !bytes.Equal(want[0], unknown) {
		t.Fatalf("unknown head must come back in want, got %v", want)
	}
}

func TestEntryStoreDeltaOrdersParentsBeforeChildren(t *testing.T) {
	s := &entryStore{}
	raw := entriesOf(t, testChain(t))
	s.putAll(raw)

	entries, _ := s.delta(nil)
	seen := map[string]bool{}
	for _, raw := range entries {
		e, err := trustlog.UnmarshalEntry(raw)
		if err != nil {
			t.Fatalf("UnmarshalEntry: %v", err)
		}
		if len(e.Prev) > 0 && !seen[string(e.Prev)] {
			t.Fatalf("child emitted before its parent")
		}
		seen[string(trustlog.HashEntry(&e))] = true
	}
}

func TestEntryStoreRetainsAnOrphan(t *testing.T) {
	s := &entryStore{}
	raw := entriesOf(t, testChain(t))
	if len(raw) < 2 {
		t.Skip("need a multi-entry chain")
	}

	s.putAll(raw[1:]) // no genesis
	entries, _ := s.delta(nil)
	if len(entries) != len(raw)-1 {
		t.Fatalf("orphans must be retained and served: got %d, want %d", len(entries), len(raw)-1)
	}
}

func TestEntryStoreRejectsUndecodableEntries(t *testing.T) {
	s := &entryStore{}
	if s.put([]byte("garbage")) {
		t.Fatalf("undecodable entry must not be stored")
	}
}

func TestEntryStoreCeilingStopsRunawayGrowth(t *testing.T) {
	s := &entryStore{}
	raw := entriesOf(t, testChain(t))
	s.count = maxRetainedEntries
	if s.put(raw[0]) {
		t.Fatalf("insert past the ceiling must be refused")
	}
}

// TestEntryStoreTwoBranchesShareGenesis covers the competing-fork case: build a
// shared genesis and two distinct extensions of it. The store must report exactly
// two heads, and delta(nil) must return every entry from both branches.
func TestEntryStoreTwoBranchesShareGenesis(t *testing.T) {
	chainA, chainB, _, _ := divergentForks(t)
	rawA := entriesOf(t, chainA)
	rawB := entriesOf(t, chainB)

	s := &entryStore{}
	s.putAll(rawA)
	s.putAll(rawB)

	heads := s.heads()
	if len(heads) != 2 {
		t.Fatalf("two competing forks must produce exactly 2 heads, got %d", len(heads))
	}

	// delta(nil) must return every entry from both branches.
	// genesis is shared, so total unique entries = len(rawA) + len(rawB) - 1.
	entries, want := s.delta(nil)
	if len(want) != 0 {
		t.Fatalf("want should be empty, got %d", len(want))
	}
	want1 := len(rawA) + len(rawB) - 1 // genesis counted once
	if len(entries) != want1 {
		t.Fatalf("delta(nil) must return all %d unique entries across both forks, got %d", want1, len(entries))
	}
}
