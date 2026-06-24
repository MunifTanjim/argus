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
	if sub.Trace != nil {
		t.Errorf("streaming view must not inline Trace, got %d chunks", len(sub.Trace))
	}
	if sub.AgentID == "" || !sub.HasTrace {
		t.Errorf("subagent item must carry AgentID + HasTrace, got AgentID=%q HasTrace=%v", sub.AgentID, sub.HasTrace)
	}
}
