package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// sidebarCtx is a ctx on the wide projects screen, for driving the right
// sidebar and its components alone.
func sidebarCtx() *ctx {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m.right.hidden = false
	m = selectRow(m, "n1:w1")
	m = withFocus(m, rightSidebar)
	return &ctx{m: &m}
}

func actionKinds(c *ctx) []actionKind {
	out := make([]actionKind, len(c.actions))
	for i, a := range c.actions {
		out[i] = a.kind
	}
	return out
}

// actionFile is the file an action opens, or a closed one.
func actionFile(a action) fileComp {
	f, _ := a.comp.(fileComp)
	return f
}

func TestFileTreeCompKeys(t *testing.T) {
	c := sidebarCtx()
	var comp component = fileTreeComp{loadedTree()}
	comp, cmd, ok := comp.handleKey(c, keyMsg("l"))
	ft := comp.(fileTreeComp)
	if !ok || cmd == nil || !ft.expanded["internal"] || len(c.actions) != 0 {
		t.Fatalf("l unfolds and loads a directory without actions: ok=%v cmd=%v expanded=%v actions=%v", ok, cmd != nil, ft.expanded, actionKinds(c))
	}
	comp, _, _ = comp.handleKey(c, keyMsg("G"))
	comp, cmd, _ = comp.handleKey(c, keyMsg("enter"))
	if len(c.actions) != 2 || c.actions[0].kind != actOpen || c.actions[1].kind != actFocus || cmd == nil {
		t.Fatalf("enter on a file asks to show it and to focus the main pane: %v", actionKinds(c))
	}
	if f := actionFile(c.actions[0]); f.ws != "n1:w1" || f.path != "go.mod" || !f.loading || f.diff {
		t.Errorf("shown file = %+v", f)
	}
	if c.actions[1].focus != mainPane {
		t.Errorf("focus = %v, want the main pane", c.actions[1].focus)
	}
	c.actions = nil
	if _, _, ok := comp.handleKey(c, keyMsg("esc")); !ok || len(c.actions) != 1 || c.actions[0].focus != mainPane {
		t.Errorf("esc asks to focus the main pane: %v", actionKinds(c))
	}
	if comp.section() != "file-tree" {
		t.Errorf("section = %q", comp.section())
	}
}

func TestChangesCompKeys(t *testing.T) {
	c := sidebarCtx()
	c.m.right.tab = sideChanges
	var comp component = changesComp{
		ws:      "n1:w1",
		files:   []api.ChangedFile{{Path: "a.go"}},
		commits: []api.Commit{{SHA: "abc1234", Short: "abc1234", Subject: "s"}},
	}
	comp, _, ok := comp.handleKey(c, keyMsg("j"))
	if ch := comp.(changesComp); !ok || ch.cursor != 1 {
		t.Fatalf("j moves onto the commit: ok=%v cursor=%d", ok, ch.cursor)
	}
	comp, cmd, _ := comp.handleKey(c, keyMsg("enter"))
	if ch := comp.(changesComp); ch.commit == nil || ch.commit.SHA != "abc1234" || cmd == nil || len(c.actions) != 0 {
		t.Fatalf("enter on a commit opens it and fetches its files: commit=%v actions=%v", ch.commit, actionKinds(c))
	}
	comp, _, _ = comp.handleKey(c, keyMsg("esc"))
	if ch := comp.(changesComp); ch.commit != nil || len(c.actions) != 0 {
		t.Fatalf("esc closes the commit and keeps focus: commit=%v actions=%v", ch.commit, actionKinds(c))
	}
	comp, _, _ = comp.handleKey(c, keyMsg("k"))
	comp, cmd, _ = comp.handleKey(c, keyMsg("enter"))
	if len(c.actions) != 2 || c.actions[0].kind != actOpen || !actionFile(c.actions[0]).diff || actionFile(c.actions[0]).path != "a.go" || cmd == nil {
		t.Fatalf("enter on a file asks to show its diff: %v", actionKinds(c))
	}
	c.actions = nil
	comp.handleKey(c, keyMsg("t"))
	if len(c.actions) != 1 || c.actions[0].kind != actFlash || !strings.Contains(c.actions[0].flash, "no target branch") {
		t.Errorf("t with no target asks for a flash: %v", actionKinds(c))
	}
	c.actions = nil
	comp.handleKey(c, keyMsg("esc"))
	if len(c.actions) != 1 || c.actions[0].focus != mainPane {
		t.Errorf("esc on the list asks to focus the main pane: %v", actionKinds(c))
	}
	if comp.section() != "changes" {
		t.Errorf("section = %q", comp.section())
	}
}

