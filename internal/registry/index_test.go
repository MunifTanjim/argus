package registry

import "testing"

func TestSessionIndexFindSetClear(t *testing.T) {
	x := newSessionIndex()
	if _, ok := x.findByPane("default:%1"); ok {
		t.Fatal("empty index should not find a pane")
	}

	x.setPane("default:%1", "s1")
	x.setClaude("claude-1", "s1")
	if id, ok := x.findByPane("default:%1"); !ok || id != "s1" {
		t.Errorf("findByPane = %q,%v want s1,true", id, ok)
	}
	if id, ok := x.findByClaude("claude-1"); !ok || id != "s1" {
		t.Errorf("findByClaude = %q,%v want s1,true", id, ok)
	}

	// clear drops the session from BOTH indices — the invariant the type exists for.
	x.clear("default:%1", "claude-1")
	if _, ok := x.findByPane("default:%1"); ok {
		t.Error("clear should drop the pane entry")
	}
	if _, ok := x.findByClaude("claude-1"); ok {
		t.Error("clear should drop the claude entry")
	}
}

func TestSessionIndexClearIgnoresEmptyKeys(t *testing.T) {
	x := newSessionIndex()
	x.setPane("default:%1", "s1")
	x.clear("", "") // a session with no claude id / no pane: must be a safe no-op
	if _, ok := x.findByPane("default:%1"); !ok {
		t.Error("clear with empty keys must not touch existing entries")
	}
}
