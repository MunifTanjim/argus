package antigravity

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

const sampleTranscript = `{"type":"USER_INPUT","source":"USER_EXPLICIT","content":"<USER_REQUEST>\nlist the files\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-07-05T01:00:00+06:00.\n</ADDITIONAL_METADATA>","step_index":0,"created_at":"2026-07-05T01:00:00Z"}
{"type":"CONVERSATION_HISTORY","source":"SYSTEM","step_index":1}
{"type":"PLANNER_RESPONSE","source":"MODEL","thinking":"I should list the dir.","content":"Listing now.","step_index":2}
{"type":"PLANNER_RESPONSE","source":"MODEL","tool_calls":[{"name":"list_dir","args":{"DirectoryPath":"/x","toolSummary":"List /x"}}],"step_index":3}
{"type":"LIST_DIRECTORY","source":"MODEL","content":"- a.go\n- b.go","step_index":4}
{"type":"PLANNER_RESPONSE","source":"MODEL","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"/x/a.go","toolSummary":"View a.go"}}],"step_index":5}
{"type":"ERROR_MESSAGE","source":"SYSTEM","content":"Error: file not found","step_index":6}
{"type":"SYSTEM_MESSAGE","source":"SYSTEM","content":"scaffolding noise","step_index":7}
{"type":"PLANNER_RESPONSE","source":"MODEL","content":"Done.","step_index":8}
`

func parseSample(t *testing.T) []transcript.Chunk {
	t.Helper()
	chunks, err := parseTranscript(writeLines(t, sampleTranscript))
	if err != nil {
		t.Fatal(err)
	}
	return chunks
}

func TestParseUserChunkStripsWrappers(t *testing.T) {
	c := parseSample(t)
	if len(c) < 1 || c[0].Kind != transcript.ChunkUser {
		t.Fatalf("first chunk should be user, got %+v", c)
	}
	if c[0].Text != "list the files" {
		t.Fatalf("user text = %q; want %q", c[0].Text, "list the files")
	}
}

func TestParseAssistantTurnGrouping(t *testing.T) {
	c := parseSample(t)
	// user chunk + one AI chunk (all MODEL lines fold into one turn).
	if len(c) != 2 {
		t.Fatalf("want 2 chunks, got %d: %+v", len(c), c)
	}
	ai := c[1]
	if ai.Kind != transcript.ChunkAI {
		t.Fatalf("second chunk should be AI, got %s", ai.Kind)
	}
}

func TestParseThinkingTextToolOrder(t *testing.T) {
	ai := parseSample(t)[1]
	kinds := []transcript.ItemKind{}
	for _, it := range ai.Items {
		kinds = append(kinds, it.Kind)
	}
	want := []transcript.ItemKind{transcript.ItemThinking, transcript.ItemText, transcript.ItemTool, transcript.ItemTool, transcript.ItemText}
	if len(kinds) != len(want) {
		t.Fatalf("item kinds = %v; want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("item %d kind = %s; want %s (all: %v)", i, kinds[i], want[i], kinds)
		}
	}
	if ai.Thinking != 1 || ai.ToolCount != 2 {
		t.Fatalf("counts thinking=%d toolCount=%d; want 1,2", ai.Thinking, ai.ToolCount)
	}
}

func TestParseToolResultAdjacency(t *testing.T) {
	ai := parseSample(t)[1]
	var listTool, viewTool transcript.Item
	for _, it := range ai.Items {
		if it.ToolName == "list_dir" {
			listTool = it
		}
		if it.ToolName == "view_file" {
			viewTool = it
		}
	}
	if listTool.Result != "- a.go\n- b.go" {
		t.Fatalf("list_dir result = %q", listTool.Result)
	}
	if listTool.InputPreview != "List /x" {
		t.Fatalf("list_dir preview = %q; want %q", listTool.InputPreview, "List /x")
	}
	if viewTool.Result != "Error: file not found" || !viewTool.ResultIsError {
		t.Fatalf("view_file result=%q err=%v; want error", viewTool.Result, viewTool.ResultIsError)
	}
}

func TestParseToolIDsUnique(t *testing.T) {
	ai := parseSample(t)[1]
	seen := map[string]bool{}
	for _, it := range ai.Items {
		if it.Kind != transcript.ItemTool {
			continue
		}
		if it.ToolID == "" || seen[it.ToolID] {
			t.Fatalf("tool id missing/duplicate: %q (seen %v)", it.ToolID, seen)
		}
		seen[it.ToolID] = true
	}
}
