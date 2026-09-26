package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

func pressKeys(m model, keys ...tea.KeyPressMsg) model {
	for _, k := range keys {
		m, _ = upd(m, k)
	}
	return m
}

func cw(r rune) []tea.KeyPressMsg { return []tea.KeyPressMsg{ctrlKey('w'), keyMsg(string(r))} }

// workspaceSession is the session screen on n1:s1, whose workspace n1:w1 is in
// the tree, with the files sidebar visible and loaded.
func workspaceSession(ix *session.Interaction) model {
	m := wideWorkspace()
	s := m.sessions["n1:s1"]
	s.Interaction = ix
	m.sessions["n1:s1"] = s
	m.projects.focus = focusPane
	mm, _ := m.enterSession("n1:s1")
	m, _ = mm.syncSidebar()
	return withTreeLoaded(m, "n1:w1")
}

func TestPaneRightFromTreeThenFiles(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('l')...)
	if m.projects.focus != focusPane {
		t.Fatalf("<C-w>l from the tree: focus = %v, want the pane", m.projects.focus)
	}
	m = pressKeys(m, cw('l')...)
	if m.projects.focus != focusFiles {
		t.Errorf("<C-w>l from the pane: focus = %v, want the files sidebar", m.projects.focus)
	}
}

func TestPaneRightWithoutWorkspaceDoesNothing(t *testing.T) {
	m := wideWorkspace()
	m.projects.selectRow("n1:p1")
	m.projects.focus = focusPane
	m = pressKeys(m, cw('l')...)
	if m.projects.focus != focusPane {
		t.Errorf("<C-w>l from the pane with no workspace: focus = %v, want no change", m.projects.focus)
	}
}

func TestPaneLeftFromFilesThenPane(t *testing.T) {
	m := pressKeys(filesFocused(), cw('h')...)
	if m.projects.focus != focusPane || m.mode != modeProjects {
		t.Fatalf("<C-w>h from the files sidebar: focus = %v mode = %v, want the pane", m.projects.focus, m.mode)
	}
	m = pressKeys(m, cw('h')...)
	if m.projects.focus != focusTree || m.projects.cursorRowID() != "n1:w1" {
		t.Errorf("<C-w>h from the pane: focus = %v row = %q, want the tree on n1:w1", m.projects.focus, m.projects.cursorRowID())
	}
}

func TestPaneLeftFromTreeDoesNothing(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('h')...)
	if m.projects.focus != focusTree || m.mode != modeProjects {
		t.Errorf("<C-w>h from the tree: focus = %v mode = %v, want no change", m.projects.focus, m.mode)
	}
}

func TestPaneLeftWithTreeHiddenDoesNothing(t *testing.T) {
	m := wideWorkspace()
	m.projects.focus = focusPane
	m.projects.sidebarHidden = true
	m = pressKeys(m, cw('h')...)
	if m.projects.focus != focusPane || m.mode != modeProjects {
		t.Errorf("<C-w>h with the tree hidden: focus = %v mode = %v, want no change", m.projects.focus, m.mode)
	}
}

func TestPaneCycleOnProjects(t *testing.T) {
	m := wideWorkspace()
	for _, want := range []projectsFocus{focusPane, focusFiles, focusTree} {
		if m = pressKeys(m, cw('w')...); m.projects.focus != want {
			t.Fatalf("<C-w>w: focus = %v, want %v", m.projects.focus, want)
		}
	}
	if m = pressKeys(m, cw('W')...); m.projects.focus != focusFiles {
		t.Errorf("<C-w>W from the tree: focus = %v, want the files sidebar", m.projects.focus)
	}
}

func TestPaneSessionFilesAndBack(t *testing.T) {
	m := pressKeys(workspaceSession(nil), cw('l')...)
	if m.projects.focus != focusFiles || m.mode != modeSession {
		t.Fatalf("<C-w>l in a session: focus = %v mode = %v, want the files sidebar", m.projects.focus, m.mode)
	}
	m = pressKeys(m, cw('h')...)
	if m.projects.focus == focusFiles || m.mode != modeSession {
		t.Errorf("<C-w>h from the session files sidebar: focus = %v mode = %v, want the transcript", m.projects.focus, m.mode)
	}
}

func TestPaneDownFocusesTheDockOnlyWithAPrompt(t *testing.T) {
	m := pressKeys(sessionModel(&session.Interaction{Kind: session.InteractionPermission}), cw('j')...)
	if m.focus != focusDock {
		t.Errorf("<C-w>j with a prompt: focus = %v, want the dock", m.focus)
	}
	m = pressKeys(sessionModel(nil), cw('j')...)
	if m.focus != focusHistory {
		t.Errorf("<C-w>j with no prompt: focus = %v, want no change", m.focus)
	}
}

func TestPaneCycleOnSession(t *testing.T) {
	m := pressKeys(workspaceSession(&session.Interaction{Kind: session.InteractionPermission}), cw('w')...)
	if m.projects.focus != focusFiles {
		t.Fatalf("<C-w>w from the transcript: focus = %v, want the files sidebar", m.projects.focus)
	}
	m = pressKeys(m, cw('w')...)
	if m.focus != focusDock || m.projects.focus == focusFiles {
		t.Errorf("<C-w>w from the files sidebar: focus = %v files focus = %v, want the dock", m.focus, m.projects.focus)
	}
	m = pressKeys(workspaceSession(&session.Interaction{Kind: session.InteractionPermission}), cw('W')...)
	if m.focus != focusDock {
		t.Errorf("<C-w>W from the transcript: focus = %v, want the dock", m.focus)
	}
}

