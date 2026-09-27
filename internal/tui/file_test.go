package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func assertFileOnTop(t *testing.T, step string, m model, below shownView, path string) {
	t.Helper()
	f, ok := m.main.top().(fileComp)
	if !ok || f.path != path {
		t.Fatalf("%s: top = %#v, want the file %q", step, m.main.top(), path)
	}
	if under := m.main[len(m.main)-2]; !standsFor(m, under, below) {
		t.Fatalf("%s: under the file = %#v, want %v", step, under, below)
	}
	if viewOf(m) != below {
		t.Fatalf("%s: view = %v, want %v under the file", step, viewOf(m), below)
	}
}

func TestFileOpensOverProjectsAndEscPopsIt(t *testing.T) {
	m := filesFocused()
	n := len(m.main)
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.main) != n+1 {
		t.Fatalf("opening a file should push it: stack %d, want %d", len(m.main), n+1)
	}
	assertFileOnTop(t, "open", m, viewTree, "go.mod")
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(m.main) != n || !isWorkspace(m.main.top()) || m.focused != rightSidebar {
		t.Errorf("esc should pop the file back to the workspace and the sidebar: stack %v focus %v", m.main, m.focused)
	}
}

func TestFileOpensOverSessionAndEscPopsIt(t *testing.T) {
	m := workspaceSession(nil)
	m = withFocus(m, rightSidebar)
	n := len(m.main)
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.main) != n+1 {
		t.Fatalf("opening a file should push it: stack %d, want %d", len(m.main), n+1)
	}
	assertFileOnTop(t, "open", m, viewSession, "go.mod")
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(m.main) != n || !standsFor(m, m.main.top(), viewSession) || m.focused != rightSidebar {
		t.Errorf("esc should pop the file back to the transcript and the sidebar: stack %v focus %v", m.main, m.focused)
	}
}

func TestReadFileForAnotherFileIsDropped(t *testing.T) {
	m := filesFocused()
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, msg := range []readFileMsg{
		{ws: "n1:w2", path: "go.mod", content: "other workspace"},
		{ws: "n1:w1", path: "go.sum", content: "other path"},
	} {
		m, _ = upd(m, msg)
		if f, _ := m.openFile(); !f.loading || len(f.lines) != 0 {
			t.Errorf("%+v should not fill the open file: %+v", msg, f)
		}
	}
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "module argus"})
	if f, _ := m.openFile(); f.loading || len(f.lines) != 1 {
		t.Errorf("the file's own read should fill it: %+v", f)
	}
}

func TestDiffStepReplacesTheTopFile(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go"}, api.ChangedFile{Path: "b.go"})
	m, _ = upd(m, keyMsg("enter"))
	n := len(m.main)
	m = typeKeys(m, "]f")
	if len(m.main) != n {
		t.Fatalf("]f should replace the open diff, not push: stack %d, want %d", len(m.main), n)
	}
	assertFileOnTop(t, "]f", m, viewTree, "b.go")
	m = typeKeys(m, "[f")
	if len(m.main) != n {
		t.Fatalf("[f should replace the open diff, not push: stack %d, want %d", len(m.main), n)
	}
	assertFileOnTop(t, "[f", m, viewTree, "a.go")
}

func TestLeavingTheSessionUnderAFileKeepsIt(t *testing.T) {
	m := workspaceSession(nil)
	m = withFocus(m, rightSidebar)
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.leaveView()
	assertFileOnTop(t, "leave the session", m, viewTree, "go.mod")
	m, _ = m.enterSession("n1:s1")
	assertFileOnTop(t, "enter a session", m, viewSession, "go.mod")
	m, _ = m.enterSession("n1:s3")
	assertFileOnTop(t, "switch the session", m, viewSession, "go.mod")
	if len(m.main) != 3 {
		t.Errorf("stack = %v, want [projects session file]", m.main)
	}
}

