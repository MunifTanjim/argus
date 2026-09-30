package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func loadedTree() fileTree {
	t := newFileTree("n1:w1")
	t.setDir("", []api.DirEntry{
		{Name: "internal", Path: "internal", IsDir: true},
		{Name: "empty", Path: "empty", IsDir: true},
		{Name: "go.mod", Path: "go.mod"},
	}, nil)
	return t
}

func press(t *fileTree, s string) treeRequest {
	var msg tea.KeyPressMsg
	switch s {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	default:
		r := []rune(s)[0]
		msg = tea.KeyPressMsg{Code: r, Text: s}
	}
	m := withFocus(model{}, rightSidebar)
	return t.key(func(bs ...binding) bool { return m.matches(msg, bs...) }, 5)
}

func TestFileTreeRootRequestsLoadAndRows(t *testing.T) {
	ft := newFileTree("n1:w1")
	if rows := ft.rows(); len(rows) != 1 || rows[0].note != "loading…" {
		t.Fatalf("unloaded root should show one loading row: %+v", rows)
	}
	ft = loadedTree()
	if got := len(ft.rows()); got != 3 {
		t.Fatalf("root rows = %d, want 3", got)
	}
}

func TestFileTreeUnfoldLoadsThenFoldsAndGoesToParent(t *testing.T) {
	ft := loadedTree()
	req := press(&ft, "l") // cursor on "internal"
	if req.loadDir == nil || *req.loadDir != "internal" {
		t.Fatalf("unfolding an unloaded dir should request it: %+v", req)
	}
	if rows := ft.rows(); rows[1].note != "loading…" || rows[1].depth != 1 {
		t.Fatalf("a loading dir shows one indented loading row: %+v", rows)
	}
	ft.setDir("internal", []api.DirEntry{{Name: "tui", Path: "internal/tui", IsDir: true}}, nil)
	press(&ft, "j") // onto internal/tui
	if r := ft.rows()[ft.cursor]; r.entry.Path != "internal/tui" {
		t.Fatalf("cursor on %q, want internal/tui", r.entry.Path)
	}
	press(&ft, "h") // a folded dir: go to the parent row
	if r := ft.rows()[ft.cursor]; r.entry.Path != "internal" {
		t.Fatalf("h should move to the parent, cursor on %q", r.entry.Path)
	}
	press(&ft, "h") // an unfolded dir: fold
	if ft.expanded["internal"] || len(ft.rows()) != 3 {
		t.Fatalf("h on an unfolded dir should fold it: %+v", ft.rows())
	}
}

func TestFileTreeEmptyDirAndErrorRows(t *testing.T) {
	ft := loadedTree()
	press(&ft, "j") // "empty"
	press(&ft, "l")
	ft.setDir("empty", nil, nil)
	if r := ft.rows()[2]; r.note != "(empty)" {
		t.Errorf("an unfolded empty dir should show (empty), got %+v", r)
	}
	ft2 := loadedTree()
	press(&ft2, "l")
	ft2.setDir("internal", nil, errString("denied"))
	if r := ft2.rows()[1]; !strings.Contains(r.note, "denied") {
		t.Errorf("a failed dir should show its error, got %+v", r)
	}
}

func TestFileTreeEnterOpensFile(t *testing.T) {
	ft := loadedTree()
	press(&ft, "G") // go.mod
	req := press(&ft, "enter")
	if req.openFile == nil || *req.openFile != "go.mod" {
		t.Fatalf("enter on a file should request opening it: %+v", req)
	}
}

func TestFileTreeViewFitsAndMarksCursor(t *testing.T) {
	ft := loadedTree()
	ft.setDir("", []api.DirEntry{{Name: strings.Repeat("n", 90), Path: strings.Repeat("n", 90)}}, nil)
	out := ft.view(30, 10, true)
	for _, ln := range strings.Split(ansi.Strip(out), "\n") {
		if w := len([]rune(ln)); w > 30 {
			t.Errorf("row wider than the sidebar (%d): %q", w, ln)
		}
	}
	if !strings.Contains(ansi.Strip(out), "▌") {
		t.Errorf("view should mark the cursor:\n%s", ansi.Strip(out))
	}
}