// sidebarKey gives msg to the right sidebar as the focus path does: its own
// keys, then the shown tab.
func sidebarKey(r rightSidebarState, c *ctx, msg tea.KeyPressMsg) (rightSidebarState, tea.Cmd, bool) {
	c.m.right = r
	if r, ok := r.handleKey(c, msg); ok {
		return r, nil, true
	}
	comp, cmd, ok := r.current().handleKey(c, msg)
	return r.store(comp), cmd, ok
}

func TestRightSidebarSwitchesTabs(t *testing.T) {
	c := sidebarCtx()
	r := rightSidebarState{ws: "n1:w1", fileTree: fileTreeComp{loadedTree()}, changes: changesComp{ws: "n1:w1", files: []api.ChangedFile{{Path: "a.go"}, {Path: "b.go"}}}}
	r, _, ok := sidebarKey(r, c, seqKey("gt"))
	if !ok || r.tab != sideChanges {
		t.Fatalf("next tab: tab=%v", r.tab)
	}
	r, _, _ = sidebarKey(r, c, keyMsg("j"))
	if r.changes.cursor != 1 || r.fileTree.cursor != 0 {
		t.Errorf("keys go to the shown tab: changes cursor=%d tree cursor=%d", r.changes.cursor, r.fileTree.cursor)
	}
	r, _, _ = sidebarKey(r, c, seqKey("gt"))
	if r.tab != sideFiles {
		t.Errorf("next tab wraps: tab=%v", r.tab)
	}
	r, _, _ = sidebarKey(r, c, seqKey("gT"))
	if r.tab != sideChanges {
		t.Errorf("prev tab wraps: tab=%v", r.tab)
	}
	before := c.m.projectsFilesW()
	r, _, _ = sidebarKey(r, c, seqKey("<C-w>>"))
	if r.width <= before {
		t.Errorf("widen: %d -> %d", before, r.width)
	}
}

func TestRightSidebarLeavesTheTogglesToTheModel(t *testing.T) {
	c := sidebarCtx()
	r := rightSidebarState{ws: "n1:w1", fileTree: fileTreeComp{loadedTree()}}
	for _, k := range []string{"<Space>e", "<Space>o"} {
		next, _, ok := sidebarKey(r, c, seqKey(k))
		if ok || next.hidden || len(c.actions) != 0 {
			t.Errorf("%s reached the right sidebar: ok=%v hidden=%v actions=%v", k, ok, next.hidden, actionKinds(c))
		}
	}
}

