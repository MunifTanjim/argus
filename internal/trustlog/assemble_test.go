package trustlog

import (
	"bytes"
	"testing"
)

// chainOf builds a valid chain of n entries using the package's existing test
// helpers, returning the marshalled chain and its decoded entries.
func chainOf(t *testing.T, n int) ([]byte, []Entry) {
	t.Helper()
	_, short, extended, _ := buildStoreChain(t)
	chain := short
	if n > 1 {
		chain = extended
	}
	entries, err := UnmarshalChain(chain)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	return chain, entries
}

func TestChainEntriesRoundTripsThroughAssemble(t *testing.T) {
	chain, entries := chainOf(t, 2)

	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	if len(raw) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(raw), len(entries))
	}

	got := AssembleChains(raw)
	if len(got) != 1 {
		t.Fatalf("got %d chains, want 1", len(got))
	}
	if !bytes.Equal(got[0], chain) {
		t.Fatalf("round trip changed the chain bytes")
	}
}

func TestChainEntriesRejectsGarbage(t *testing.T) {
	if _, err := ChainEntries([]byte("not a chain")); err == nil {
		t.Fatalf("expected an error for garbage input")
	}
}

func TestAssembleChainsSkipsBranchMissingAnAncestor(t *testing.T) {
	chain, _ := chainOf(t, 2)
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	if len(raw) < 2 {
		t.Fatalf("need at least 2 entries, got %d", len(raw))
	}

	// Drop the genesis: the remaining head can no longer reach a nil-Prev root.
	orphaned := raw[1:]
	if got := AssembleChains(orphaned); len(got) != 0 {
		t.Fatalf("incomplete branch must be skipped, got %d chains", len(got))
	}
}

func TestAssembleChainsDedupesRepeatedEntries(t *testing.T) {
	chain, _ := chainOf(t, 2)
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	got := AssembleChains(append(append([][]byte{}, raw...), raw...))
	if len(got) != 1 {
		t.Fatalf("duplicate entries must collapse to one chain, got %d", len(got))
	}
	if !bytes.Equal(got[0], chain) {
		t.Fatalf("dedupe changed the chain bytes")
	}
}

func TestAssembleChainsIgnoresUndecodableEntries(t *testing.T) {
	chain, _ := chainOf(t, 2)
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	got := AssembleChains(append(append([][]byte{}, raw...), []byte("garbage")))
	if len(got) != 1 {
		t.Fatalf("garbage must be ignored, got %d chains", len(got))
	}
	if !bytes.Equal(got[0], chain) {
		t.Fatalf("garbage changed the chain bytes")
	}
}

func TestAssembleChainsReportCompletChainIsFullyPlaced(t *testing.T) {
	chain, _ := chainOf(t, 2)
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	chains, unplaced := AssembleChainsReport(raw)
	if unplaced != 0 {
		t.Fatalf("unplaced = %d, want 0 for a complete chain", unplaced)
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], chain) {
		t.Fatalf("AssembleChainsReport returned wrong chain")
	}
}

func TestAssembleChainsReportCountsOrphanedEntries(t *testing.T) {
	chain, _ := chainOf(t, 2)
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	if len(raw) < 2 {
		t.Fatalf("need at least 2 entries, got %d", len(raw))
	}

	// Drop the genesis: all remaining entries are orphaned (cannot walk to nil-Prev).
	orphaned := raw[1:]
	chains, unplaced := AssembleChainsReport(orphaned)
	if len(chains) != 0 {
		t.Fatalf("incomplete branch must yield no chains, got %d", len(chains))
	}
	if unplaced != len(orphaned) {
		t.Fatalf("unplaced = %d, want %d (one per orphaned entry)", unplaced, len(orphaned))
	}
}

func TestAssembleChainsReportIgnoresGarbageInUnplacedCount(t *testing.T) {
	chain, _ := chainOf(t, 2)
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	// Garbage entry must not increment unplaced — it is not a decodable entry.
	chains, unplaced := AssembleChainsReport(append(append([][]byte{}, raw...), []byte("garbage")))
	if unplaced != 0 {
		t.Fatalf("unplaced = %d, want 0; garbage must not count as unplaced", unplaced)
	}
	if len(chains) != 1 {
		t.Fatalf("got %d chains, want 1", len(chains))
	}
}

func TestAssembleChainsReportDuplicateOrphanCountsOnce(t *testing.T) {
	chain, _ := chainOf(t, 2)
	raw, err := ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	if len(raw) < 2 {
		t.Fatalf("need at least 2 entries, got %d", len(raw))
	}

	// Drop genesis → orphan; supply it twice.
	orphan := raw[1]
	_, unplaced := AssembleChainsReport([][]byte{orphan, orphan})
	if unplaced != 1 {
		t.Fatalf("unplaced = %d, want 1; duplicated orphan must count once", unplaced)
	}
}