func wideWorkspace() model {
	m := homeTestModel()
	m.width, m.height = 160, 30
	m.right.hidden = false
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	m.left.tree.rebuild()
	m = selectRow(m, "n1:w1")
	m, _ = m.syncSidebar() // as Update would after the cursor move
	return m
}

func upd(m model, msg tea.Msg) (model, tea.Cmd) {
	res, cmd := m.Update(msg)
	return res.(model), cmd
}

func TestRightSidebarFollowsWorkspaceAndLoadsRoot(t *testing.T) {
	m := wideWorkspace()
	m = typeKeys(m, "j") // to n1:w2
	m, cmd := upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.right.fileTree.ws != "n1:w2" || cmd == nil {
		t.Fatalf("tree should follow the workspace and load its root: ws=%q", m.right.fileTree.ws)
	}
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{{Name: "stale", Path: "stale"}}})
	if d := m.right.fileTree.dirs[""]; d != nil && len(d.entries) > 0 {
		t.Error("a listing for the old workspace must be ignored")
	}
	m, _ = upd(m, listDirMsg{ws: "n1:w2", dir: "", entries: []api.DirEntry{{Name: "a.go", Path: "a.go"}}})
	if !strings.Contains(ansi.Strip(m.View().Content), "a.go") {
		t.Error("the right sidebar should render the loaded root")
	}
}

func TestRightSidebarHintWithoutWorkspace(t *testing.T) {
	m := wideWorkspace()
	m = selectRow(m, "n1:p1")
	m, _ = upd(m, tea.KeyPressMsg{Code: 'k', Text: "k"}) // Home row
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Files") || !strings.Contains(out, "select a workspace") {
		t.Errorf("no workspace: the sidebar should show a hint:\n%s", out)
	}
}

func TestFocusCyclesThroughFiles(t *testing.T) {
	m := wideWorkspace()
	m = pressKeys(m, cw('w')...)
	if m.focused != mainPane {
		t.Fatalf("<C-w>w from the tree: focus=%v, want pane", m.focused)
	}
	m = pressKeys(m, cw('w')...)
	if m.focused != rightSidebar {
		t.Fatalf("<C-w>w from the pane: focus=%v, want files", m.focused)
	}
	m = pressKeys(m, cw('w')...)
	if m.focused != leftSidebar {
		t.Fatalf("<C-w>w from files: focus=%v, want tree", m.focused)
	}
	m = pressKeys(m, cw('W')...)
	if m.focused != rightSidebar {
		t.Errorf("<C-w>W from the tree: focus=%v, want files", m.focused)
	}
}

func TestHidingFilesMovesFocusAndWidensPane(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, rightSidebar)
	withFiles := m.bodyWidthFor(m.projectsLeftW())
	m = typeKeys(m, " e")
	if m.filesVisible() || m.focused != mainPane {
		t.Fatalf("␣e should hide files and move focus to the pane: visible=%v focus=%v", m.filesVisible(), m.focused)
	}
	if m.bodyWidthFor(m.projectsLeftW()) <= withFiles {
		t.Errorf("hiding files should widen the pane: %d -> %d", withFiles, m.bodyWidthFor(m.projectsLeftW()))
	}

	n := wideWorkspace()
	n = withFocus(n, rightSidebar)
	n, _ = upd(n, tea.WindowSizeMsg{Width: 110, Height: 30}) // below 120
	if n.filesVisible() || n.focused == rightSidebar {
		t.Errorf("auto-hide should take focus off the files panel: focus=%v", n.focused)
	}
}

func TestRightSidebarFitsAndResizes(t *testing.T) {
	m := wideWorkspace()
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{{Name: strings.Repeat("deep", 40), Path: "d"}}})
	if !strings.Contains(ansi.Strip(m.View().Content), "deepdeep") {
		t.Fatal("the long name should render (truncated) in the sidebar")
	}
	assertFits(t, m.View().Content, m.width)
	m = withFocus(m, rightSidebar)
	before := m.projectsFilesW()
	m = pressKeys(m, ctrlKey('w'), keyMsg(">"))
	if m.projectsFilesW() <= before {
		t.Errorf("^w> with files focused should widen the files sidebar: %d -> %d", before, m.projectsFilesW())
	}
}

func withTreeLoaded(m model, ws string) model {
	m, _ = upd(m, listDirMsg{ws: ws, dir: "", entries: []api.DirEntry{{Name: "go.mod", Path: "go.mod"}}})
	return m
}

