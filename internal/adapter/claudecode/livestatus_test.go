package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestLiveStatusFromChunks(t *testing.T) {
	t0 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// Empty transcript: no claim.
	if got := liveStatusFromChunks(nil); got != "" {
		t.Errorf("empty: got %q want \"\"", got)
	}

	// Ends with text output -> turn done -> idle.
	idle := parser.BuildChunks([]parser.ClassifiedMsg{
		parser.AIMsg{Timestamp: t0, Model: "claude-opus-4-8",
			Blocks: []parser.ContentBlock{{Type: "text", Text: "All done."}}},
	})
	if got := liveStatusFromChunks(idle); got != session.StatusIdle {
		t.Errorf("text-output: got %q want idle", got)
	}

	// Pending tool_use with no result -> working.
	working := parser.BuildChunks([]parser.ClassifiedMsg{
		parser.AIMsg{Timestamp: t0, Model: "claude-opus-4-8",
			ToolCalls: []parser.ToolCall{{ID: "c1", Name: "Read"}},
			Blocks: []parser.ContentBlock{{Type: "tool_use", ToolID: "c1",
				ToolName: "Read", ToolInput: json.RawMessage(`{"file_path":"x.go"}`)}}},
	})
	if got := liveStatusFromChunks(working); got != session.StatusWorking {
		t.Errorf("tool_use: got %q want working", got)
	}
}

func TestClassifyLiveStatus(t *testing.T) {
	// Real parser fixtures (relative to this package dir).
	if got := classifyLiveStatus("parser/testdata/not_ongoing_text.jsonl"); got != session.StatusIdle {
		t.Errorf("not_ongoing_text: got %q want idle", got)
	}
	if got := classifyLiveStatus("parser/testdata/ongoing_tooluse.jsonl"); got != session.StatusWorking {
		t.Errorf("ongoing_tooluse: got %q want working", got)
	}
	// No claim on empty/missing path.
	if got := classifyLiveStatus(""); got != "" {
		t.Errorf("empty path: got %q want \"\"", got)
	}
	if got := classifyLiveStatus("parser/testdata/does_not_exist.jsonl"); got != "" {
		t.Errorf("missing file: got %q want \"\"", got)
	}
}

// copyFixture copies a fixture transcript into a temp dir so the test can age
// its mtime without touching the committed file.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("parser", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write temp fixture: %v", err)
	}
	return dst
}

func TestClassifyLiveStatusFreshnessGuard(t *testing.T) {
	// ongoing_toolresult: IsOngoing=true with a COMPLETED tool (nothing pending).
	// Fresh -> trusted as working; stale -> reclassified idle by the guard.
	done := copyFixture(t, "ongoing_toolresult.jsonl")
	if got := classifyLiveStatus(done); got != session.StatusWorking {
		t.Fatalf("fresh completed-tool: got %q want working", got)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(done, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if got := classifyLiveStatus(done); got != session.StatusIdle {
		t.Errorf("stale completed-tool: got %q want idle (interrupted/aborted)", got)
	}

	// ongoing_tooluse: a PENDING tool (no result) is genuinely executing, so a
	// stale transcript must still classify working — the guard must not fire.
	pending := copyFixture(t, "ongoing_tooluse.jsonl")
	if err := os.Chtimes(pending, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if got := classifyLiveStatus(pending); got != session.StatusWorking {
		t.Errorf("stale pending-tool: got %q want working (long-running tool)", got)
	}
}

// TestClassifyLiveStatusEarlyUnpairedTool is the regression for an interrupted,
// walked-away session stuck as working. ongoing_early_unpaired_tool has an early
// tool_use whose result lands outside its merge buffer (folds to an empty
// ToolResult) and a later completed turn whose trailing tool DOES have a result.
// IsOngoing is true (AI activity after the last text output), but the only empty
// ToolResult is a historical folding artifact, not active work — so a stale
// transcript must reclassify idle. Before the trailing-chunk fix to
// hasPendingWork, the early empty result bypassed the freshness guard and the
// session stayed working forever.
func TestClassifyLiveStatusEarlyUnpairedTool(t *testing.T) {
	f := copyFixture(t, "ongoing_early_unpaired_tool.jsonl")

	// Fresh: trusted as working (recent activity).
	if got := classifyLiveStatus(f); got != session.StatusWorking {
		t.Fatalf("fresh early-unpaired: got %q want working", got)
	}

	// Stale: the empty ToolResult is an early folding artifact, not pending work,
	// so the guard must downgrade to idle.
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if got := classifyLiveStatus(f); got != session.StatusIdle {
		t.Errorf("stale early-unpaired: got %q want idle (interrupted/abandoned)", got)
	}
}

// TestHasPendingWorkTrailingChunkOnly verifies hasPendingWork only counts a
// tool/subagent awaiting its result in the LAST AI chunk — a genuinely executing
// operation — and ignores empty ToolResults in earlier chunks (folding artifacts).
func TestHasPendingWorkTrailingChunkOnly(t *testing.T) {
	t0 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	read := func(id string) parser.ContentBlock {
		return parser.ContentBlock{Type: "tool_use", ToolID: id, ToolName: "Read",
			ToolInput: json.RawMessage(`{"file_path":"x.go"}`)}
	}

	// Early pending tool, then a genuine user prompt (chunk break), then a
	// completed turn. The pending tool is NOT in the last AI chunk -> false.
	earlyPending := parser.BuildChunks([]parser.ClassifiedMsg{
		parser.AIMsg{Timestamp: t0, Model: "m", Blocks: []parser.ContentBlock{read("r1")}},
		parser.UserMsg{Timestamp: t0.Add(time.Second), Text: "now do Y"},
		parser.AIMsg{Timestamp: t0.Add(2 * time.Second), Model: "m", StopReason: "end_turn",
			Blocks: []parser.ContentBlock{{Type: "text", Text: "Done."}}},
	})
	if hasPendingWork(earlyPending) {
		t.Errorf("early pending tool in a non-last chunk must not count as pending work")
	}

	// Pending tool in the last AI chunk -> genuinely executing -> true.
	lastPending := parser.BuildChunks([]parser.ClassifiedMsg{
		parser.AIMsg{Timestamp: t0, Model: "m",
			Blocks: []parser.ContentBlock{{Type: "text", Text: "Working on it."}}},
		parser.UserMsg{Timestamp: t0.Add(time.Second), Text: "go"},
		parser.AIMsg{Timestamp: t0.Add(2 * time.Second), Model: "m",
			Blocks: []parser.ContentBlock{{Type: "tool_use", ToolID: "b1", ToolName: "Bash",
				ToolInput: json.RawMessage(`{"command":"sleep 300"}`)}}},
	})
	if !hasPendingWork(lastPending) {
		t.Errorf("pending tool in the last AI chunk must count as pending work")
	}

	// No pending tool anywhere -> false.
	none := parser.BuildChunks([]parser.ClassifiedMsg{
		parser.AIMsg{Timestamp: t0, Model: "m", StopReason: "end_turn",
			Blocks: []parser.ContentBlock{{Type: "text", Text: "All done."}}},
	})
	if hasPendingWork(none) {
		t.Errorf("completed turn must not report pending work")
	}
}
