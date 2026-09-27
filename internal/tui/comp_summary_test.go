package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestProjectRowShowsTheSummaryComponent(t *testing.T) {
	m := selectRow(wideWorkspace(), "n1:p1")
	s, ok := m.baseComp().(summaryComp)
	if !ok || s.kind != rowProject || s.id != "n1:p1" {
		t.Fatalf("base = %#v, want the summary of n1:p1", m.baseComp())
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "/repo/.git") {
		t.Error("the summary should show the project's directory")
	}
	if m.currentWorkspace() != "" {
		t.Errorf("currentWorkspace = %q, want none for a summary", m.currentWorkspace())
	}
}

func TestWorkspacePaneReadsItsOwnWorkspace(t *testing.T) {
	m := wideWorkspace()
	m.main = m.main.replaceAt(0, workspaceComp{ws: "n1:w2"})
	if got := m.currentWorkspace(); got != "n1:w2" {
		t.Errorf("currentWorkspace = %q, want n1:w2 (the pane's own workspace)", got)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "repo-feat  feature") {
		t.Error("the pane should draw n1:w2's header, not the cursor row's")
	}
}