func TestOpenFileFromTreeShowsInPane(t *testing.T) {
	m := withTreeLoaded(wideWorkspace(), "n1:w1")
	m = withFocus(m, rightSidebar)
	m, cmd := upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.hasOpenFile() || fileOf(m).path != "go.mod" || cmd == nil {
		t.Fatalf("enter on a file should open it and fetch: %+v", fileOf(m))
	}
	if m.focused != mainPane {
		t.Errorf("focus should move to the opened file: focus=%v", m.focused)
	}
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "module argus"})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "module argus") || !strings.Contains(out, "repo  main") {
		t.Errorf("the pane should show the file under the unchanged header:\n%s", out)
	}
	cursor := m.right.fileTree.cursor
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.hasOpenFile() || m.focused != rightSidebar || m.right.fileTree.cursor != cursor {
		t.Errorf("esc should close the file and return to its tree row: open=%v focus=%v cursor=%d",
			m.hasOpenFile(), m.focused, m.right.fileTree.cursor)
	}
}

func TestEscWithSidebarHiddenKeepsPaneFocus(t *testing.T) {
	m := openedFile(wideWorkspace())
	m = typeKeys(m, " e") // hide the sidebar
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.hasOpenFile() || m.focused != mainPane {
		t.Errorf("with the sidebar hidden, esc should close the file and keep the pane: open=%v focus=%v",
			m.hasOpenFile(), m.focused)
	}
}

func TestFileClosesWhenWorkspaceChanges(t *testing.T) {
	m := withTreeLoaded(wideWorkspace(), "n1:w1")
	m = withFocus(m, rightSidebar)
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = withFocus(m, leftSidebar)
	m = typeKeys(m, "j") // to n1:w2
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.hasOpenFile() {
		t.Error("changing the workspace should close the open file")
	}
}

func TestFocusRightFromSessionOpensFileInPlaceOfTranscript(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	mm, _ := m.enterSession("n1:s1") // session in n1:w1
	m, _ = mm.syncSidebar()
	m = withTreeLoaded(m, "n1:w1")
	m = pressKeys(m, cw('l')...)
	if m.focused != rightSidebar {
		t.Fatalf("<C-w>l in a session should focus the file tree: focus=%v", m.focused)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "module argus"})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "module argus") || m.focused == rightSidebar {
		t.Fatalf("the file should replace the transcript and take focus: focus=%v\n%s", m.focused, out)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape}) // close the file
	if m.hasOpenFile() || m.focused != rightSidebar || viewOf(m) != viewSession {
		t.Fatalf("esc should close the file and return to the tree: open=%v focus=%v view=%v",
			m.hasOpenFile(), m.focused, viewOf(m))
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape}) // tree → session
	if m.focused == rightSidebar || viewOf(m) != viewSession {
		t.Errorf("esc in the tree should return to the session: focus=%v view=%v", m.focused, viewOf(m))
	}
}

func TestFooterAndHelpListFileKeys(t *testing.T) {
	m := wideWorkspace()
	m.width = 200
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "␣e files") {
		t.Errorf("tree footer should list ␣e files: %q", f)
	}
	m.showHelp = true
	h := ansi.Strip(m.projectsHelpView())
	for _, want := range []string{"toggle right sidebar", "^wh/^wl", "^wW", "gT/gt"} {
		if !strings.Contains(h, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func filesFocused() model {
	m := withTreeLoaded(wideWorkspace(), "n1:w1")
	m = withFocus(m, rightSidebar)
	return m
}

func TestFilesEscReturnsToPaneOnProjectsScreen(t *testing.T) {
	m := filesFocused()
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.focused != mainPane || viewOf(m) != viewTree {
		t.Errorf("esc in files: focus=%v view=%v, want the pane on the projects screen", m.focused, viewOf(m))
	}
	h := filesFocused()
	h.left.hidden = true
	h, _ = upd(h, tea.KeyPressMsg{Code: tea.KeyEscape})
	if viewOf(h) != viewTree || h.left.tree.cursorRowID() != "n1:w1" {
		t.Errorf("esc in files with the left sidebar hidden must not jump to Home: view=%v row=%q", viewOf(h), h.left.tree.cursorRowID())
	}
}

func TestFilesReloadRefetchesRootAndUnfoldedDirs(t *testing.T) {
	rc := &recordingClient{}
	m := filesFocused()
	m.client = rc
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{{Name: "a", Path: "a", IsDir: true}}})
	m, cmd := upd(m, tea.KeyPressMsg{Code: 'l', Text: "l"}) // unfold a
	execCmd(cmd)
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "a", entries: []api.DirEntry{{Name: "x.go", Path: "a/x.go"}}})
	rc.calls, rc.params = nil, nil
	m, cmd = typeKeysCmd(m, "gr")
	execCmd(cmd)
	var dirs []string
	for i, c := range rc.calls {
		if c == api.MethodWorkspaceListDir {
			dirs = append(dirs, rc.params[i].(api.WorkspaceFileParams).Path)
		}
	}
	if len(dirs) != 2 {
		t.Fatalf("gr should re-fetch the root and the unfolded dir, fetched %q", dirs)
	}
	if rc.calls[0] == api.MethodProjectList || m.focused != rightSidebar {
		t.Errorf("gr in files must reload the tree, not the project list; focus=%v", m.focused)
	}
}

