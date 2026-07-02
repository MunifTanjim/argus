package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestReadTranscriptViewStampsSubagent(t *testing.T) {
	view, err := ReadTranscriptView("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	var sub *transcript.Item
	for i := range view.Chunks {
		for j := range view.Chunks[i].Items {
			if view.Chunks[i].Items[j].ToolName == "spawn_agent" {
				sub = &view.Chunks[i].Items[j]
			}
		}
	}
	if sub == nil {
		t.Fatal("no spawn_agent item")
	}
	if len(sub.Subagents) == 0 || sub.Subagents[0].ID == "" {
		t.Fatal("subagent id not linked")
	}
}

func TestStreamingRefreshReturnsFullList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")

	lines := `{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewStreamingTranscript(path, "", false)

	chunks1, err := st.Refresh()
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if len(chunks1) == 0 {
		t.Fatal("first Refresh returned no chunks")
	}

	// Second call with no file change must return the full list, not a delta.
	chunks2, err := st.Refresh()
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if len(chunks2) != len(chunks1) {
		t.Fatalf("second Refresh returned %d chunks, want %d (full list)", len(chunks2), len(chunks1))
	}
}

func TestFindToolDetail(t *testing.T) {
	view, err := ReadTranscriptView("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var toolID string
	for _, c := range view.Chunks {
		for _, it := range c.Items {
			if it.ToolName == "update_plan" {
				toolID = it.ToolID
			}
		}
	}
	if toolID == "" {
		t.Fatal("no update_plan tool id")
	}
	det, ok, err := FindToolDetail("testdata/rollout-parent.jsonl", "", toolID)
	if err != nil || !ok {
		t.Fatalf("FindToolDetail ok=%v err=%v", ok, err)
	}
	if det.Result != "Plan updated" {
		t.Fatalf("result = %q, want 'Plan updated'", det.Result)
	}
}
