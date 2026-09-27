package tui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
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
	m = withFocus(m, mainPane)
	mm, _ := m.enterSession("n1:s1")
	m, _ = mm.syncSidebar()
	return withTreeLoaded(m, "n1:w1")
}

func TestPaneRightFromTreeThenFiles(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('l')...)
	if m.focused != mainPane {
		t.Fatalf("<C-w>l from the tree: focus = %v, want the pane", m.focused)
	}
	m = pressKeys(m, cw('l')...)
	if m.focused != rightSidebar {
		t.Errorf("<C-w>l from the pane: focus = %v, want the files sidebar", m.focused)
	}
}

func TestPaneRightWithoutWorkspaceDoesNothing(t *testing.T) {
	m := wideWorkspace()
	m = selectRow(m, "n1:p1")
	m = withFocus(m, mainPane)
	m = pressKeys(m, cw('l')...)
	if m.focused != mainPane {
		t.Errorf("<C-w>l from the pane with no workspace: focus = %v, want no change", m.focused)
	}
}

func TestPaneLeftFromFilesThenPane(t *testing.T) {
	m := pressKeys(filesFocused(), cw('h')...)
	if m.focused != mainPane || viewOf(m) != viewTree {
		t.Fatalf("<C-w>h from the files sidebar: focus = %v view = %v, want the pane", m.focused, viewOf(m))
	}
	m = pressKeys(m, cw('h')...)
	if m.focused != leftSidebar || m.left.tree.cursorRowID() != "n1:w1" {
		t.Errorf("<C-w>h from the pane: focus = %v row = %q, want the tree on n1:w1", m.focused, m.left.tree.cursorRowID())
	}
}

func TestPaneLeftFromTreeDoesNothing(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('h')...)
	if m.focused != leftSidebar || viewOf(m) != viewTree {
		t.Errorf("<C-w>h from the tree: focus = %v view = %v, want no change", m.focused, viewOf(m))
	}
}

func TestPaneLeftWithTreeHiddenDoesNothing(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	m.left.hidden = true
	m = pressKeys(m, cw('h')...)
	if m.focused != mainPane || viewOf(m) != viewTree {
		t.Errorf("<C-w>h with the tree hidden: focus = %v view = %v, want no change", m.focused, viewOf(m))
	}
}

func TestPaneCycleOnProjects(t *testing.T) {
	m := wideWorkspace()
	for _, want := range []container{mainPane, rightSidebar, leftSidebar} {
		if m = pressKeys(m, cw('w')...); m.focused != want {
			t.Fatalf("<C-w>w: focus = %v, want %v", m.focused, want)
		}
	}
	if m = pressKeys(m, cw('W')...); m.focused != rightSidebar {
		t.Errorf("<C-w>W from the tree: focus = %v, want the files sidebar", m.focused)
	}
}

func TestPaneSessionFilesAndBack(t *testing.T) {
	m := pressKeys(workspaceSession(nil), cw('l')...)
	if m.focused != rightSidebar || viewOf(m) != viewSession {
		t.Fatalf("<C-w>l in a session: focus = %v view = %v, want the files sidebar", m.focused, viewOf(m))
	}
	m = pressKeys(m, cw('h')...)
	if m.focused == rightSidebar || viewOf(m) != viewSession {
		t.Errorf("<C-w>h from the session files sidebar: focus = %v view = %v, want the transcript", m.focused, viewOf(m))
	}
}

func TestPaneDownFocusesTheDockOnlyWithAPrompt(t *testing.T) {
	m := pressKeys(sessionModel(&session.Interaction{Kind: session.InteractionPermission}), cw('j')...)
	if m.focused != sessionDock {
		t.Errorf("<C-w>j with a prompt: focus = %v, want the dock", m.focused)
	}
	m = pressKeys(sessionModel(nil), cw('j')...)
	if m.focused != mainPane {
		t.Errorf("<C-w>j with no prompt: focus = %v, want no change", m.focused)
	}
}

func TestPaneCycleOnSession(t *testing.T) {
	m := pressKeys(workspaceSession(&session.Interaction{Kind: session.InteractionPermission}), cw('w')...)
	if m.focused != rightSidebar {
		t.Fatalf("<C-w>w from the transcript: focus = %v, want the files sidebar", m.focused)
	}
	m = pressKeys(m, cw('w')...)
	if m.focused != sessionDock {
		t.Errorf("<C-w>w from the files sidebar: focus = %v, want the dock", m.focused)
	}
	m = pressKeys(workspaceSession(&session.Interaction{Kind: session.InteractionPermission}), cw('W')...)
	if m.focused != sessionDock {
		t.Errorf("<C-w>W from the transcript: focus = %v, want the dock", m.focused)
	}
}

func TestPaneLeftFromSessionOpensTheWorkspaceRow(t *testing.T) {
	m := workspaceSession(nil)
	rc := &recordingClient{}
	m.client = rc
	sub := trOf(m).activeSub
	m.left.tree.cursor = 0
	m = paneLeftRun(m)
	if viewOf(m) != viewTree || m.focused != leftSidebar {
		t.Fatalf("<C-w>h from a session: view = %v focus = %v", viewOf(m), m.focused)
	}
	if got := m.left.tree.cursorRowID(); got != "n1:w1" {
		t.Errorf("<C-w>h from a session: row = %q, want the session's workspace n1:w1", got)
	}
	if !slices.Contains(rc.subIDs(api.MethodTranscriptUnsubscribe), sub.subID) {
		t.Errorf("leaving the session should end its transcript subscription: %+v", sub)
	}
}

