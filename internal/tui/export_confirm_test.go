package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestExportConfirmGate(t *testing.T) {
	m := model{mode: modeHistoryTranscript, historyView: histTranscript}
	m.history.openPath = "/x/session.jsonl"
	m.history.openAgent = "claude"

	// First press arms the confirmation, makes no RPC.
	res, cmd := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'E'})
	m = res.(model)
	if !m.pendingExport {
		t.Fatal("export key should arm pendingExport")
	}
	if cmd != nil {
		t.Fatal("arming export must not return a command")
	}

	// A non-y key cancels.
	res, cmd = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'n'})
	m = res.(model)
	if m.pendingExport {
		t.Fatal("non-y key should clear pendingExport")
	}
	if cmd != nil {
		t.Fatal("cancel must not return a command")
	}

	// Confirming with y runs the export.
	m.pendingExport = true
	res, cmd = m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'y'})
	m = res.(model)
	if m.pendingExport {
		t.Fatal("y should clear pendingExport")
	}
	if cmd == nil {
		t.Fatal("y should trigger the export command")
	}
}

func TestExportConfirmGateSessionList(t *testing.T) {
	m := model{mode: modeHistorySessions}
	m.history.sessions = []session.HistorySession{{SessionID: "s1", Agent: "claude", TranscriptPath: "/x/s1.jsonl"}}
	m.history.project = session.HistoryProject{Repo: "argus", Cwd: "/x"}

	res, cmd := m.handleHistorySessionsKey(tea.KeyPressMsg{Code: 'E'})
	m = res.(model)
	if !m.pendingExport || cmd != nil {
		t.Fatalf("export key should arm confirm without a command: pending=%v cmd=%v", m.pendingExport, cmd != nil)
	}

	res, cmd = m.handleHistorySessionsKey(tea.KeyPressMsg{Code: 'y'})
	m = res.(model)
	if m.pendingExport || cmd == nil {
		t.Fatalf("y should clear pending and run export: pending=%v cmd=%v", m.pendingExport, cmd != nil)
	}
}

func TestExportKeyInertOnViewer(t *testing.T) {
	m := model{mode: modeHistoryTranscript, historyView: histTranscript, viewer: true}
	m.history.openPath = "/x/session.jsonl"
	res, cmd := m.handleHistoryTranscriptKey(tea.KeyPressMsg{Code: 'E'})
	if res.(model).pendingExport {
		t.Fatal("viewer must not arm export")
	}
	if cmd != nil {
		t.Fatal("viewer export key must be inert")
	}
}

func TestBundleExtractDirContentAddressed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.argus")
	content := bytes.Repeat([]byte("argus-bundle-payload!"), 4096) // > 64 KiB chunk
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	d1, err := bundleExtractDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if d2, err := bundleExtractDir(path); err != nil || d2 != d1 {
		t.Fatalf("same bundle should map to same dir: %q vs %q (err=%v)", d1, d2, err)
	}

	// Same content at a different path (copy/rename) reuses the extraction.
	copyPath := filepath.Join(t.TempDir(), "renamed.argus")
	if err := os.WriteFile(copyPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if dc, err := bundleExtractDir(copyPath); err != nil || dc != d1 {
		t.Fatalf("copied bundle should reuse dir: got %q want %q (err=%v)", dc, d1, err)
	}

	// Edited content maps elsewhere.
	if err := os.WriteFile(path, append(content, 'X'), 0o644); err != nil {
		t.Fatal(err)
	}
	if de, err := bundleExtractDir(path); err != nil || de == d1 {
		t.Fatalf("edited bundle should map to a different dir: got %q (err=%v)", de, err)
	}
}