func TestSessionFilesKeysShareHandler(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	mm, _ := m.enterSession("n1:s1")
	m, _ = mm.syncSidebar()
	m = withTreeLoaded(m, "n1:w1")
	m = pressKeys(m, cw('l')...)
	before := m.projectsFilesW()
	m = pressKeys(m, ctrlKey('w'), keyMsg(">"))
	if m.projectsFilesW() <= before {
		t.Errorf("^w> in the session's file tree should widen it: %d -> %d", before, m.projectsFilesW())
	}
}

func TestCtrlBKeepsFilesFocus(t *testing.T) {
	m := filesFocused()
	m = typeKeys(m, " o")
	if m.focused != rightSidebar || m.sidebarVisible() {
		t.Errorf("␣o from files should hide the left sidebar and keep files focused: focus=%v", m.focused)
	}
}

func TestFilesFocusFallsBackToPaneWithoutWorkspace(t *testing.T) {
	m := filesFocused()
	m = selectRow(m, "n1:p1") // e.g. a filter moved the cursor
	m = m.repairFocus()
	m, _ = m.syncSidebar()
	if m.focused != mainPane {
		t.Errorf("no workspace with the left tree visible: focus=%v, want the pane", m.focused)
	}
}

func TestProjectsFooterUsesFullWidth(t *testing.T) {
	m := wideWorkspace()
	m.left.tree.setFilter("repo")
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "? help") {
		t.Errorf("the projects footer spans the terminal and should fit ? help at 160 cols: %q", f)
	}
}

func openedFile(m model) model {
	m = withTreeLoaded(m, "n1:w1")
	m = withFocus(m, rightSidebar)
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "module argus"})
	return m
}

// A file open over a workspace pane passes the keys that act on the tree to the
// pane, which runs each once; the pane's own list keys stay with the file.
func TestFileOverAWorkspaceLeavesTheTreeKeys(t *testing.T) {
	base := func() model {
		m := withFocus(openedFile(wideWorkspace()), mainPane)
		m.client = &recordingClient{}
		return m
	}
	keys := [][]tea.KeyPressMsg{
		{keyMsg("/")}, {keyMsg("s")}, {keyMsg("L")}, {keyMsg("z"), keyMsg(".")}, {keyMsg("z"), keyMsg("g")},
		cw('>'), cw('<'), {keyMsg("O")}, {keyMsg("enter")}, {keyMsg("d"), keyMsg("d")},
	}
	for _, k := range []string{"a", "r", "H", "P", "T", "D", "F", "S"} {
		keys = append(keys, []tea.KeyPressMsg{keyMsg(k)})
	}
	for _, ks := range keys {
		m := pressKeys(base(), ks...)
		tree := m.left.tree
		if !m.hasOpenFile() || fileOf(m).log || spawnOpen(m) || m.focused != mainPane || tree.inputMode != pmNone ||
			tree.showHidden || tree.showGone || m.projectsLeftW() != base().projectsLeftW() ||
			viewOf(m) != viewTree || paneOf(m).killID != "" || m.flash != "" {
			t.Errorf("%q over the file acted: file open=%v focus=%v flash=%q", keyTexts(ks), m.hasOpenFile(), m.focused, m.flash)
		}
	}
}

