package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestHistResumeGating(t *testing.T) {
	m := model{}
	m.history.project = session.HistoryProject{NodeID: "n1", Cwd: "/tmp/proj"}
	m.history.sessions = []session.HistorySession{{SessionID: "s1", Agent: "claude", Resumable: false}}
	m.history.sessCursor = 0
	_, cmd := m.actHistSessResume(tea.KeyPressMsg{})
	if cmd != nil {
		t.Fatal("non-resumable session must not issue a resume command")
	}
	m.history.sessions[0].Resumable = true
	if _, cmd := m.actHistSessResume(tea.KeyPressMsg{}); cmd == nil {
		t.Fatal("resumable session must issue a resume command")
	}
}

func TestHistResumeGatingUnknownCwd(t *testing.T) {
	m := model{}
	m.history.project = session.HistoryProject{NodeID: "n1", Cwd: ""} // unknown cwd
	m.history.sessions = []session.HistorySession{{SessionID: "s1", Agent: "claude", Resumable: true}}
	m.history.sessCursor = 0
	mm, cmd := m.actHistSessResume(tea.KeyPressMsg{})
	if cmd != nil {
		t.Fatal("unknown-cwd session must not issue a resume command")
	}
	if !strings.Contains(mm.(model).flash, "working directory") {
		t.Fatalf("expected unknown-cwd flash, got %q", mm.(model).flash)
	}
}

func TestHistorySessionsViewShowsFlash(t *testing.T) {
	m := model{width: 80, height: 24}
	m.history.project = session.HistoryProject{Label: "proj", Cwd: "/tmp"}
	m.history.sessions = []session.HistorySession{
		{SessionID: "s1", Agent: "claude", LastActivity: "2026-01-01T00:00:00Z"},
	}
	m.flash = "resume unavailable: unknown working directory"
	out := ansi.Strip(m.historySessionsView())
	if !strings.Contains(out, "unknown working directory") {
		t.Fatalf("flash not rendered in history sessions view:\n%s", out)
	}
}

func TestHistorySessionRowShowsAgentWhenMixed(t *testing.T) {
	s := session.HistorySession{SessionID: "s1", Agent: "codex", LastActivity: "2026-01-01T00:00:00Z"}
	shown := ansi.Strip(historySessionRow(s, false, 78, true))
	if !strings.Contains(shown, "Codex") {
		t.Errorf("showAgent=true should render agent label:\n%s", shown)
	}
	hidden := ansi.Strip(historySessionRow(s, false, 78, false))
	if strings.Contains(hidden, "Codex") {
		t.Errorf("showAgent=false should not render agent label:\n%s", hidden)
	}
}