func TestRightSidebarRepliesNeedTheirWorkspaceAndGeneration(t *testing.T) {
	c := sidebarCtx()
	r := rightSidebarState{}
	r, cmd := r.show(c, "n1:w1")
	if r.ws != "n1:w1" || r.fileTree.ws != "n1:w1" || r.changes.ws != "n1:w1" || cmd == nil {
		t.Fatalf("show sets the workspace and loads the Files root: ws=%q tree=%q changes=%q", r.ws, r.fileTree.ws, r.changes.ws)
	}
	r, _ = r.update(c, listDirMsg{ws: "n1:w2", dir: "", entries: []api.DirEntry{{Name: "x", Path: "x"}}})
	if d := r.fileTree.dirs[""]; d == nil || !d.loading {
		t.Error("a listing for another workspace must be dropped")
	}
	r, _ = r.update(c, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{{Name: "x", Path: "x"}}})
	if d := r.fileTree.dirs[""]; d == nil || len(d.entries) != 1 {
		t.Error("the listing for the shown workspace reaches the file tree")
	}

	r.changes.gen = 2
	r, _ = r.update(c, changedFilesMsg{ws: "n1:w1", gen: 1, files: []api.ChangedFile{{Path: "old.go"}}})
	r, _ = r.update(c, commitsMsg{ws: "n1:w1", gen: 1, commits: []api.Commit{{SHA: "old"}}})
	if r.changes.files != nil || r.changes.commits != nil {
		t.Error("answers from an older generation must be dropped")
	}
	r, _ = r.update(c, changedFilesMsg{ws: "n1:w2", gen: 2, files: []api.ChangedFile{{Path: "w2.go"}}})
	if r.changes.files != nil {
		t.Error("changed files for another workspace must be dropped")
	}
	r, _ = r.update(c, changedFilesMsg{ws: "n1:w1", gen: 2, files: []api.ChangedFile{{Path: "a.go"}}})
	r, _ = r.update(c, commitsMsg{ws: "n1:w1", gen: 2, commits: []api.Commit{{SHA: "abc"}}})
	if len(r.changes.files) != 1 || len(r.changes.commits) != 1 {
		t.Errorf("matching answers reach the changes: files=%v commits=%v", r.changes.files, r.changes.commits)
	}
	r.changes.commit = &api.Commit{SHA: "abc"}
	r, _ = r.update(c, commitFilesMsg{ws: "n1:w1", sha: "other", files: []api.ChangedFile{{Path: "x"}}})
	if r.changes.commitFiles != nil {
		t.Error("files for another commit must be dropped")
	}

	r, _ = r.show(c, "n1:w2")
	if r.fileTree.ws != "n1:w2" || r.changes.ws != "n1:w2" || r.changes.files != nil || r.changes.gen != 2 {
		t.Errorf("a new workspace resets both tabs and keeps the generation: %+v", r.changes)
	}
}

func TestToggleRightSidebarIsTheSameFromEveryContainer(t *testing.T) {
	for _, from := range []container{mainPane, rightSidebar} {
		m := withFocus(filesFocused(), from)
		m = typeKeys(m, " e")
		if m.filesVisible() || m.focused == rightSidebar {
			t.Errorf("from %v: ␣e should hide the right sidebar: visible=%v focus=%v", from, m.filesVisible(), m.focused)
		}
		if m = typeKeys(m, " e"); !m.filesVisible() {
			t.Errorf("from %v: ␣e again should show the right sidebar", from)
		}
	}
	s := workspaceSession(nil)
	s = pressKeys(s, cw('l')...)
	if s = typeKeys(s, " e"); s.filesVisible() || s.focused == rightSidebar {
		t.Errorf("from the session's right sidebar: visible=%v focus=%v", s.filesVisible(), s.focused)
	}
}

func TestToggleLeftSidebarFromTheRightSidebarKeepsTheWidthRule(t *testing.T) {
	m := filesFocused()
	if m = typeKeys(m, " o"); m.sidebarVisible() || m.focused != rightSidebar {
		t.Fatalf("␣o from the right sidebar hides the tree: visible=%v focus=%v", m.sidebarVisible(), m.focused)
	}
	if m = typeKeys(m, " o"); !m.sidebarVisible() {
		t.Fatal("␣o again shows the tree")
	}
	for _, view := range []shownView{viewTree, viewSession} {
		n := withFocus(withView(filesFocused(), view), rightSidebar)
		n.width = 70
		mm, _ := n.runKey(seqKey("<Space>o"))
		got := mm.(model)
		if got.left.hidden || !strings.Contains(got.flash, "needs 80 columns") {
			t.Errorf("view %v: ␣o from the right sidebar at 70 columns: hidden=%v flash=%q", view, got.left.hidden, got.flash)
		}
	}
}