func TestLiveScreenRoundTripKeepsTheOpenFile(t *testing.T) {
	m := workspaceSession(&session.Interaction{Kind: session.InteractionPermission})
	s := m.sessions["n1:s1"]
	s.CanOpenTerminal = true
	m.sessions["n1:s1"] = s
	m = withFocus(m, rightSidebar)
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assertFileOnTop(t, "open", m, viewSession, "go.mod")
	m = withFocus(m, sessionDock)
	m = pressKeys(m, ctrlKey('t'))
	assertView(t, "<C-t>", m, viewScreen)
	m = pressKeys(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	assertView(t, "^]", m, viewSession)
	if f := fileOf(m); !m.hasOpenFile() || f.path != "go.mod" {
		t.Errorf("^] should return to the session with the file still open: %+v", f)
	}
}

func TestLiveScreenOpensOverTheOpenFile(t *testing.T) {
	m := workspaceSession(&session.Interaction{Kind: session.InteractionPermission})
	s := m.sessions["n1:s1"]
	s.CanOpenTerminal = true
	m.sessions["n1:s1"] = s
	m = withFocus(m, rightSidebar)
	m = pressKeys(m, keyMsg("enter"))
	assertFileOnTop(t, "open", m, viewSession, "go.mod")
	m = pressKeys(m, keyMsg("tab"), ctrlKey('t'))
	assertView(t, "<C-t>", m, viewScreen)
	if _, ok := m.main.top().(fileComp); ok {
		t.Fatal("<C-t>: the file stays on top, want the live screen over it")
	}
	bare := m
	c := &ctx{m: &bare}
	c.closeFile()
	bare.apply(c)
	if got, want := m.View().Content, bare.View().Content; got != want {
		t.Errorf("the live screen draws differently over an open file:\n got: %q\nwant: %q", got, want)
	}

	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "module argus"})
	m = pressKeys(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	assertFileOnTop(t, "^]", m, viewSession, "go.mod")
	if f := fileOf(m); f.loading || len(f.lines) != 1 {
		t.Errorf("^]: the file = %+v, want the content read while the live screen showed", f)
	}
	if m.focused != sessionDock {
		t.Errorf("^]: focus = %v, want the dock", m.focused)
	}
}

// A resume reply while a file is open over a live transcript replaces the
// transcript under the file, and the file stays on top. A file never stays over
// a Home tab: with no workspace, the right sidebar drops it.
func TestResumeUnderAnOpenFileKeepsTheFileOnTop(t *testing.T) {
	m := workspaceSession(nil)
	m = withFocus(m, rightSidebar)
	m = pressKeys(m, keyMsg("enter"))
	assertFileOnTop(t, "open", m, viewSession, "go.mod")
	n := len(m.main)
	m, _ = upd(m, resumeResultMsg{sessionID: "n1:s3"})
	assertFileOnTop(t, "the resume reply", m, viewSession, "go.mod")
	if len(m.main) != n || trOf(m).sessionID != "n1:s3" {
		t.Fatalf("the resume reply: stack %d session %q, want %d with n1:s3 under the file", len(m.main), trOf(m).sessionID, n)
	}
	m = pressKeys(m, keyMsg("esc"))
	if t2, ok := m.main.top().(transcriptComp); !ok || t2.sessionID != "n1:s3" {
		t.Errorf("<Esc> on the file: top = %#v, want n1:s3's transcript", m.main.top())
	}
}

