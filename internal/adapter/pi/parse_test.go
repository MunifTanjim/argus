package pi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

const testSession = `{"type":"session","version":3,"id":"ses-1","timestamp":"2026-10-11T18:00:00.000Z","cwd":"/work/proj"}
{"type":"thinking_level_change","id":"e0","parentId":null,"timestamp":"2026-10-11T18:00:01.000Z","thinkingLevel":"off"}
{"type":"message","id":"m1","parentId":"e0","timestamp":"2026-10-11T18:00:02.000Z","message":{"role":"user","content":"list files","timestamp":1791741602000}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-11T18:00:03.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"should check"},{"type":"text","text":"On it."},{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"ls"}}],"api":"x","provider":"p","model":"m1","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15},"stopReason":"toolUse","timestamp":1791741603000}}
{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-10-11T18:00:04.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"a.txt"}],"isError":false,"timestamp":1791741604000}}
{"type":"message","id":"m4","parentId":"m3","timestamp":"2026-10-11T18:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done: a.txt"}],"api":"x","provider":"p","model":"m1","usage":{"input":20,"output":8,"cacheRead":0,"cacheWrite":0,"totalTokens":28},"stopReason":"stop","timestamp":1791741605000}}
{"type":"model_change","id":"e5","parentId":"m4","timestamp":"2026-10-11T18:00:06.000Z","provider":"anthropic","modelId":"claude-x"}
{"type":"message","id":"m6","parentId":"e5","timestamp":"2026-10-11T18:00:07.000Z","message":{"role":"bashExecution","command":"echo hi","output":"hi\n","exitCode":0,"cancelled":false,"truncated":false,"timestamp":1791741607000}}
`

func writeTestSession(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "2026-10-11T18-00-00-000Z_ses-1.jsonl")
	if err := os.WriteFile(p, []byte(testSession), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func kinds(es []transcript.Entry) []transcript.EntryKind {
	out := make([]transcript.EntryKind, len(es))
	for i, e := range es {
		out[i] = e.Kind
	}
	return out
}

func TestFoldBranch(t *testing.T) {
	sf, err := readSessionFile(writeTestSession(t))
	if err != nil {
		t.Fatal(err)
	}
	entries := foldBranch(sf.activeBranch(), true)
	got := kinds(entries)
	want := []transcript.EntryKind{
		transcript.EntrySystem, // thinking_level_change
		transcript.EntryUser,
		transcript.EntryThinking,
		transcript.EntryText, // "On it."
		transcript.EntryTool, // bash call_1 + joined result
		transcript.EntryText, // "Done: a.txt" (same run)
		transcript.EntryTurnEnd,
		transcript.EntrySystem, // model_change
		transcript.EntryShell,  // bashExecution
	}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
	// Tool result joined inline.
	var tool *transcript.Entry
	for i := range entries {
		if entries[i].Kind == transcript.EntryTool {
			tool = &entries[i]
		}
	}
	if tool == nil {
		t.Fatal("no tool entry")
	}
	if tool.ToolName != "bash" || tool.ToolID != "call_1" {
		t.Fatalf("tool = %+v", tool)
	}
	if tool.Result != "a.txt" {
		t.Fatalf("tool result = %q", tool.Result)
	}
	if tool.InputPreview != "ls" {
		t.Fatalf("tool preview = %q", tool.InputPreview)
	}
	// Turn footer carries the run's model + accumulated usage.
	var turns []transcript.Entry
	for _, e := range entries {
		if e.Kind == transcript.EntryTurnEnd {
			turns = append(turns, e)
		}
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	if turns[0].ModelName != "m1" || turns[0].Usage.Input != 30 || turns[0].Usage.Output != 13 {
		t.Fatalf("turn0 = %+v", turns[0])
	}
	if turns[0].Thinking != 1 || turns[0].ToolCount != 1 {
		t.Fatalf("turn0 counts = %+v", turns[0])
	}
	// Shell entry.
	last := entries[len(entries)-1]
	if last.Kind != transcript.EntryShell || last.Text != "echo hi" || last.Detail != "hi\n" {
		t.Fatalf("shell = %+v", last)
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	sf, err := readSessionFile(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.entries) != 0 {
		t.Fatalf("entries = %d", len(sf.entries))
	}
	if out := foldBranch(sf.activeBranch(), true); len(out) != 0 {
		t.Fatalf("folded = %d", len(out))
	}
}

func TestCompactionTruncates(t *testing.T) {
	const sess = `{"type":"session","version":3,"id":"s","timestamp":"2026-10-11T18:00:00.000Z","cwd":"/w"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-11T18:00:01.000Z","message":{"role":"user","content":"old","timestamp":1}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-11T18:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}],"model":"m","usage":{"input":1,"output":1},"stopReason":"stop","timestamp":2}}
{"type":"compaction","id":"c1","parentId":"m2","timestamp":"2026-10-11T18:00:03.000Z","summary":"talked about old","firstKeptEntryId":"m2","tokensBefore":99}
{"type":"message","id":"m3","parentId":"c1","timestamp":"2026-10-11T18:00:04.000Z","message":{"role":"user","content":"new","timestamp":3}}
`
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(sess), 0o644); err != nil {
		t.Fatal(err)
	}
	sf, err := readSessionFile(p)
	if err != nil {
		t.Fatal(err)
	}
	entries := foldBranch(sf.activeBranch(), true)
	got := kinds(entries)
	// m1 dropped (before firstKeptEntryId); m2 kept; compaction row; m3.
	want := []transcript.EntryKind{
		transcript.EntryText, // m2 "old reply"
		transcript.EntryTurnEnd,
		transcript.EntryCompact,
		transcript.EntryUser, // m3 "new"
	}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
	if entries[2].Summary != "talked about old" {
		t.Fatalf("compact = %+v", entries[2])
	}
}

func TestContextEditOmits(t *testing.T) {
	const sess = `{"type":"session","version":3,"id":"s","timestamp":"2026-10-11T18:00:00.000Z","cwd":"/w"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-11T18:00:01.000Z","message":{"role":"user","content":"secret","timestamp":1}}
{"type":"context_edit","id":"e1","parentId":"m1","timestamp":"2026-10-11T18:00:02.000Z","targetId":"m1","replacement":null}
{"type":"message","id":"m2","parentId":"e1","timestamp":"2026-10-11T18:00:03.000Z","message":{"role":"user","content":"after","timestamp":3}}
`
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(sess), 0o644); err != nil {
		t.Fatal(err)
	}
	sf, err := readSessionFile(p)
	if err != nil {
		t.Fatal(err)
	}
	entries := foldBranch(sf.activeBranch(), true)
	if len(entries) != 1 || entries[0].Text != "after" {
		t.Fatalf("entries = %+v", entries)
	}
}
