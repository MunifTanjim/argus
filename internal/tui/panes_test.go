package tui

import (
	"context"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
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

// Typing fast, ctrl is often still held for the second key.
func TestPaneKeysWithCtrlHeld(t *testing.T) {
	m := wideWorkspace()
	for _, step := range []struct {
		r    rune
		want container
	}{{'l', mainPane}, {'l', rightSidebar}, {'h', mainPane}, {'w', rightSidebar}, {'w', leftSidebar}} {
		if m = pressKeys(m, ctrlKey('w'), ctrlKey(step.r)); m.focused != step.want {
			t.Fatalf("<C-w><C-%c>: focus = %v, want %v", step.r, m.focused, step.want)
		}
	}
	m = pressKeys(sessionModel(&session.Interaction{Kind: session.InteractionPermission}), ctrlKey('w'), ctrlKey('j'))
	if m.focused != sessionDock {
		t.Errorf("<C-w><C-j> with a prompt: focus = %v, want the dock", m.focused)
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

func TestPaneDownFocusesTheDock(t *testing.T) {
	m := pressKeys(sessionModel(&session.Interaction{Kind: session.InteractionPermission}), cw('j')...)
	if m.focused != sessionDock {
		t.Errorf("<C-w>j with a prompt: focus = %v, want the dock", m.focused)
	}
	m = pressKeys(sessionModel(nil), cw('j')...)
	if m.focused != sessionDock {
		t.Errorf("<C-w>j with no prompt: focus = %v, want the dock", m.focused)
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
	if m.focused != leftSidebar {
		t.Errorf("<C-w>W from the transcript: focus = %v, want the tree", m.focused)
	}
}

func TestPaneLeftFromSessionKeepsItAndMovesTheCursorToItsWorkspace(t *testing.T) {
	m := workspaceSession(nil)
	rc := &recordingClient{}
	m.client = rc
	sub := trOf(m).activeSub
	m.left.tree.cursor = 0
	m = paneLeftRun(m)
	if viewOf(m) != viewSession || m.focused != leftSidebar {
		t.Fatalf("<C-w>h from a session: view = %v focus = %v", viewOf(m), m.focused)
	}
	if got := m.left.tree.cursorRowID(); got != "n1:w1" {
		t.Errorf("<C-w>h from a session: row = %q, want the session's workspace n1:w1", got)
	}
	if slices.Contains(rc.subIDs(api.MethodTranscriptUnsubscribe), sub.subID) {
		t.Errorf("the tree over the session must keep its transcript subscription: %+v", sub)
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

func TestPaneLeftFromDetailKeepsTheDetail(t *testing.T) {
	m := workspaceSession(nil)
	m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
	m, _ = onTr(m, func(v tview) tea.Cmd { v.enterDetail(); return nil })
	m.left.tree.cursor = 0
	if m.screen() != "transcript" || trOf(m).historyView != histDetail {
		t.Fatalf("setup: screen = %q, want the transcript's detail", m.screen())
	}
	m = pressKeys(m, cw('h')...)
	if trOf(m).historyView != histDetail || !m.treeFocused() || m.left.tree.cursorRowID() != "n1:w1" {
		t.Errorf("<C-w>h from the detail: view = %v focus = %v row = %q, want the tree on n1:w1 over the detail",
			viewOf(m), m.focused, m.left.tree.cursorRowID())
	}
}

func TestPaneLeftFromASubagentKeepsBothStreams(t *testing.T) {
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
	if viewOf(m) != viewSession || len(unsubscribed) != 0 {
		t.Errorf("<C-w>h from a subagent: view = %v unsubscribed = %v, want both streams kept",
			viewOf(m), unsubscribed)
	}
}

func TestPaneLeftFromSessionWithAWorkspaceOutsideTheTreeKeepsTheCursor(t *testing.T) {
	m := workspaceSession(nil)
	s := m.sessions["n1:s1"]
	s.WorkspaceID = "n1:gone"
	m.sessions["n1:s1"] = s
	m.left.tree.selectRow("n1:w2")
	m = pressKeys(m, cw('h')...)
	if viewOf(m) != viewSession || !m.treeFocused() || m.left.tree.cursorRowID() != "n1:w2" {
		t.Errorf("<C-w>h from a session outside the tree: view = %v focus = %v row = %q, want the tree on n1:w2",
			viewOf(m), m.focused, m.left.tree.cursorRowID())
	}
}

func TestPaneLeftFromHomeScreensMovesTheCursorToHome(t *testing.T) {
	for _, view := range []shownView{viewHome, viewHistoryProjects, viewLogs, viewHistoryTranscript} {
		m := homeTestModel()
		m = withView(m, view)
		m.left.tree.rebuild()
		m.left.tree.cursor = 2
		m = pressKeys(m, cw('h')...)
		if viewOf(m) != view || !m.treeFocused() || m.left.tree.cursorRowID() != homeRowID {
			t.Errorf("view %v: <C-w>h: view = %v focus = %v row = %q, want the tree on Home over the view",
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
	if viewOf(m) != viewSession || !m.hasOpenFile() || fileOf(m).path != "a.go" {
		t.Errorf("<C-w>h keeps what the main pane shows: view = %v file = %+v", viewOf(m), fileOf(m))
	}
}

// pressTmuxNav presses keys on m with the tmux hand-off on, runs the command
// of the last key, and returns the model and the tmux selections it made.
func pressTmuxNav(t *testing.T, m model, keys ...tea.KeyPressMsg) (model, []string) {
	t.Helper()
	var got []string
	orig := selectTmuxPane
	selectTmuxPane = func(_ context.Context, pane, dir string) error {
		got = append(got, pane+" "+dir)
		return nil
	}
	t.Cleanup(func() { selectTmuxPane = orig })
	m.tmuxNavPane = "%7"
	m = pressKeys(m, keys[:len(keys)-1]...)
	m, cmd := upd(m, keys[len(keys)-1])
	execCmd(cmd)
	return m, got
}

func TestPaneKeysHandOffToTmuxAtTheEdge(t *testing.T) {
	permission := &session.Interaction{Kind: session.InteractionPermission}
	cases := []struct {
		name  string
		start func() model
		keys  []tea.KeyPressMsg
		want  container
		tmux  []string
	}{
		{"left from the tree", wideWorkspace, cw('h'), leftSidebar, []string{"%7 L"}},
		{"right from the files", wideWorkspace, slices.Concat(cw('l'), cw('l'), cw('l')), rightSidebar, []string{"%7 R"}},
		{"up from the pane", wideWorkspace, slices.Concat(cw('l'), cw('k')), mainPane, []string{"%7 U"}},
		{"down from a pane with no dock", wideWorkspace, slices.Concat(cw('l'), cw('j')), mainPane, []string{"%7 D"}},
		{"down to the dock", func() model { return workspaceSession(permission) }, cw('j'), sessionDock, nil},
		{"left to the tree", wideWorkspace, slices.Concat(cw('l'), cw('h')), leftSidebar, nil},
		{"next pane", wideWorkspace, cw('w'), mainPane, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, got := pressTmuxNav(t, tc.start(), tc.keys...)
			if m.focused != tc.want {
				t.Errorf("focus = %v, want %v", m.focused, tc.want)
			}
			if !slices.Equal(got, tc.tmux) {
				t.Errorf("tmux selections = %q, want %q", got, tc.tmux)
			}
		})
	}
}

func TestPaneKeysStayInTheTUIWithTmuxNavigationOff(t *testing.T) {
	orig := selectTmuxPane
	selectTmuxPane = func(context.Context, string, string) error {
		t.Error("tmux navigation is off, but the key went to tmux")
		return nil
	}
	t.Cleanup(func() { selectTmuxPane = orig })
	m, cmd := upd(pressKeys(wideWorkspace(), ctrlKey('w')), keyMsg("h"))
	execCmd(cmd)
	if m.focused != leftSidebar {
		t.Errorf("focus = %v, want the tree", m.focused)
	}
}

func TestMappedCtrlHHandsOffToTmux(t *testing.T) {
	m := wideWorkspace().withKeymaps(config.TUIConfig{Keymaps: strMaps(map[string]map[string]string{
		"global": {"<C-h>": ":focus left<CR>"},
	})})
	m, got := pressTmuxNav(t, m, ctrlKey('h'))
	if m.focused != leftSidebar || !slices.Equal(got, []string{"%7 L"}) {
		t.Errorf("<C-h> on the tree: focus = %v, tmux selections = %q", m.focused, got)
	}
}

func TestLiveScreenKeepsPaneKeysFromTmux(t *testing.T) {
	m := testModel()
	m = withView(m, viewScreen)
	m = withTerm(m, "t1", nil)
	m.termKeyCh = make(chan termKey, 4)
	_, got := pressTmuxNav(t, m, cw('h')...)
	if len(got) > 0 {
		t.Errorf("the live screen takes <C-w>h, but tmux got %q", got)
	}
}

func TestTmuxNavPane(t *testing.T) {
	cases := []struct {
		name       string
		on         bool
		tmux, pane string
		want       string
	}{
		{"on in tmux", true, "/tmp/tmux-1000/default,1,0", "%3", "%3"},
		{"off in tmux", false, "/tmp/tmux-1000/default,1,0", "%3", ""},
		{"on with no $TMUX", true, "", "%3", ""},
		{"on outside tmux", true, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMUX", tc.tmux)
			t.Setenv("TMUX_PANE", tc.pane)
			if got := tmuxNavPane(config.TUIConfig{TmuxNavigation: tc.on}); got != tc.want {
				t.Errorf("tmuxNavPane = %q, want %q", got, tc.want)
			}
		})
	}
}
