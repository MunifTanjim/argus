package tui

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestTreeClickSelectsThenOpens(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withFocus(m, mainPane)
	y, x := findBlock(t, m, "feature")
	m, _ = click(m, x, y)
	if m.focused != leftSidebar || m.left.tree.cursorRowID() != "n1:w2" {
		t.Fatalf("focused=%v row=%q, want the tree on n1:w2", m.focused, m.left.tree.cursorRowID())
	}
	m, _ = click(m, x, y)
	if p, ok := m.baseComp().(workspaceComp); !ok || p.ws != "n1:w2" || m.focused != mainPane {
		t.Errorf("base=%T focused=%v; a second click must open the workspace", m.baseComp(), m.focused)
	}
}

func TestTreeFoldMarkerFolds(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withFocus(m, mainPane)
	y, x := findBlock(t, m, "▾ argus")
	m, _ = click(m, x, y)
	if !m.left.tree.isFolded("n1:p1") || m.focused != leftSidebar {
		t.Fatalf("folded=%v focused=%v, want the project folded and the tree focused", m.left.tree.isFolded("n1:p1"), m.focused)
	}
	if _, ok := m.baseComp().(homeComp); !ok {
		t.Error("a fold click must not open the row")
	}
	y, x = findBlock(t, m, "▸ argus")
	m, _ = click(m, x, y)
	if m.left.tree.isFolded("n1:p1") {
		t.Error("a second fold click must unfold")
	}
}

func TestTreeWheelMovesCursor(t *testing.T) {
	m := withMouse(homeTestModel())
	m, _ = wheelAt(m, 1, 5, 2)
	if m.left.tree.cursor != 2 || m.focused != mainPane {
		t.Errorf("cursor=%d focused=%v, want 2 and focus unchanged", m.left.tree.cursor, m.focused)
	}
}

func withTreeEntries(m model, ws string, names ...string) model {
	entries := make([]api.DirEntry, len(names))
	for i, n := range names {
		entries[i] = api.DirEntry{Name: n, Path: n}
	}
	m, _ = upd(m, listDirMsg{ws: ws, dir: "", entries: entries})
	return m
}

func TestRightTabClickSwitches(t *testing.T) {
	m := withMouse(wideWorkspace())
	y, x := findBlock(t, m, "Changes")
	m, _ = click(m, x, y)
	if m.right.tab != sideChanges || m.focused != rightSidebar {
		t.Errorf("tab=%v focused=%v, want Changes and the sidebar focused", m.right.tab, m.focused)
	}
}

func TestFilesClickOpensFile(t *testing.T) {
	m := withMouse(withTreeEntries(wideWorkspace(), "n1:w1", "a.go", "b.go"))
	x, y := itemCell(t, m, regRight, 1)
	m, _ = click(m, x, y)
	if m.right.fileTree.cursor != 1 || m.focused != rightSidebar {
		t.Fatalf("cursor=%d focused=%v, want 1 and the sidebar", m.right.fileTree.cursor, m.focused)
	}
	m, _ = click(m, x, y)
	if f, ok := m.openFile(); !ok || f.path != "b.go" {
		t.Errorf("open file = %+v, want b.go after a second click", f)
	}
}

func TestChangesClickOpensDiff(t *testing.T) {
	m := withMouse(wideWorkspace())
	m.right.tab = sideChanges
	m = withFocus(m, rightSidebar)
	m.right.changes.files = []api.ChangedFile{{Path: "a.go", Change: "modified"}, {Path: "b.go", Change: "modified"}}
	m.right.changes.commits = []api.Commit{}
	x, y := itemCell(t, m, regRight, 1)
	m, _ = click(m, x, y)
	if m.right.changes.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.right.changes.cursor)
	}
	m, _ = click(m, x, y)
	if f, ok := m.openFile(); !ok || f.path != "b.go" || !f.diff {
		t.Errorf("open file = %+v, want the diff of b.go", f)
	}
}

func TestFilesWheelMovesCursor(t *testing.T) {
	m := withMouse(withTreeEntries(wideWorkspace(), "n1:w1", "a.go", "b.go"))
	x, y := itemCell(t, m, regRight, 0)
	m, _ = wheelAt(m, x, y, 1)
	if m.right.fileTree.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.right.fileTree.cursor)
	}
}

func withDirTree(m model) model {
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "", entries: []api.DirEntry{
		{Name: "a.go", Path: "a.go"},
		{Name: "pkg", Path: "pkg", IsDir: true},
	}})
	return m
}

func TestFilesFoldMarkerToggles(t *testing.T) {
	m := withMouse(withDirTree(wideWorkspace()))
	y, x := findBlock(t, m, "▸ pkg")
	m, cmd := click(m, x, y)
	ft := m.right.fileTree
	if !ft.expanded["pkg"] || cmd == nil || m.focused != rightSidebar {
		t.Fatalf("expanded=%v cmd=%v focused=%v, want pkg opened, loaded, and the sidebar focused", ft.expanded["pkg"], cmd != nil, m.focused)
	}
	if ft.cursor != 1 {
		t.Errorf("cursor = %d, want the pkg row", ft.cursor)
	}
	if _, ok := m.openFile(); ok {
		t.Error("a fold click must not open a file")
	}
	m, _ = upd(m, listDirMsg{ws: "n1:w1", dir: "pkg", entries: []api.DirEntry{{Name: "b.go", Path: "pkg/b.go"}}})
	y, x = findBlock(t, m, "▾ pkg")
	m, _ = click(m, x, y)
	if m.right.fileTree.expanded["pkg"] {
		t.Error("a second fold click must close pkg")
	}
}

func TestFilesFileRowHasNoFoldZone(t *testing.T) {
	m := withMouse(withDirTree(wideWorkspace()))
	m = withFocus(m, rightSidebar)
	y, x := findBlock(t, m, "a.go")
	m, _ = click(m, x-2, y)
	if f, ok := m.openFile(); !ok || f.path != "a.go" {
		t.Errorf("open file = %+v; the marker cells of a file row act as the row", f)
	}
}
