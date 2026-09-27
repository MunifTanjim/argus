package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestHistResumeGating(t *testing.T) {
	p := session.HistoryProject{NodeID: "n1", Cwd: "/tmp/proj"}
	s := session.HistorySession{SessionID: "s1", Agent: "claude", Resumable: false}
	m := withHistorySessions(model{}, p, session.HistorySessionPage{Items: []session.HistorySession{s}})
	_, cmd := m.baseKey(keyMsg("R"))
	if cmd != nil {
		t.Fatal("non-resumable session must not issue a resume command")
	}
	s.Resumable = true
	m = withHistorySessions(model{}, p, session.HistorySessionPage{Items: []session.HistorySession{s}})
	if _, cmd := m.baseKey(keyMsg("R")); cmd == nil {
		t.Fatal("resumable session must issue a resume command")
	}
}

func TestHistResumeGatingUnknownCwd(t *testing.T) {
	p := session.HistoryProject{NodeID: "n1", Cwd: ""} // unknown cwd
	m := withHistorySessions(model{}, p, session.HistorySessionPage{Items: []session.HistorySession{{SessionID: "s1", Agent: "claude", Resumable: true}}})
	mm, cmd := m.baseKey(keyMsg("R"))
	if cmd != nil {
		t.Fatal("unknown-cwd session must not issue a resume command")
	}
	if !strings.Contains(mm.(model).flash, "working directory") {
		t.Fatalf("expected unknown-cwd flash, got %q", mm.(model).flash)
	}
}

func TestHistorySessionsViewShowsFlash(t *testing.T) {
	m := model{width: 80, height: 24, left: leftSidebarState{hidden: true}}
	m = withHistorySessions(m, session.HistoryProject{Label: "proj", Cwd: "/tmp"}, session.HistorySessionPage{Items: []session.HistorySession{
		{SessionID: "s1", Agent: "claude", LastActivity: "2026-01-01T00:00:00Z"},
	}})
	m.flash = "resume unavailable: unknown working directory"
	out := ansi.Strip(m.View().Content)
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

func historyProjectsAB() []session.HistoryProject {
	return []session.HistoryProject{
		{NodeID: "n1", ProjectDir: "/a", Label: "a", Cwd: "/a"},
		{NodeID: "n1", ProjectDir: "/b", Label: "b", Cwd: "/b"},
	}
}

func histPage(ids ...string) session.HistorySessionPage {
	var p session.HistorySessionPage
	for _, id := range ids {
		p.Items = append(p.Items, session.HistorySession{SessionID: id, Agent: "claude", TranscriptPath: "/t/" + id})
	}
	return p
}

// A reply for a project the user has left still lands in the sessions of the
// project open now: the reply carries its project, but nothing checks it.
func TestHistoryReplyForAnOldProjectApplies(t *testing.T) {
	m := typeKeys(testModel(), "gt")
	m, _ = upd(m, histProjectsMsg{projects: historyProjectsAB()})
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, keyMsg("esc"))
	m = typeKeys(m, "j")
	m, _ = upd(m, keyMsg("enter"))
	if h := historyOf(m); h.project.ProjectDir != "/b" || !h.loading {
		t.Fatalf("enter should open b and load it: project=%q loading=%v", h.project.ProjectDir, h.loading)
	}
	m, _ = upd(m, histSessionsMsg{projectDir: "/a", page: histPage("a1")})
	if h := historyOf(m); len(h.sessions) != 1 || h.sessions[0].SessionID != "a1" || h.loading {
		t.Errorf("the reply for a lands in b's list: sessions=%v loading=%v", h.sessions, h.loading)
	}
}

// A reply that arrives after History closed changes nothing the user sees:
// History opens again from the start.
func TestHistoryReplyAfterCloseIsDropped(t *testing.T) {
	m := typeKeys(testModel(), "gt")
	m = typeKeys(m, "gT")
	m, _ = upd(m, histProjectsMsg{projects: historyProjectsAB()})
	if m.historyAt() >= 0 || viewOf(m) != viewHome {
		t.Fatalf("the reply should not open History: stack=%#v", m.main)
	}
	m = typeKeys(m, "gt")
	if h := historyOf(m); h.projects != nil || h.projCursor != 0 {
		t.Errorf("History should open loading: projects=%v cursor=%d", h.projects, h.projCursor)
	}
}

// The sessions list keeps its rows and cursor under an open transcript, and a
// page that arrives meanwhile still lands in it.
func TestHistoryTranscriptRoundTripKeepsTheSessions(t *testing.T) {
	m := typeKeys(testModel(), "gt")
	m, _ = upd(m, histProjectsMsg{projects: historyProjectsAB()})
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, histSessionsMsg{projectDir: "/a", page: histPage("s1", "s2")})
	m = typeKeys(m, "j")
	m, _ = upd(m, keyMsg("enter"))
	if viewOf(m) != viewHistoryTranscript || trOf(m).history.openSessionID != "s2" {
		t.Fatalf("enter should open s2: view=%v open=%q", viewOf(m), trOf(m).history.openSessionID)
	}
	m, _ = upd(m, histSessionsMsg{projectDir: "/a", offset: 2, page: histPage("s3")})
	m, _ = upd(m, keyMsg("esc"))
	h := historyOf(m)
	if viewOf(m) != viewHistorySessions || h.sessCursor != 1 || len(h.sessions) != 3 {
		t.Errorf("esc should return to the sessions on s2: view=%v cursor=%d sessions=%d", viewOf(m), h.sessCursor, len(h.sessions))
	}
}