// paneLeftRun presses focus left and runs the command of its last key.
func paneLeftRun(m model) model {
	keys := cw('h')
	m = pressKeys(m, keys[:len(keys)-1]...)
	m, cmd := upd(m, keys[len(keys)-1])
	runCmd(cmd)
	return m
}

func TestPaneLeftFromDetailOpensTheWorkspaceRow(t *testing.T) {
	m := workspaceSession(nil)
	m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
	m, _ = onTr(m, func(v tview) tea.Cmd { v.enterDetail(); return nil })
	m.left.tree.cursor = 0
	if m.screen() != "transcript" || trOf(m).historyView != histDetail {
		t.Fatalf("setup: screen = %q, want the transcript's detail", m.screen())
	}
	m = pressKeys(m, cw('h')...)
	if viewOf(m) != viewTree || !m.treeFocused() || m.left.tree.cursorRowID() != "n1:w1" {
		t.Errorf("<C-w>h from the detail: view = %v focus = %v row = %q, want the tree on n1:w1",
			viewOf(m), m.focused, m.left.tree.cursorRowID())
	}
}

func TestPaneLeftFromASubagentEndsBothStreams(t *testing.T) {
	m := workspaceSession(nil)
	rc := &recordingClient{}
	m.client = rc
	m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
	m, _ = onTr(m, func(v tview) tea.Cmd { v.enterDetail(); return nil })
	m = withTr(m, func(t *transcriptComp) {
		t.sessionSub = subRef{subID: "session-sub", sessionID: "n1:s1"}
		t.activeSub = subRef{subID: "agent-sub", sessionID: "n1:s1", agentID: "agent42"}
	})
	if m.screen() != "transcript" || trOf(m).historyView != histDetail {
		t.Fatalf("setup: screen = %q, want the transcript's detail", m.screen())
	}
	m = paneLeftRun(m)
	unsubscribed := rc.subIDs(api.MethodTranscriptUnsubscribe)
	if viewOf(m) != viewTree || !slices.Contains(unsubscribed, "agent-sub") || !slices.Contains(unsubscribed, "session-sub") {
		t.Errorf("<C-w>h from a subagent: view = %v unsubscribed = %v, want both streams ended",
			viewOf(m), unsubscribed)
	}
}

func TestPaneLeftFromSessionWithHiddenWorkspaceOpensTheHomeRow(t *testing.T) {
	m := workspaceSession(nil)
	s := m.sessions["n1:s1"]
	s.WorkspaceID = "n1:gone"
	m.sessions["n1:s1"] = s
	m = selectRow(m, "n1:w2")
	m = pressKeys(m, cw('h')...)
	if viewOf(m) != viewTree || !m.treeFocused() || m.left.tree.cursorRowID() != homeRowID {
		t.Errorf("<C-w>h from a session outside the tree: view = %v focus = %v row = %q, want the tree on Home",
			viewOf(m), m.focused, m.left.tree.cursorRowID())
	}
}

func TestPaneLeftFromHomeScreensOpensTheHomeRow(t *testing.T) {
	for _, view := range []shownView{viewHome, viewHistoryProjects, viewLogs, viewHistoryTranscript} {
		m := homeTestModel()
		m = withView(m, view)
		m.left.tree.rebuild()
		m.left.tree.cursor = 2
		m = pressKeys(m, cw('h')...)
		if viewOf(m) != viewTree || !m.treeFocused() || m.left.tree.cursorRowID() != homeRowID {
			t.Errorf("view %v: <C-w>h: view = %v focus = %v row = %q, want the tree on Home",
				view, viewOf(m), m.focused, m.left.tree.cursorRowID())
		}
	}
}

func TestPaneLeftInTheViewerDoesNothing(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewHistoryTranscript)
	m.viewer = true
	m = pressKeys(m, cw('h')...)
	if viewOf(m) != viewHistoryTranscript {
		t.Errorf("<C-w>h in the viewer: view = %v, want no change", viewOf(m))
	}
}

func TestPaneKeysGoToTheLiveScreen(t *testing.T) {
	m := testModel()
	m = withView(m, viewScreen)
	m = withTerm(m, "t1", nil)
	m.termKeyCh = make(chan termKey, 4)
	m = pressKeys(m, cw('h')...)
	if viewOf(m) != viewScreen {
		t.Fatalf("<C-w>h on the live screen: view = %v, want no change", viewOf(m))
	}
	if k := <-m.termKeyCh; string(k.data) != "\x17" {
		t.Errorf("<C-w> should go to the pane as ^W: %q", k.data)
	}
}

func TestPaneLeftFromSessionKeepsTheOpenFile(t *testing.T) {
	m := workspaceSession(nil)
	m = withFile(m, fileComp{ws: "n1:w1", path: "a.go"})
	m = pressKeys(m, cw('h')...)
	if viewOf(m) != viewTree || !m.hasOpenFile() || fileOf(m).path != "a.go" {
		t.Errorf("<C-w>h keeps what the main pane shows: view = %v file = %+v", viewOf(m), fileOf(m))
	}
}
