package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestProjectTreeIsTheLeftSidebarComponent(t *testing.T) {
	m := projectsTestModel()
	m = withPane(m, workspaceComp{ws: "n1:w1", cursor: 1})
	var comp component = m.left.tree
	if comp.section() != "project-tree" || comp.raw(&ctx{m: &m}) {
		t.Fatalf("section = %q raw = %v, want project-tree and not raw", comp.section(), comp.raw(&ctx{m: &m}))
	}
	c := &ctx{m: &m}
	next, _, used := comp.handleKey(c, keyMsg("j"))
	if !used || next.(projectTreeComp).cursorRowID() != "n1:p1" {
		t.Fatalf("j through handleKey: used=%v row=%q, want the project row", used, next.(projectTreeComp).cursorRowID())
	}
	if m.left.tree.cursor != 0 || paneOf(m).cursor != 1 {
		t.Error("handleKey must only ask for changes, not write the model")
	}
}

func TestTreeKeysReachOnlyTheFocusedTree(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:w1")

	tree := pressKeys(m, keyMsg("j"))
	if tree.left.tree.cursorRowID() != "n1:w2" || paneOf(tree).cursor != 0 {
		t.Errorf("j on the tree: row=%q wsCursor=%d, want the tree to move and the pane to follow", tree.left.tree.cursorRowID(), paneOf(tree).cursor)
	}

	pane := pressKeys(withFocus(m, mainPane), keyMsg("j"))
	if pane.left.tree.cursorRowID() != "n1:w1" || paneOf(pane).cursor != 1 {
		t.Errorf("j on the pane: row=%q wsCursor=%d, want only the pane to move", pane.left.tree.cursorRowID(), paneOf(pane).cursor)
	}
}

func TestPickersOpenAsPopupsAndKeepTheTreeFocused(t *testing.T) {
	branches := branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main"}, {Name: "dev"}}}
	cases := []struct {
		name  string
		row   string
		open  tea.KeyPressMsg
		close []tea.Msg
	}{
		{"create, cancel", "n1:p1", keyMsg("a"), []tea.Msg{keyMsg("esc")}},
		{"retarget, cancel", "n1:w2", keyMsg("T"), []tea.Msg{keyMsg("esc")}},
		{"retarget, submit", "n1:w2", keyMsg("T"), []tea.Msg{branches, keyMsg("enter")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := projectsTestModel()
			m.width, m.height = 120, 30
			m.client = &recordingClient{}
			m = selectRow(m, c.row)
			m = withFile(m, fileComp{ws: "n1:w2", path: "a.go"})
			under := len(m.main)

			m, _ = upd(m, c.open)
			if !m.pickerOpen() || len(m.main) != under || m.focused != leftSidebar {
				t.Fatalf("the picker should open as a popup with the tree focused: stack=%d focus=%v", len(m.main), m.focused)
			}
			if f, ok := m.openFile(); !ok || f.path != "a.go" || viewOf(m) != viewTree {
				t.Fatalf("the file and the view stay under the picker: file=%v view=%v", ok, viewOf(m))
			}
			for _, msg := range c.close {
				m, _ = upd(m, msg)
			}
			if m.pickerOpen() || len(m.main) != under || m.focused != leftSidebar {
				t.Errorf("closing the picker should leave focus on the tree: open=%v stack=%d focus=%v", m.pickerOpen(), len(m.main), m.focused)
			}
			if f, ok := m.openFile(); !ok || f.path != "a.go" {
				t.Error("the file should show again once the picker closes")
			}
		})
	}
}

