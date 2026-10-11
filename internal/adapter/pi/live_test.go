package pi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// TestFoldLiveSession folds a real session file captured from pi 1.1.0
// (openrouter liquid/lfm-2.5-2.6b:free): prompt → gated bash approval → DONE.
// It locks the parser against the true on-disk shapes: block-array user
// content, thinking+toolCall assistants, toolResult join, model/thinking
// changes, usage accumulation.
func TestFoldLiveSession(t *testing.T) {
	sf, err := readSessionFile(filepath.Join("testdata", "live-turn-01.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	entries := foldBranch(sf.activeBranch(), true)
	got := kinds(entries)
	want := []transcript.EntryKind{
		transcript.EntrySystem, // model_change
		transcript.EntrySystem, // thinking_level_change
		transcript.EntryUser,
		transcript.EntryThinking,
		transcript.EntryTool, // bash + joined result
		transcript.EntryThinking,
		transcript.EntryText, // DONE
		transcript.EntryTurnEnd,
	}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}

	var user, tool, done *transcript.Entry
	var turn *transcript.Entry
	for i := range entries {
		switch entries[i].Kind {
		case transcript.EntryUser:
			user = &entries[i]
		case transcript.EntryTool:
			tool = &entries[i]
		case transcript.EntryText:
			done = &entries[i]
		case transcript.EntryTurnEnd:
			turn = &entries[i]
		}
	}
	if user == nil || !strings.HasPrefix(user.Text, "Run the shell command") {
		t.Fatalf("user = %+v", user)
	}
	if tool == nil {
		t.Fatal("no tool entry")
	}
	if tool.ToolName != "bash" || !strings.HasPrefix(tool.ToolID, "chatcmpl-tool-") {
		t.Fatalf("tool = %+v", tool)
	}
	if tool.InputPreview != "echo hello-argus" {
		t.Fatalf("preview = %q", tool.InputPreview)
	}
	if tool.Result != "hello-argus\n" || tool.ResultIsError {
		t.Fatalf("result = %+v", tool)
	}
	if done == nil || done.Text != "DONE" {
		t.Fatalf("done = %+v", done)
	}
	if turn == nil {
		t.Fatal("no turn footer")
	}
	if turn.ModelName != "liquid/lfm-2.5-2.6b:free" {
		t.Fatalf("model = %q", turn.ModelName)
	}
	if turn.Usage.Input == 0 || turn.Usage.Output == 0 {
		t.Fatalf("usage = %+v", turn.Usage)
	}
	if turn.Thinking != 2 || turn.ToolCount != 1 {
		t.Fatalf("counts = %+v", turn)
	}
	// Model/thinking change rows render.
	if entries[0].Label != "Model: openrouter/liquid/lfm-2.5-2.6b:free" {
		t.Fatalf("model row = %+v", entries[0])
	}
	if entries[1].Label != "Thinking: medium" {
		t.Fatalf("thinking row = %+v", entries[1])
	}
}
