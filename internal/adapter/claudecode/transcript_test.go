package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTranscriptView_LazyTopLevel(t *testing.T) {
	root, _ := writeNestedStreamFixture(t)
	v, err := ReadTranscriptView(root)
	if err != nil {
		t.Fatal(err)
	}
	it, ok := subagentItem(v.Chunks)
	if !ok {
		t.Fatal("no subagent item at top level")
	}
	sa := it.Subagents[0]
	if sa.ID != "A" || !sa.HasTrace {
		t.Fatalf("top item ID=%q HasTrace=%v, want A/true", sa.ID, sa.HasTrace)
	}
	if len(sa.Trace) != 0 {
		t.Fatalf("Trace should not be inlined, got %d chunks", len(sa.Trace))
	}
}

func TestReadSubagentView_NestedDrillable(t *testing.T) {
	root, _ := writeNestedStreamFixture(t)
	v, ok, err := ReadSubagentView(root, "A")
	if err != nil || !ok {
		t.Fatalf("ReadSubagentView(A) ok=%v err=%v", ok, err)
	}
	it, found := subagentItem(v.Chunks)
	if !found {
		t.Fatal("no nested subagent item in A's view")
	}
	sa := it.Subagents[0]
	if sa.ID != "B" || !sa.HasTrace {
		t.Fatalf("nested item ID=%q HasTrace=%v, want B/true", sa.ID, sa.HasTrace)
	}
}

func TestReadSubagentView_DepthCap(t *testing.T) {
	root, subA := writeNestedStreamFixture(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(subA), "agent-A.meta.json"),
		[]byte(`{"spawnDepth":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	v, _, _ := ReadSubagentView(root, "A")
	it, _ := subagentItem(v.Chunks)
	sa := it.Subagents[0]
	if sa.ID != "" || sa.HasTrace {
		t.Fatalf("capped nested item ID=%q HasTrace=%v, want empty/false", sa.ID, sa.HasTrace)
	}
}

func TestReadSubagentView_Missing(t *testing.T) {
	root, _ := writeNestedStreamFixture(t)
	if _, ok, _ := ReadSubagentView(root, "nope"); ok {
		t.Fatal("ReadSubagentView(nope) ok=true, want false")
	}
}
