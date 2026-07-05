package antigravity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
)

func writeBrainTranscript(t *testing.T, convID, content string) {
	t.Helper()
	p := transcriptPathFor(convID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSummarizeChunksTaskAndActivity(t *testing.T) {
	chunks, _ := parseTranscript(writeLines(t, sampleTranscript))
	s := summarizeChunks(chunks)
	if s == nil {
		t.Fatal("summary should not be nil")
	}
	if s.Task != "list the files" {
		t.Fatalf("task = %q; want %q", s.Task, "list the files")
	}
	if s.Tokens != 0 {
		t.Fatalf("tokens must stay 0 (agy has none), got %d", s.Tokens)
	}
}

func TestSummarizeChunksEmpty(t *testing.T) {
	if s := summarizeChunks(nil); s != nil {
		t.Fatalf("empty chunks should yield nil, got %+v", s)
	}
}

func TestRefreshesSummary(t *testing.T) {
	if !refreshesSummary("PreInvocation") {
		t.Fatal("PreInvocation should refresh")
	}
	for _, ev := range []string{"Stop", "PreToolUse", "PostToolUse"} {
		if refreshesSummary(ev) {
			t.Errorf("%s must not refresh", ev)
		}
	}
}

func TestProcessHookNoClobberOnNonRefreshEvent(t *testing.T) {
	setupHome(t)
	reg := registry.New()
	writeBrainTranscript(t, "conv-nc", sampleTranscript)
	// Seed with a full card via a refresh event.
	seed := []byte(`{"conversationId":"conv-nc","modelName":"gpt-oss-120b-medium","workspacePaths":["/x"]}`)
	ProcessHook(reg, HookEvent{Agent: Agent, Event: "PreInvocation", TmuxPane: "%9", Payload: seed})

	// A high-frequency event with modelName must not clobber the card.
	follow := []byte(`{"conversationId":"conv-nc","modelName":"gpt-oss-120b-medium","workspacePaths":["/x"]}`)
	s, alive := ProcessHook(reg, HookEvent{Agent: Agent, Event: "PostToolUse", TmuxPane: "%9", Payload: follow})
	if !alive {
		t.Fatal("session should still exist")
	}
	// Non-refresh event must not clobber the seeded card's Task.
	if s.Summary == nil || s.Summary.Task == "" {
		t.Fatalf("card was clobbered by PostToolUse: summary=%+v", s.Summary)
	}
}

func TestProcessHookSetsCard(t *testing.T) {
	setupHome(t)
	reg := registry.New()
	writeBrainTranscript(t, "conv-card", sampleTranscript)
	payload := []byte(`{"conversationId":"conv-card","modelName":"gpt-oss-120b-medium","workspacePaths":["/x"]}`)
	ev := HookEvent{Agent: Agent, Event: "PreInvocation", TmuxPane: "%1", Payload: payload}
	s, alive := ProcessHook(reg, ev)
	if !alive || s.Summary == nil {
		t.Fatalf("expected a card, got alive=%v summary=%+v", alive, s.Summary)
	}
	if s.Summary.ModelName == "" {
		t.Fatalf("model name not set: %+v", s.Summary)
	}
	if s.Summary.Task != "list the files" {
		t.Fatalf("task = %q", s.Summary.Task)
	}
}
