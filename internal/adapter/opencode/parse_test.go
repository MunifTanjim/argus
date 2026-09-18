package opencode

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestFoldMessages(t *testing.T) {
	items := []ocMessageItem{
		{
			Info: ocMessage{ID: "m1", Role: "user", Time: struct {
				Created   int64 `json:"created"`
				Completed int64 `json:"completed,omitempty"`
			}{Created: 1}},
			Parts: []ocPart{{ID: "p1", Type: "text", Text: "hello"}},
		},
		{
			Info: ocMessage{ID: "m2", Role: "assistant", ModelID: "glm", Time: struct {
				Created   int64 `json:"created"`
				Completed int64 `json:"completed,omitempty"`
			}{Created: 2}},
			Parts: []ocPart{
				{ID: "p2", Type: "reasoning", Text: "thinking"},
				{ID: "p3", Type: "text", Text: "hi there"},
				{ID: "p4", Type: "tool", CallID: "c1", Tool: "bash", State: &ocToolState{
					Status: "completed", Input: json.RawMessage(`{"command":"ls"}`), Output: "file.txt",
				}},
			},
		},
	}
	view := foldMessages(items)
	if len(view.Chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(view.Chunks))
	}
	if view.Chunks[0].Kind != transcript.ChunkUser || view.Chunks[0].Text != "hello" {
		t.Fatalf("user chunk: %+v", view.Chunks[0])
	}
	ai := view.Chunks[1]
	if ai.Kind != transcript.ChunkAI {
		t.Fatalf("ai chunk kind: %q", ai.Kind)
	}
	var kinds []transcript.ItemKind
	for _, it := range ai.Items {
		kinds = append(kinds, it.Kind)
	}
	// expect thinking, text, tool
	if len(kinds) != 3 || kinds[0] != transcript.ItemThinking || kinds[2] != transcript.ItemTool {
		t.Fatalf("item kinds: %v", kinds)
	}
	var tool transcript.Item
	for _, it := range ai.Items {
		if it.Kind == transcript.ItemTool {
			tool = it
		}
	}
	if tool.ToolName != "bash" || tool.ToolID != "c1" || tool.Result != "file.txt" {
		t.Fatalf("tool item: %+v", tool)
	}
}