func TestCreatePickerClosesOnItsCreateReply(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	m, _ = upd(m, createDoneMsg{seq: createOf(m).seq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9"}, source: api.SourceNew})
	if m.pickerOpen() || m.focused != leftSidebar || m.left.tree.want != "n1:w9" {
		t.Errorf("a create reply should close the picker onto the tree: open=%v focus=%v want=%q", m.pickerOpen(), m.focused, m.left.tree.want)
	}
}

func TestPickerRepliesWithNoPickerOpenAreDropped(t *testing.T) {
	m := projectsTestModel()
	before := len(m.main)
	for _, msg := range []tea.Msg{
		branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main"}}},
		prsMsg{projectID: "n1:p1"},
		issuesMsg{projectID: "n1:p1"},
	} {
		if m, _ = upd(m, msg); len(m.main) != before || m.pickerOpen() {
			t.Errorf("%T with no picker open should change nothing: stack=%d", msg, len(m.main))
		}
	}
}

func TestTreeReplyWithAnOldFetchSeqDoesNotAnswerTheNewest(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m.left.tree.loading, m.left.tree.fetchSeq = true, 2
	m.left.tree.want = "n1:w3"
	m, cmd := upd(m, projectsTreeMsg{seq: 1, tree: m.left.tree.data})
	if !m.left.tree.loading || m.left.tree.want != "n1:w3" || cmd != nil {
		t.Errorf("an old reply must not end the newest fetch: loading=%v want=%q cmd=%v", m.left.tree.loading, m.left.tree.want, cmd != nil)
	}
	m, _ = upd(m, projectsTreeMsg{seq: 2, tree: withNewWorkspace(m.left.tree.data)})
	if m.left.tree.loading || m.left.tree.want != "" || m.left.tree.cursorRowID() != "n1:w3" {
		t.Errorf("the newest reply ends the fetch and selects the wanted row: loading=%v want=%q row=%q", m.left.tree.loading, m.left.tree.want, m.left.tree.cursorRowID())
	}
}

// A key pressed while the tree has focus but no longer shows goes to the main
// pane alone; focus repair then moves focus off the tree.
func TestHiddenTreeKeyReachesOneHandler(t *testing.T) {
	t.Run("on the Home row", func(t *testing.T) {
		m := withFocus(withView(homeTestModel(), viewTree), leftSidebar)
		m.left.tree.cursor = 0
		m = typeKeys(m, " o")
		if viewOf(m) != viewHome || m.focused != mainPane {
			t.Fatalf("␣o on the Home row: view=%v focus=%v, want focus repaired onto the Home pane", viewOf(m), m.focused)
		}
		m, _ = upd(m, keyMsg("j"))
		if homeOf(m).cursor != 1 || m.left.tree.cursor != 0 {
			t.Errorf("j should move the Home list once: cursor=%d tree cursor=%d", homeOf(m).cursor, m.left.tree.cursor)
		}
	})
	t.Run("on a workspace", func(t *testing.T) {
		m := withFocus(withView(homeTestModel(), viewTree), leftSidebar)
		m = selectRow(m, "n1:w1")
		m = typeKeys(m, " o")
		if m.sidebarVisible() || !isWorkspace(m.main.top()) || m.focused != mainPane {
			t.Fatalf("␣o on a workspace: tree shown=%v top=%T focus=%v, want focus repaired onto the pane", m.sidebarVisible(), m.main.top(), m.focused)
		}
		m, _ = upd(m, keyMsg("j"))
		if paneOf(m).cursor != 1 || m.left.tree.cursorRowID() != "n1:w1" || m.focused != mainPane {
			t.Errorf("j should move the pane once: wsCursor=%d row=%q focus=%v", paneOf(m).cursor, m.left.tree.cursorRowID(), m.focused)
		}
	})
}

func TestKeyAfterANarrowingResizeReachesOneHandler(t *testing.T) {
	narrow := tea.WindowSizeMsg{Width: 70, Height: 30}
	t.Run("on the Home row", func(t *testing.T) {
		m := withFocus(withView(homeTestModel(), viewTree), leftSidebar)
		m.left.tree.cursor = 0
		m, _ = upd(m, narrow)
		m, _ = upd(m, keyMsg("j"))
		if homeOf(m).cursor != 1 || viewOf(m) != viewHome || m.focused != mainPane {
			t.Errorf("j should move the Home list once: cursor=%d view=%v focus=%v", homeOf(m).cursor, viewOf(m), m.focused)
		}
	})
	t.Run("on a workspace", func(t *testing.T) {
		m := withFocus(withView(homeTestModel(), viewTree), leftSidebar)
		m = selectRow(m, "n1:w1")
		m, _ = upd(m, narrow)
		m, _ = upd(m, keyMsg("j"))
		if paneOf(m).cursor != 1 || m.left.tree.cursorRowID() != "n1:w1" || m.focused != mainPane {
			t.Errorf("j should move the pane once: wsCursor=%d row=%q focus=%v", paneOf(m).cursor, m.left.tree.cursorRowID(), m.focused)
		}
	})
}

// A mapping that extends the toggle makes the toggle and the next key run in
// one Update; the next key must see focus repaired.
func TestKeyReplayedAfterAToggleSeesRepairedFocus(t *testing.T) {
	m := withKeymap(homeTestModel(), map[string]map[string]string{"project-tree": {"<Leader>ox": "toggle show-gone"}})
	m = withFocus(withView(m, viewTree), leftSidebar)
	m.left.tree.cursor = 0
	m = typeKeys(m, " oj")
	if m.sidebarVisible() || viewOf(m) != viewHome || homeOf(m).cursor != 1 {
		t.Errorf("␣o then j: tree shown=%v view=%v cursor=%d, want j to move the Home list", m.sidebarVisible(), viewOf(m), homeOf(m).cursor)
	}
}

func TestFocusRepairTakesFocusOffAHiddenTree(t *testing.T) {
	m := withFocus(withView(homeTestModel(), viewTree), leftSidebar)
	m = selectRow(m, "n1:w1")
	m.left.hidden = true
	if m, _ = upd(m, spinTickMsg{}); m.focused != mainPane || viewOf(m) != viewTree {
		t.Errorf("any update should move focus to the pane: focus=%v view=%v", m.focused, viewOf(m))
	}
}