func TestFileTextExpandsTabsToTabStops(t *testing.T) {
	for in, want := range map[string]string{
		"\tx":       "    x",
		"ab\tx":     "ab  x",
		"abcd\tx":   "abcd    x",
		"a\n\tb\tc": "a\n    b   c",
		"世\tx":      "世  x",
		"no tabs\n": "no tabs\n",
	} {
		if got := fileText(in); got != want {
			t.Errorf("fileText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileTextDropsEscapeSequences(t *testing.T) {
	if got := fileText("\x1b[31mred\x1b[0m\tx\x1b]0;title\x07\r\n"); got != "red x\n" {
		t.Errorf("fileText = %q, want the escape sequences and controls gone", got)
	}
}

func TestFileViewKeepsTabs(t *testing.T) {
	m := filesFocused()
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "a\tb\n\tc"})
	f, _ := m.openFile()
	if got := ansi.Strip(strings.Join(f.lines, "\n")); got != "a   b\n    c" {
		t.Errorf("file lines = %q, want the tabs expanded to tab stops", got)
	}
}

func TestFileErrorFitsThePane(t *testing.T) {
	m := filesFocused()
	f := fileComp{ws: "n1:w1", path: "go.mod", err: errors.New(strings.Repeat("x", 300))}
	for _, ln := range strings.Split(ansi.Strip(f.content(&ctx{m: &m}, 40, 10)), "\n") {
		if ansi.StringWidth(ln) > 40 {
			t.Errorf("error line is %d cells, want at most 40: %q", ansi.StringWidth(ln), ln)
		}
	}
}

func TestSetupLogRefetchesWhileTheSetupRuns(t *testing.T) {
	m := withSetup(projectsTestModel(), 1, &api.ScriptRun{State: "running"})
	m.width, m.height = 120, 30
	rc := &recordingClient{}
	m.client = rc
	m = selectRow(m, "n1:w2")
	m, _ = upd(m, keyMsg("L"))
	m, cmd := upd(m, setupLogMsg{ws: "n1:w2", output: "installing"})
	f, _ := m.openFile()
	if cmd == nil || !f.live {
		t.Fatal("a log reply while the setup runs should schedule a refetch")
	}
	if _, cmd := upd(m, setupLogTickMsg{ws: "n1:w2", tick: f.tick - 1}); cmd != nil {
		t.Error("a stale tick should not fetch")
	}
	n := len(rc.calledMethods())
	_, cmd = upd(m, setupLogTickMsg{ws: "n1:w2", tick: f.tick})
	runCmd(cmd)
	if calls := rc.calledMethods(); len(calls) != n+1 || calls[n] != api.MethodWorkspaceSetupLog {
		t.Fatalf("the tick should fetch the log: calls=%v", calls)
	}
	p := m.left.tree.data[0]
	p.Workspaces = slices.Clone(p.Workspaces)
	p.Workspaces[1].Setup = &api.ScriptRun{State: "done"}
	m, cmd = upd(m, projectsTreeMsg{tree: []api.ProjectNode{p}})
	runCmd(cmd)
	if f, _ := m.openFile(); f.live || rc.calledMethods()[len(rc.calledMethods())-1] != api.MethodWorkspaceSetupLog {
		t.Fatalf("the end of the setup should fetch the log's last lines once: calls=%v", rc.calledMethods())
	}
	if _, cmd := upd(m, setupLogMsg{ws: "n1:w2", output: "installing\ndone"}); cmd != nil {
		t.Error("a log reply after the setup ended should not schedule a refetch")
	}
}

func TestFilesReloadDropsFoldedListings(t *testing.T) {
	rc := &recordingClient{}
	m := filesFocused()
	m.client = rc
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{{Name: "a", Path: "a", IsDir: true}}})
	m, _ = upd(m, keyMsg("l"))
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "a", entries: []api.DirEntry{{Name: "old.go", Path: "a/old.go"}}})
	m, _ = upd(m, keyMsg("h"))
	m, _ = typeKeysCmd(m, "gr")
	if _, ok := m.right.fileTree.dirs["a"]; ok {
		t.Fatal("gr should drop the listing of the folded a")
	}
	rc.calls, rc.params = nil, nil
	_, cmd := upd(m, keyMsg("l"))
	runCmd(cmd)
	if p, ok := paramsFor(m, api.MethodWorkspaceListDir).(api.WorkspaceFileParams); !ok || p.Path != "a" {
		t.Errorf("unfolding a after gr should fetch it again: calls=%v", rc.calls)
	}
}
