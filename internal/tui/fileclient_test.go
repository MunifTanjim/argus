package tui

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func copyFileForTest(t *testing.T, src, dst string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func TestFileClientServesHistory(t *testing.T) {
	// Reuse a real claudecode transcript fixture from the parser testdata.
	fixture := filepath.Join("..", "adapter", "claudecode", "parser", "testdata", "multi_turn.jsonl")
	dir := t.TempDir()
	// Lay the fixture where manifest.Entry points.
	entry := "root/session.jsonl"
	if err := copyFileForTest(t, fixture, filepath.Join(dir, filepath.FromSlash(entry))); err != nil {
		t.Fatalf("fixture unavailable: %v", err)
	}
	m := bundle.Manifest{Agent: "claude", Entry: entry, Metadata: bundle.Metadata{Title: "T"}}
	fc, err := newFileClient(dir, m)
	if err != nil {
		t.Fatal(err)
	}

	var page session.HistorySessionPage
	if err := fc.Call(api.MethodSessionsHistorySessions, api.HistorySessionsParams{}, &page); err != nil {
		t.Fatalf("historySessions: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "T" {
		t.Fatalf("want one session titled T, got %+v", page.Items)
	}

	var view transcript.TranscriptView
	if err := fc.Call(api.MethodSessionsHistoryTranscript, api.HistoryTranscriptParams{
		TranscriptPath: filepath.Join(dir, filepath.FromSlash(entry)), Agent: "claude",
	}, &view); err != nil {
		t.Fatalf("historyTranscript: %v", err)
	}
	if len(view.Chunks) == 0 {
		t.Fatal("expected chunks from fixture")
	}
}

func TestFileClientCloseIdempotent(t *testing.T) {
	fixture := filepath.Join("..", "adapter", "claudecode", "parser", "testdata", "multi_turn.jsonl")
	dir := t.TempDir()
	entry := "root/session.jsonl"
	if err := copyFileForTest(t, fixture, filepath.Join(dir, filepath.FromSlash(entry))); err != nil {
		t.Fatalf("fixture unavailable: %v", err)
	}
	m := bundle.Manifest{Agent: "claude", Entry: entry, Metadata: bundle.Metadata{Title: "T"}}
	fc, err := newFileClient(dir, m)
	if err != nil {
		t.Fatal(err)
	}

	if err := fc.Close(); err != nil && !os.IsNotExist(err) {
		t.Fatalf("first Close: %v", err)
	}
	// Second call must not panic.
	if err := fc.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestFileClientSubagentDrill(t *testing.T) {
	// Fixture mirrors the claudecode subagents layout:
	//   destDir/root/session.jsonl             (main transcript)
	//   destDir/root/session/subagents/agent-abc1234.jsonl  (subagent file)
	baseFixture := filepath.Join("..", "adapter", "claudecode", "parser", "testdata", "test-session.jsonl")
	subagentFixture := filepath.Join("..", "adapter", "claudecode", "parser", "testdata", "test-session", "subagents", "agent-abc1234.jsonl")

	dir := t.TempDir()
	entry := "root/session.jsonl"
	entryAbs := filepath.Join(dir, filepath.FromSlash(entry))
	if err := copyFileForTest(t, baseFixture, entryAbs); err != nil {
		t.Fatalf("main fixture unavailable: %v", err)
	}
	// subagentsDir for session.jsonl is session/subagents/
	subDir := filepath.Join(filepath.Dir(entryAbs), "session", "subagents")
	if err := copyFileForTest(t, subagentFixture, filepath.Join(subDir, "agent-abc1234.jsonl")); err != nil {
		t.Fatalf("subagent fixture unavailable: %v", err)
	}

	m := bundle.Manifest{Agent: "claude", Entry: entry, Metadata: bundle.Metadata{Title: "T"}}
	fc, err := newFileClient(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	defer fc.Close()

	var view transcript.TranscriptView
	if err := fc.Call(api.MethodSessionsHistoryTranscript, api.HistoryTranscriptParams{
		TranscriptPath: entryAbs, Agent: "claude", AgentID: "abc1234",
	}, &view); err != nil {
		t.Fatalf("subagent Call: %v", err)
	}
	if len(view.Chunks) == 0 {
		t.Fatal("expected chunks from subagent fixture")
	}
}
