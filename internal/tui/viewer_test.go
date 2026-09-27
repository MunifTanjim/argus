package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/bundle"
)

func newTestViewerModel(t *testing.T) (model, *fileClient) {
	t.Helper()
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
	return newViewerModel(fc, false), fc
}

func TestViewerModelStartsInTranscript(t *testing.T) {
	model, fc := newTestViewerModel(t)
	if !model.viewer {
		t.Fatal("want model.viewer == true")
	}
	if viewOf(model) != viewHistoryTranscript {
		t.Fatalf("want viewHistoryTranscript, got %v", viewOf(model))
	}
	if trOf(model).history.openAgent != "claude" {
		t.Fatalf("want openAgent=claude, got %q", trOf(model).history.openAgent)
	}
	if trOf(model).history.openPath != fc.entryPath {
		t.Fatalf("want openPath=%q, got %q", fc.entryPath, trOf(model).history.openPath)
	}
	if trOf(model).history.openSession.Title != "T" {
		t.Fatalf("openSession not seeded from manifest: got title %q", trOf(model).history.openSession.Title)
	}
}

func TestViewerHeaderFromManifest(t *testing.T) {
	m, _ := newTestViewerModel(t)
	h := tvOf(&m).historyTranscriptHeader()
	if !strings.Contains(h, "T") {
		t.Fatalf("viewer header should show manifest title/label, got %q", h)
	}
	if strings.Contains(h, "history") {
		t.Fatalf("viewer header must not show the history breadcrumb, got %q", h)
	}
}

func TestViewerResumeInert(t *testing.T) {
	m, _ := newTestViewerModel(t)
	m = withTr(m, func(t *transcriptComp) { t.historyView = histTranscript })
	res, cmd := m.baseKey(tea.KeyPressMsg{Code: 'R'})
	if cmd != nil {
		t.Fatal("resume key in viewer should be inert")
	}
	if viewOf(res.(model)) != viewHistoryTranscript {
		t.Fatal("resume key in viewer should not change view")
	}
}

// TestViewerBackQuits asserts that pressing the Back key at the top transcript
// level in the viewer returns a tea.Quit command rather than navigating to the
// (non-existent) session list.
func TestViewerBackQuits(t *testing.T) {
	m, _ := newTestViewerModel(t)
	m = withTr(m, func(t *transcriptComp) { t.historyView = histTranscript }) // ensure top level, no detail frames

	_, cmd := m.baseKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("back key in viewer: want non-nil quit cmd, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("back key in viewer: want QuitMsg from cmd(), got %T", cmd())
	}
}

// TestViewerExportInert asserts that pressing the export key in the viewer is a
// no-op: actExportSession returns a nil command so no RPC is made and no file is
// written.
func TestViewerExportInert(t *testing.T) {
	m, _ := newTestViewerModel(t)
	cmd := tvOf(&m).actExportSession(tea.KeyPressMsg{})
	if cmd != nil {
		t.Fatalf("actExportSession in viewer: want nil cmd, got non-nil")
	}
}