func TestOpenFileSwallowsKeysForHiddenContent(t *testing.T) {
	m := openedFile(wideWorkspace())
	m = withFocus(m, mainPane)
	for _, keys := range [][]tea.KeyPressMsg{{keyMsg("enter")}, {keyMsg("d"), keyMsg("d")}, {keyMsg("l")}} {
		mm := pressKeys(m, keys...)
		if viewOf(mm) != viewTree || paneOf(mm).killID != "" || !mm.hasOpenFile() {
			t.Errorf("%q acted on the content behind the open file: view=%v kill=%q", keyTexts(keys), viewOf(mm), paneOf(mm).killID)
		}
	}
}

func TestViewerAndSessionFilesFooters(t *testing.T) {
	m := openedFile(wideWorkspace())
	m = withFocus(m, mainPane)
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "scroll") || !strings.Contains(f, "close") {
		t.Errorf("pane footer with a file open = %q", f)
	}

	s := wideWorkspace()
	s = withFocus(s, mainPane)
	ss, _ := s.enterSession("n1:s1")
	s, _ = ss.syncSidebar()
	s = withTreeLoaded(s, "n1:w1")
	s = pressKeys(s, cw('l')...)
	if f := ansi.Strip(s.currentFooter()); !strings.Contains(f, "h/l fold") {
		t.Errorf("session footer with the file tree focused = %q", f)
	}
	s, _ = upd(s, tea.KeyPressMsg{Code: tea.KeyEnter}) // focus moves to the file
	if f := ansi.Strip(s.currentFooter()); !strings.Contains(f, "close") {
		t.Errorf("session footer with a file open = %q", f)
	}
}

func TestSidebarsCannotSqueezePane(t *testing.T) {
	m := wideWorkspace()
	m.width = 130
	m = withFocus(m, rightSidebar)
	for i := 0; i < 30; i++ {
		m, _ = upd(m, tea.KeyPressMsg{Code: '>', Text: ">"})
	}
	m = withFocus(m, leftSidebar)
	for i := 0; i < 30; i++ {
		m, _ = upd(m, tea.KeyPressMsg{Code: '>', Text: ">"})
	}
	if w := m.bodyWidthFor(m.projectsLeftW()); w < 30 {
		t.Errorf("two sidebars squeezed the pane to %d columns", w)
	}
	assertFits(t, m.View().Content, m.width)
}

func TestTreeCursorStaysOnEntryWhenDirAboveLoads(t *testing.T) {
	ft := newFileTree("n1:w1")
	ft.setDir("", []api.DirEntry{{Name: "a", Path: "a", IsDir: true}, {Name: "b.go", Path: "b.go"}}, nil)
	press(&ft, "l") // unfold a: rows a, loading…, b.go
	press(&ft, "G") // b.go
	ft.setDir("a", []api.DirEntry{{Name: "x", Path: "a/x"}, {Name: "y", Path: "a/y"}}, nil)
	if r := ft.rows()[ft.cursor]; r.entry.Path != "b.go" {
		t.Errorf("cursor moved to %q when a/ loaded, want it to stay on b.go", r.entry.Path)
	}
}

func TestScrollStopsAtTheEnd(t *testing.T) {
	m := openedFile(wideWorkspace())
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "1\n2\n3"})
	m = withFocus(m, mainPane)
	for i := 0; i < 10; i++ {
		m, _ = upd(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if s := fileOf(m).scroll; s != 0 {
		t.Errorf("a file that fits should not scroll: %d", s)
	}

}

func TestFileViewHighlightsOnceOnLoad(t *testing.T) {
	m := openedFile(wideWorkspace())
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "module argus\n\ngo 1.24"})
	f := fileOf(m)
	if len(f.lines) != 3 {
		t.Fatalf("load should store the highlighted lines: %q", f.lines)
	}
	f.lines[0] = "CACHED"
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "CACHED") {
		t.Errorf("render should reuse the stored lines, not highlight again:\n%s", out)
	}
}

func TestEnteringSessionDropsFilesFocus(t *testing.T) {
	m := filesFocused()
	mm, _ := m.enterSession("n1:s1") // e.g. a resume result arriving while files had focus
	if mm.focused == rightSidebar {
		t.Error("a newly opened session should not start with the file tree focused")
	}
}

