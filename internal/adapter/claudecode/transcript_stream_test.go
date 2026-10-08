package claudecode

import (
	"testing"
)

func TestReadStreamingViewOmitsTraceSetsHasTrace(t *testing.T) {
	sessionPath := writeSession(t) // reuse existing helper from chunk_test.go
	entries, err := ReadStreamingView(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	var sub *Entry
	for i := range entries {
		if entries[i].Kind == EntrySubagent {
			sub = &entries[i]
		}
	}
	if sub == nil {
		t.Fatal("no subagent entry found")
	}
	sa := sub.Subagents[0]
	if sa.Trace != nil {
		t.Errorf("streaming view must not inline Trace, got %d entries", len(sa.Trace))
	}
	if sa.ID == "" || !sa.HasTrace {
		t.Errorf("subagent item must carry ID + HasTrace, got ID=%q HasTrace=%v", sa.ID, sa.HasTrace)
	}
}