func TestPaneLeftFromSessionOpensTheWorkspaceRow(t *testing.T) {
	m := workspaceSession(nil)
	m.projects.cursor = 0
	m = pressKeys(m, cw('h')...)
	if m.mode != modeProjects || m.projects.focus != focusTree {
		t.Fatalf("<C-w>h from a session: mode = %v focus = %v", m.mode, m.projects.focus)
	}
	if got := m.projects.cursorRowID(); got != "n1:w1" {
		t.Errorf("<C-w>h from a session: row = %q, want the session's workspace n1:w1", got)
	}
	if m.activeSub.subID != "" {
		t.Errorf("leaving the session should end its transcript subscription: %+v", m.activeSub)
	}
}

func TestPaneLeftFromDetailOpensTheWorkspaceRow(t *testing.T) {
	m := workspaceSession(nil)
	m.historyView = histDetail
	m.enterDetail()
	m.projects.cursor = 0
	if m.screen() != "detail" {
		t.Fatalf("setup: screen = %q, want detail", m.screen())
	}
	m = pressKeys(m, cw('h')...)
	if m.mode != modeProjects || !m.treeFocused() || m.projects.cursorRowID() != "n1:w1" {
		t.Errorf("<C-w>h from the detail: mode = %v focus = %v row = %q, want the tree on n1:w1",
			m.mode, m.projects.focus, m.projects.cursorRowID())
	}
}

func TestPaneLeftFromASubagentEndsBothStreams(t *testing.T) {
	m := workspaceSession(nil)
	m.historyView = histDetail
	m.enterDetail()
	m.sessionSub = subRef{subID: "session-sub", sessionID: "n1:s1"}
	m.activeSub = subRef{subID: "agent-sub", sessionID: "n1:s1", agentID: "agent42"}
	if m.screen() != "detail" {
		t.Fatalf("setup: screen = %q, want detail", m.screen())
	}
	m = pressKeys(m, cw('h')...)
	if m.mode != modeProjects || m.activeSub.subID != "" || m.sessionSub.subID != "" {
		t.Errorf("<C-w>h from a subagent: mode = %v activeSub = %+v sessionSub = %+v, want both streams ended",
			m.mode, m.activeSub, m.sessionSub)
	}
}

func TestPaneLeftFromSessionWithHiddenWorkspaceOpensTheHomeRow(t *testing.T) {
	m := workspaceSession(nil)
	s := m.sessions["n1:s1"]
	s.WorkspaceID = "n1:gone"
	m.sessions["n1:s1"] = s
	m.projects.selectRow("n1:w2")
	m = pressKeys(m, cw('h')...)
	if m.mode != modeProjects || !m.treeFocused() || m.projects.cursorRowID() != homeRowID {
		t.Errorf("<C-w>h from a session outside the tree: mode = %v focus = %v row = %q, want the tree on Home",
			m.mode, m.projects.focus, m.projects.cursorRowID())
	}
}

func TestPaneLeftFromHomeScreensOpensTheHomeRow(t *testing.T) {
	for _, mode := range []viewMode{modeList, modeHistoryProjects, modeLogs, modeHistoryTranscript} {
		m := homeTestModel()
		m.mode = mode
		m.projects.rebuild()
		m.projects.cursor = 2
		m = pressKeys(m, cw('h')...)
		if m.mode != modeProjects || !m.treeFocused() || m.projects.cursorRowID() != homeRowID {
			t.Errorf("mode %v: <C-w>h: mode = %v focus = %v row = %q, want the tree on Home",
				mode, m.mode, m.projects.focus, m.projects.cursorRowID())
		}
	}
}

func TestPaneLeftInTheViewerDoesNothing(t *testing.T) {
	m := homeTestModel()
	m.mode, m.viewer = modeHistoryTranscript, true
	m = pressKeys(m, cw('h')...)
	if m.mode != modeHistoryTranscript {
		t.Errorf("<C-w>h in the viewer: mode = %v, want no change", m.mode)
	}
}

func TestPaneKeysGoToTheLiveScreen(t *testing.T) {
	m := testModel()
	m.mode, m.termID = modeScreen, "t1"
	m.termKeyCh = make(chan termKey, 4)
	m = pressKeys(m, cw('h')...)
	if m.mode != modeScreen {
		t.Fatalf("<C-w>h on the live screen: mode = %v, want no change", m.mode)
	}
	if k := <-m.termKeyCh; string(k.data) != "\x17" {
		t.Errorf("<C-w> should go to the pane as ^W: %q", k.data)
	}
}

func TestPaneLeftFromSessionKeepsTheOpenFile(t *testing.T) {
	m := workspaceSession(nil)
	m.projects.fileView = fileViewState{ws: "n1:w1", path: "a.go"}
	m = pressKeys(m, cw('h')...)
	if m.mode != modeProjects || !m.projects.fileView.open() || m.projects.fileView.path != "a.go" {
		t.Errorf("<C-w>h keeps what the main pane shows: mode = %v file = %+v", m.mode, m.projects.fileView)
	}
}