func TestLOpensAFile(t *testing.T) {
	ft := newFileTree("n1:w1")
	ft.setDir("", []api.DirEntry{{Name: "go.mod", Path: "go.mod"}}, nil)
	if req := press(&ft, "l"); req.openFile == nil || *req.openFile != "go.mod" {
		t.Errorf("l on a file should open it: %+v", req)
	}
}

func TestRefreshKeepsTreeCursor(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, rightSidebar)
	m.client = &recordingClient{}
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{{Name: "a", Path: "a", IsDir: true}, {Name: "z.go", Path: "z.go"}}})
	m, _ = upd(m, keyMsg("l"))
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "a", entries: []api.DirEntry{{Name: "x.go", Path: "a/x.go"}}})
	m, _ = upd(m, keyMsg("j")) // a/x.go
	m, cmd := typeKeysCmd(m, "gr")
	ft := m.right.fileTree
	if r := ft.rows()[ft.cursor]; cmd == nil || r.entry.Path != "a/x.go" {
		t.Errorf("gr should keep the tree and its cursor while it reloads: cursor on %q", r.entry.Path)
	}
}

func mainFileHeight(m model) int {
	_, h := m.mainSize()
	return fileViewHeight(h)
}

func TestFileViewGoesToEnds(t *testing.T) {
	m := openedFile(wideWorkspace())
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: b.String()})
	m = withFocus(m, mainPane)
	m, _ = upd(m, keyMsg("G"))
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "line 100") || strings.Contains(out, "line 1\n") {
		t.Errorf("G should show the last line:\n%s", out)
	}
	if want := 100 - (mainFileHeight(m) - 1); fileOf(m).scroll != want {
		t.Errorf("G should stop with the last line at the bottom: scroll=%d want %d", fileOf(m).scroll, want)
	}
	m, _ = upd(m, keyMsg("j"))
	if want := 100 - (mainFileHeight(m) - 1); fileOf(m).scroll != want {
		t.Errorf("j at the end should not scroll further: %d", fileOf(m).scroll)
	}
	m = typeKeys(m, "gg")
	if fileOf(m).scroll != 0 {
		t.Errorf("gg should go to the top: %d", fileOf(m).scroll)
	}
}

func TestFileViewPaneBindingsUseFileViewKeys(t *testing.T) {
	rc := &recordingClient{}
	m := openedFile(wideWorkspace())
	m.client = rc
	m = withFocus(m, mainPane)
	_, cmd := typeKeysCmd(m, "gr")
	if cmd == nil {
		t.Fatal("gr with pane focus and file open should produce a reload command")
	}
	execCmd(cmd)
	var calledRead bool
	for _, c := range rc.calls {
		if c == api.MethodWorkspaceReadFile {
			calledRead = true
		}
		if c == api.MethodProjectList {
			t.Error("gr with file view open must not reload the project list")
		}
	}
	if !calledRead {
		t.Errorf("gr with file view open should fetch the file; calls: %v", rc.calls)
	}

	m2 := openedFile(wideWorkspace())
	m2 = withFocus(m2, mainPane)
	m2, _ = upd(m2, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m2.hasOpenFile() {
		t.Error("esc with pane focus should close the file view")
	}
}

func TestTreeShowsSymlinkTargets(t *testing.T) {
	ft := newFileTree("n1:w1")
	ft.setDir("", []api.DirEntry{{Name: "docs", Path: "docs", IsDir: true, Symlink: true, Target: "site/docs"}, {Name: "cfg", Path: "cfg", Symlink: true, Target: "/etc/cfg"}}, nil)
	out := ansi.Strip(ft.view(60, 10, true))
	if !strings.Contains(out, "▸ docs → site/docs") || !strings.Contains(out, "cfg → /etc/cfg") {
		t.Errorf("symlink rows should show their targets:\n%s", out)
	}
}

func TestDetailFilesSidebarBindingsAreListed(t *testing.T) {
	m := filesFocused()
	m = withView(m, viewSession)
	m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
	// gr: the container's keys, then the Files tab's refresh.
	// t: the container's keys, then every key of the Files tab.
	typeKeys(m, "gr")
	typeKeys(m, "t")
	// TestMain's strict check reports any unlisted bindings and fails the run.
}
