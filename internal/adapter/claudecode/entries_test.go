package claudecode

import (
	"reflect"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

var t0 = time.Date(2026, 10, 6, 14, 31, 0, 0, time.UTC)

func kinds(es []Entry) []EntryKind {
	out := make([]EntryKind, len(es))
	for i, e := range es {
		out[i] = e.Kind
	}
	return out
}

func doneTurn() []parser.Chunk {
	return []parser.Chunk{
		{Type: parser.UserChunk, Timestamp: t0, UserText: "map the code"},
		{Type: parser.AIChunk, Timestamp: t0.Add(time.Second), Model: "claude-opus-4-8", DurationMs: 61000,
			ThinkingCount: 1, Usage: parser.Usage{InputTokens: 1000, OutputTokens: 30},
			Items: []parser.DisplayItem{
				{Type: parser.ItemThinking, Text: "hmm"},
				{Type: parser.ItemToolCall, ToolName: "Read", ToolID: "tu1", ToolSummary: "a.go", ToolResult: "x"},
				{Type: parser.ItemOutput, Text: "   "},
				{Type: parser.ItemOutput, Text: "all done"},
			}},
	}
}

func TestFoldEntriesFlatSequence(t *testing.T) {
	es := foldEntries(doneTurn(), nil, false)
	want := []EntryKind{EntryUser, EntryThinking, EntryTool, EntryText, EntryTurnEnd}
	if got := kinds(es); !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	ids := []string{es[0].ID, es[1].ID, es[2].ID, es[3].ID, es[4].ID}
	if want := []string{"0", "1.0", "1.1", "1.3", "1.end"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	end := es[4]
	if end.ModelName == "" || end.Usage.Output != 30 || end.DurationMs != 61000 {
		t.Errorf("footer stats = %+v", end)
	}
	if end.Thinking != 1 || end.ToolCount != 1 {
		t.Errorf("footer counts thinking=%d tools=%d", end.Thinking, end.ToolCount)
	}
	if end.Timestamp != t0.Add(62*time.Second).Format(time.RFC3339) {
		t.Errorf("footer timestamp = %q, want turn end time", end.Timestamp)
	}
	if es[2].Result != "x" || es[2].InputPreview != "a.go" {
		t.Errorf("tool entry = %+v", es[2])
	}
}

func TestFoldEntriesFooterAppearsOnlyWhenDone(t *testing.T) {
	running := []parser.Chunk{
		{Type: parser.UserChunk, Timestamp: t0, UserText: "go"},
		{Type: parser.AIChunk, Timestamp: t0, Items: []parser.DisplayItem{
			{Type: parser.ItemOutput, Text: "checking"},
			{Type: parser.ItemToolCall, ToolName: "Bash", ToolID: "tu1"}, // no result yet
		}},
	}
	if got := kinds(foldEntries(running, nil, false)); got[len(got)-1] == EntryTurnEnd {
		t.Fatalf("running turn got a footer: %v", got)
	}

	done := running
	done[1].Items = append(done[1].Items,
		parser.DisplayItem{Type: parser.ItemOutput, Text: "done"})
	done[1].Items[1].ToolResult = "ok"
	got := foldEntries(done, nil, false)
	if got[len(got)-1].Kind != EntryTurnEnd {
		t.Fatalf("finished turn has no footer: %v", kinds(got))
	}
}

func TestFoldEntriesFinishedClosesDeadTurn(t *testing.T) {
	// A history session that died mid-turn: the last tool call never returned.
	died := []parser.Chunk{
		{Type: parser.UserChunk, Timestamp: t0, UserText: "go"},
		{Type: parser.AIChunk, Timestamp: t0, Items: []parser.DisplayItem{
			{Type: parser.ItemToolCall, ToolName: "Bash", ToolID: "tu1"},
		}},
	}
	if got := kinds(foldEntries(died, nil, false)); got[len(got)-1] == EntryTurnEnd {
		t.Fatalf("live read of an open turn got a footer: %v", got)
	}
	if got := kinds(foldEntries(died, nil, true)); got[len(got)-1] != EntryTurnEnd {
		t.Fatalf("finished read has no final footer: %v", got)
	}
}

func TestFoldEntriesEarlierTurnAlwaysHasFooter(t *testing.T) {
	cs := append(doneTurn(), parser.Chunk{Type: parser.UserChunk, Timestamp: t0, UserText: "next"},
		parser.Chunk{Type: parser.AIChunk, Timestamp: t0, Items: []parser.DisplayItem{
			{Type: parser.ItemToolCall, ToolName: "Bash", ToolID: "tu9"}}})
	es := foldEntries(cs, nil, false)
	n := 0
	for _, e := range es {
		if e.Kind == EntryTurnEnd {
			n++
		}
	}
	if n != 1 {
		t.Errorf("footers = %d, want 1 (first turn only)", n)
	}
}

func TestFoldEntriesInterruptedFooter(t *testing.T) {
	cs := []parser.Chunk{
		{Type: parser.UserChunk, Timestamp: t0, UserText: "go"},
		{Type: parser.AIChunk, Timestamp: t0, Interrupted: true, Items: []parser.DisplayItem{
			{Type: parser.ItemToolCall, ToolName: "Bash", ToolID: "tu1"}}},
	}
	es := foldEntries(cs, nil, false)
	last := es[len(es)-1]
	if last.Kind != EntryTurnEnd || !last.Interrupted {
		t.Fatalf("last = %+v, want interrupted footer", last)
	}
}

func TestFoldEntriesInterruptedFooterSurvivesNextPrompt(t *testing.T) {
	cs := []parser.Chunk{
		{Type: parser.UserChunk, Timestamp: t0, UserText: "go"},
		{Type: parser.AIChunk, Timestamp: t0, Interrupted: true, Items: []parser.DisplayItem{
			{Type: parser.ItemToolCall, ToolName: "Bash", ToolID: "tu1"}}},
		{Type: parser.UserChunk, Timestamp: t0, UserText: "try again"},
	}
	for _, e := range foldEntries(cs, nil, false) {
		if e.Kind == EntryTurnEnd {
			if !e.Interrupted {
				t.Fatalf("footer = %+v, want interrupted after a later prompt", e)
			}
			return
		}
	}
	t.Fatal("no footer")
}

func TestFoldEntriesStableIDs(t *testing.T) {
	short := doneTurn()
	long := append(doneTurn(), parser.Chunk{Type: parser.UserChunk, Timestamp: t0, UserText: "more"})
	a, b := foldEntries(short, nil, false), foldEntries(long, nil, false)
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Kind != b[i].Kind {
			t.Fatalf("entry %d changed: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestFoldEntriesUserSkillAndOtherKinds(t *testing.T) {
	cs := []parser.Chunk{
		{Type: parser.UserChunk, Timestamp: t0, UserText: "/brainstorm", Items: []parser.DisplayItem{
			{Type: parser.ItemToolCall, ToolName: "Skill", ToolID: "sk1", ToolSummary: "brainstorming"}}},
		{Type: parser.SystemChunk, Timestamp: t0, SystemLabel: "Recap", Output: "d"},
		{Type: parser.CompactChunk, Timestamp: t0, Output: "Compacted"},
		{Type: parser.ShellChunk, Timestamp: t0, ShellCommand: "ls", Output: "a", IsError: true},
	}
	es := foldEntries(cs, nil, false)
	want := []EntryKind{EntryUser, EntrySkill, EntrySystem, EntryCompact, EntryShell}
	if got := kinds(es); !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if es[2].Label != "Recap" || es[3].Summary != "Compacted" || es[4].Text != "ls" || !es[4].IsError {
		t.Errorf("entries = %+v", es)
	}
}
