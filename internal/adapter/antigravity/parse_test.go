package antigravity

import (
	"reflect"
	"strconv"
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

func parseSample(t *testing.T) []transcript.Entry {
	t.Helper()
	entries, err := parseTranscript(writeLines(t, sampleTranscript), false)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestParseUserEntryStripsWrappers(t *testing.T) {
	c := parseSample(t)
	if len(c) < 1 || c[0].Kind != transcript.EntryUser {
		t.Fatalf("first entry should be user, got %+v", c)
	}
	if c[0].Text != "list the files" {
		t.Fatalf("user text = %q; want %q", c[0].Text, "list the files")
	}
}

func TestParseThinkingTextToolOrder(t *testing.T) {
	es := parseSample(t)
	kinds := []transcript.EntryKind{}
	for _, e := range es {
		kinds = append(kinds, e.Kind)
	}
	// All MODEL lines fold into one turn; no footer until the next USER_INPUT.
	want := []transcript.EntryKind{transcript.EntryUser, transcript.EntryThinking, transcript.EntryText, transcript.EntryTool, transcript.EntryTool, transcript.EntryText}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("entry kinds = %v; want %v", kinds, want)
	}
}

func TestParseTurnFooterCounts(t *testing.T) {
	es := foldTranscript(append(mustScan(t, sampleTranscript), line{Type: "USER_INPUT", Content: "next"}), false)
	end := es[len(es)-2]
	if end.Kind != transcript.EntryTurnEnd || end.Thinking != 1 || end.ToolCount != 2 {
		t.Fatalf("footer = %+v; want thinking=1 toolCount=2", end)
	}
}

func mustScan(t *testing.T, content string) []line {
	t.Helper()
	lines, err := scanTranscript(writeLines(t, content))
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestParseToolResultAdjacency(t *testing.T) {
	var listTool, viewTool transcript.Entry
	for _, it := range parseSample(t) {
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
	seen := map[string]bool{}
	for _, it := range parseSample(t) {
		if it.Kind != transcript.EntryTool {
			continue
		}
		if it.ToolID == "" || seen[it.ToolID] {
			t.Fatalf("tool id missing/duplicate: %q (seen %v)", it.ToolID, seen)
		}
		seen[it.ToolID] = true
	}
}

func TestFoldTranscriptFlatEntries(t *testing.T) {
	es := foldTranscript([]line{
		{Type: "USER_INPUT", Content: "<USER_REQUEST>hi</USER_REQUEST>", CreatedAt: "2026-10-06T00:00:00Z"},
		{Type: "PLANNER_RESPONSE", Thinking: "hmm", Content: "hello", CreatedAt: "2026-10-06T00:00:01Z"},
		{Type: "USER_INPUT", Content: "<USER_REQUEST>more</USER_REQUEST>"},
	}, false)
	var got []transcript.EntryKind
	for _, e := range es {
		got = append(got, e.Kind)
	}
	want := []transcript.EntryKind{transcript.EntryUser, transcript.EntryThinking, transcript.EntryText,
		transcript.EntryTurnEnd, transcript.EntryUser}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i, e := range es {
		if want := "e" + strconv.Itoa(i); e.ID != want {
			t.Errorf("entry %d id = %q, want %q", i, e.ID, want)
		}
	}
}

func TestFoldTranscriptTrailingTurnHasNoFooter(t *testing.T) {
	es := foldTranscript([]line{
		{Type: "USER_INPUT", Content: "hi"},
		{Type: "PLANNER_RESPONSE", Content: "hello"},
	}, false)
	if es[len(es)-1].Kind == transcript.EntryTurnEnd {
		t.Fatalf("trailing turn got a footer")
	}
}

func TestStampTurnModel(t *testing.T) {
	es := []transcript.Entry{{Kind: transcript.EntryText}, {Kind: transcript.EntryTurnEnd}}
	stampTurnModelWith(es, "Gemini 3", "#123456")
	if es[1].ModelName != "Gemini 3" || es[0].ModelName != "" {
		t.Errorf("stamped = %+v", es)
	}
}

func TestFoldTranscriptEmptyTurnHasNoFooter(t *testing.T) {
	es := foldTranscript([]line{
		{Type: "USER_INPUT", Content: "hi"},
		{Type: "PLANNER_RESPONSE"},
		{Type: "USER_INPUT", Content: "next"},
	}, false)
	for _, e := range es {
		if e.Kind == transcript.EntryTurnEnd {
			t.Fatalf("empty turn got a footer: %+v", es)
		}
	}
}

func TestFoldTranscriptFooterKeepsTimestamp(t *testing.T) {
	es := foldTranscript([]line{
		{Type: "USER_INPUT", Content: "hi"},
		{Type: "PLANNER_RESPONSE", Content: "a", CreatedAt: "2026-10-06T00:00:01Z"},
		{Type: "PLANNER_RESPONSE", Content: "b"},
		{Type: "USER_INPUT", Content: "next"},
	}, false)
	if got := es[3].Timestamp; es[3].Kind != transcript.EntryTurnEnd || got != "2026-10-06T00:00:01Z" {
		t.Fatalf("footer = %+v", es[3])
	}
}

func TestFoldTranscriptFinishedClosesFinalTurn(t *testing.T) {
	es := foldTranscript(mustScan(t, sampleTranscript), true)
	end := es[len(es)-1]
	if end.Kind != transcript.EntryTurnEnd || end.Thinking != 1 || end.ToolCount != 2 {
		t.Fatalf("finished final turn footer = %+v", end)
	}
	es = foldTranscript([]line{{Type: "USER_INPUT", Content: "hi"}, {Type: "PLANNER_RESPONSE"}}, true)
	if es[len(es)-1].Kind == transcript.EntryTurnEnd {
		t.Fatalf("empty finished turn got a footer: %+v", es)
	}
}
