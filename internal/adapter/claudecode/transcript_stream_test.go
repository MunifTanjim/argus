package claudecode

import (
	"testing"
)

func TestReadStreamingViewOmitsTraceSetsHasTrace(t *testing.T) {
	sessionPath := writeSession(t) // reuse existing helper from chunk_test.go
	chunks, err := ReadStreamingView(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	var sub *Item
	for i := range chunks {
		for j := range chunks[i].Items {
			if chunks[i].Items[j].Kind == ItemSubagent {
				sub = &chunks[i].Items[j]
			}
		}
	}
	if sub == nil {
		t.Fatal("no subagent item found")
	}
	sa := sub.Subagents[0]
	if sa.Trace != nil {
		t.Errorf("streaming view must not inline Trace, got %d chunks", len(sa.Trace))
	}
	if sa.ID == "" || !sa.HasTrace {
		t.Errorf("subagent item must carry ID + HasTrace, got ID=%q HasTrace=%v", sa.ID, sa.HasTrace)
	}
}
