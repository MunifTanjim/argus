package tui

import (
	"encoding/json"
	"fmt"
	"github.com/MunifTanjim/argus/internal/registry"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func projectsTestModel() model {
	m := testModel()
	m = withView(m, viewTree)
	m.left.hidden = false
	m.focused = leftSidebar
	m.left.tree.collapsed = map[string]bool{}
	m.left.tree.data = []api.ProjectNode{{
		ID: "n1:p1", Name: "argus", Kind: "git", Dir: "/repo/.git", NodeID: "n1", NodeLabel: "home",
		Workspaces: []api.WorkspaceNode{
			{ID: "n1:w1", Dir: "/repo", IsMain: true, Branch: "main"},
			{ID: "n1:w2", Dir: "/repo-feat", Branch: "feature"},
		},
	}}
	m.left.tree.rebuild()
	m.sessions = map[string]session.Session{
		"n1:s1": {ID: "n1:s1", WorkspaceID: "n1:w1", Repo: "repo"},
		"n1:s2": {ID: "n1:s2", WorkspaceID: "n1:w2", Repo: "repo"},
		"n1:s3": {ID: "n1:s3", WorkspaceID: "n1:w1", Repo: "repo"},
	}
	m = withView(m, viewTree)
	return m
}

func TestBuildProjectRows(t *testing.T) {
	m := projectsTestModel()
	// A single node still heads the tree.
	rows := m.left.tree.rows
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 (Home first): %+v", len(rows), rows)
	}
	if rows[1].kind != rowNode || rows[1].id != "n1" {
		t.Fatalf("node row missing: %+v", rows[1])
	}
	if rows[2].kind != rowProject || rows[2].depth != 1 {
		t.Fatalf("unexpected project row: %+v", rows[2])
	}
	if rows[3].kind != rowWorkspace || rows[4].kind != rowWorkspace || rows[3].depth != 2 {
		t.Fatalf("workspaces missing: %+v", rows)
	}
}

func TestBuildProjectRowsShowsNodesWhenMany(t *testing.T) {
	tree := append(projectsTestModel().left.tree.data, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2"})
	rows := buildProjectRows(projectTreeComp{data: tree})
	if rows[0].kind != rowNode || rows[1].kind != rowProject || rows[1].depth != 1 {
		t.Fatalf("two nodes should show node rows: %+v", rows)
	}
}

func TestBuildProjectRowsSortsNodesByLabel(t *testing.T) {
	tree := []api.ProjectNode{
		{ID: "n3:p", Name: "c", NodeID: "n3", NodeLabel: "work"},
		{ID: "n2:p", Name: "b", NodeID: "n2", NodeLabel: "home"},
		{ID: "n1:p", Name: "a", NodeID: "n1", NodeLabel: "work"},
	}
	var got []string
	for _, r := range buildProjectRows(projectTreeComp{data: tree}) {
		if r.kind == rowNode {
			got = append(got, r.id)
		}
	}
	if got, want := strings.Join(got, ","), "n2,n1,n3"; got != want {
		t.Fatalf("node order = %s, want %s", got, want)
	}
}

func TestFilterRows(t *testing.T) {
	m := projectsTestModel()
	rows := buildProjectRows(projectTreeComp{data: m.left.tree.data, collapsed: map[string]bool{"n1:p1": true}, filter: "FEAT"})
	// The branch match shows its project and only that workspace, even when folded.
	if len(rows) != 3 || rows[2].id != "n1:w2" {
		t.Fatalf("filter rows = %+v, want node + project + n1:w2", rows)
	}
	if rows := buildProjectRows(projectTreeComp{data: m.left.tree.data, filter: "argus"}); len(rows) != 4 {
		t.Errorf("a project-name match should keep all workspaces, got %d rows", len(rows))
	}
	if rows := buildProjectRows(projectTreeComp{data: m.left.tree.data, filter: "zzz"}); len(rows) != 0 {
		t.Errorf("no match should give no rows, got %+v", rows)
	}
}

func TestPaneSessionsFilteredByWorkspace(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:w1")
	if got := m.selectedWorkspaceID(); got != "n1:w1" {
		t.Fatalf("selectedWorkspaceID = %q, want n1:w1", got)
	}
	ss := m.wsSessions(m.selectedWorkspaceID())
	if len(ss) != 2 { // s1 and s3 are in w1
		t.Fatalf("got %d sessions for w1, want 2", len(ss))
	}
	for _, s := range ss {
		if s.WorkspaceID != "n1:w1" {
			t.Errorf("session %s has workspace %q, want n1:w1", s.ID, s.WorkspaceID)
		}
	}

	m = selectRow(m, "n1:w2")
	if got := len(m.wsSessions(m.selectedWorkspaceID())); got != 1 {
		t.Errorf("got %d sessions for w2, want 1", got)
	}
}

func TestProjectsCollapseTogglesRows(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	mm, _ := treeKey(m, keyMsg("h"))
	// Collapsing the project hides its two workspaces.
	if len(mm.left.tree.rows) != 3 {
		t.Fatalf("after collapse got %d rows, want 3 (Home, node, project)", len(mm.left.tree.rows))
	}
}

func TestProjectsEnterOnWorkspaceFocusesPane(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:w1")
	mm, _ := treeKey(m, keyMsg("enter"))
	if mm.focused != mainPane {
		t.Errorf("focus = %v, want mainPane", mm.focused)
	}
}

func TestProjectsViewRenders(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:w1")
	out := m.View().Content
	if out == "" {
		t.Fatal("projectsView rendered empty")
	}
	for _, want := range []string{"argus", "main", "feature"} {
		if !strings.Contains(out, want) {
			t.Errorf("projectsView missing %q", want)
		}
	}
}

func TestResizeLeftSidebar(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	before := m.projectsLeftW()
	res, _ := m.runKey(seqKey("<C-w>>"))
	mm := res.(model)
	if mm.projectsLeftW() <= before {
		t.Errorf("widen: leftW %d did not grow past %d", mm.projectsLeftW(), before)
	}
	res, _ = mm.runKey(seqKey("<C-w><lt>"))
	mm2 := res.(model)
	if mm2.projectsLeftW() != before {
		t.Errorf("narrow: leftW = %d, want back to %d", mm2.projectsLeftW(), before)
	}
}

func TestToggleSidebar(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	if !m.sidebarVisible() {
		t.Fatal("sidebar should start visible on a wide terminal")
	}
	mm := typeKeys(m, " o")
	if mm.sidebarVisible() {
		t.Error("␣o should hide the sidebar")
	}
	if mm.focused != mainPane {
		t.Error("hiding the sidebar should move focus to the pane")
	}
	mm = typeKeys(mm, " o")
	if !mm.sidebarVisible() {
		t.Error("␣o again should show the sidebar")
	}
	if mm.focused != leftSidebar {
		t.Errorf("showing the sidebar should focus it: focus=%v", mm.focused)
	}
}

func TestToggleFilesFocusesTheOpenedSidebar(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	m = typeKeys(m, " e")
	if m.filesVisible() || m.focused != mainPane {
		t.Fatalf("␣e should hide the right sidebar: visible=%v focus=%v", m.filesVisible(), m.focused)
	}
	m = typeKeys(m, " e")
	if !m.filesVisible() || m.focused != rightSidebar {
		t.Errorf("␣e again should show and focus the right sidebar: visible=%v focus=%v", m.filesVisible(), m.focused)
	}
}

func TestNarrowingMovesFocusOffTheTree(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1")
	m, _ = upd(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	if m.focused != mainPane {
		t.Errorf("the pane should take focus when the tree collapses: focus=%v", m.focused)
	}

	m = projectsTestModel()
	m.width, m.height = 120, 30
	m.left.tree.cursor = 0
	m, _ = upd(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	if viewOf(m) != viewHome || m.focused != mainPane {
		t.Errorf("on the Home row the Home pane should take focus: view=%v focus=%v", viewOf(m), m.focused)
	}
}

func TestHelpFitsCommonTerminal(t *testing.T) {
	for _, size := range []struct{ w, h int }{{120, 40}, {250, 20}, {120, 30}, {90, 40}, {80, 24}} {
		m := projectsTestModel()
		m.width, m.height = size.w, size.h
		m = typeKeys(m, "g?")
		fits := m.helpMaxScroll() == 0
		seen := ansi.Strip(m.View().Content)
		for !fits && m.helpScroll < m.helpMaxScroll() {
			m = typeKeys(m, "j")
			seen += ansi.Strip(m.View().Content)
		}
		if !m.showHelp {
			t.Fatalf("%dx%d: j should scroll the help, not close it", size.w, size.h)
		}
		for _, g := range m.helpGroups() {
			for _, r := range append([]helpRow{{key: g.title, desc: g.title}}, g.rows...) {
				if r.key != "" && !strings.Contains(seen, r.label()) {
					t.Errorf("%dx%d (fits=%v): help never shows %q:\n%s", size.w, size.h, fits, r.label(), seen)
				}
			}
		}
		assertFits(t, m.View().Content, size.w)
	}
}

func TestTogglesBelowBreakpointOnlyHint(t *testing.T) {
	for _, view := range []shownView{viewTree, viewHome} {
		m := projectsTestModel()
		m = withView(m, view)
		m = withFocus(m, mainPane)
		m.width, m.height = 70, 30
		mm := typeKeys(m, " o")
		if mm.left.hidden || !strings.Contains(mm.flash, "needs 80 columns") {
			t.Errorf("view %v: ␣o at 70 cols: hidden=%v flash=%q", view, mm.left.hidden, mm.flash)
		}
		m.width = 100
		m.right.hidden = false
		mm = typeKeys(m, " e")
		if mm.right.hidden || !strings.Contains(mm.flash, "needs 120 columns") {
			t.Errorf("view %v: ␣e at 100 cols: hidden=%v flash=%q", view, mm.right.hidden, mm.flash)
		}
	}
}

func TestSidebarAutoCollapsesWhenNarrow(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 60, 30 // below sidebarMinWidth
	if m.sidebarVisible() {
		t.Error("sidebar should auto-collapse on a narrow terminal")
	}
	// Renders center-only without panic.
	if out := m.View().Content; !strings.Contains(out, "Argus") {
		t.Error("collapsed projectsView did not render")
	}
}

func TestEnterSessionRecordsOrigin(t *testing.T) {
	// From the projects sidebar, exit should return here, not the flat list.
	m := selectRow(projectsTestModel(), "n1:w1")
	mm, _ := m.enterSession("n1:s1")
	if viewOf(mm) != viewSession {
		t.Fatalf("view = %v, want viewSession", viewOf(mm))
	}
	if returnView(mm) != viewTree {
		t.Errorf("sessionReturn = %v, want viewTree", returnView(mm))
	}

	// From the flat list, exit should return to the list.
	l := testModel()
	l.dock.drafts = map[string]string{}
	l = withView(l, viewHome)
	l.sessions = map[string]session.Session{"s1": {ID: "s1"}}
	ll, _ := l.enterSession("s1")
	if returnView(ll) != viewHome {
		t.Errorf("sessionReturn = %v, want viewHome", returnView(ll))
	}
}

func TestSpinnerArmsOnProjectsScreen(t *testing.T) {
	m := projectsTestModel()
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Status: session.StatusWorking}
	m = withView(m, viewTree)
	m.spinning = false
	if cmd := m.maybeSpin(); cmd == nil {
		t.Fatal("spinner tick not armed on the projects screen")
	}
	if !m.spinning {
		t.Error("spinning flag not set")
	}
}

func TestNewWorkspaceFlow(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	mm, _ := treeKey(m, keyMsg("a"))
	if !createOpen(mm) || createOf(mm).projectID != "n1:p1" {
		t.Fatalf("workspace new picker not open: %+v", createOf(mm))
	}
}

func TestRemoveWorkspaceConfirm(t *testing.T) {
	m := projectsTestModel()
	delete(m.sessions, "n1:s2") // a live session would refuse the remove
	m = selectRow(m, "n1:w2")
	mm, _ := treeKey(m, seqKey("dd"))
	if mm.left.tree.pendingRemove != "n1:w2" {
		t.Fatalf("pendingRemove = %q, want n1:w2", mm.left.tree.pendingRemove)
	}
	res2, cmd := treeKey(mm, tea.KeyPressMsg{Code: 'y'})
	if res2.left.tree.pendingRemove != "" {
		t.Error("pendingRemove not cleared after confirm")
	}
	if cmd == nil {
		t.Error("expected a remove command on y")
	}
}

func TestRemoveMainWorktreeRefused(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:w1")
	res, _ := treeKey(m, seqKey("dd"))
	if res.left.tree.pendingRemove != "" {
		t.Error("removing the main worktree should not arm a confirmation")
	}
}

func TestToggleHiddenEmitsCommand(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	if _, cmd := treeKey(m, keyMsg("H")); cmd == nil {
		t.Error("expected a set-hidden command")
	}
}

func TestShowHiddenFiltersRows(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.data[0].Hidden = true
	m.left.tree.rows = buildProjectRows(m.left.tree)
	if len(m.left.tree.rows) != 0 {
		t.Fatalf("hidden project should be filtered, got %d rows", len(m.left.tree.rows))
	}
	res, _ := m.runKey(seqKey("z."))
	if len(res.(model).left.tree.rows) == 0 {
		t.Error("z. should reveal hidden projects")
	}
}

func TestTreeLeftRight(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	press := func(m model, code rune) model {
		res, _ := m.runKey(tea.KeyPressMsg{Code: code})
		return res.(model)
	}

	m = selectRow(m, "n1:w2")
	m = press(m, 'h') // workspace: step to the parent project
	if got := m.left.tree.cursorRowID(); got != "n1:p1" {
		t.Fatalf("h on a workspace: cursor on %q, want n1:p1", got)
	}
	m = press(m, 'h') // unfolded project: fold
	if !m.left.tree.collapsed["n1:p1"] || len(m.left.tree.rows) != 3 {
		t.Fatalf("h on an unfolded project should fold it: %+v", m.left.tree.rows)
	}
	m = press(m, 'l') // folded project: unfold
	if m.left.tree.collapsed["n1:p1"] || len(m.left.tree.rows) != 5 {
		t.Fatalf("l on a folded project should unfold it: %+v", m.left.tree.rows)
	}
	m = press(m, 'l') // unfolded project: step into the first workspace
	if got := m.left.tree.cursorRowID(); got != "n1:w1" {
		t.Fatalf("l on an unfolded project: cursor on %q, want n1:w1", got)
	}
	m = press(m, 'l') // workspace: focus the pane
	if m.focused != mainPane {
		t.Errorf("l on a workspace should focus the pane")
	}
}

func TestPaneRightFromAProjectRowFocusesTheSummary(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = withFocus(selectRow(m, "n1:p1"), leftSidebar)
	mm := pressKeys(m, cw('l')...)
	if s, ok := mm.baseComp().(summaryComp); !ok || s.id != "n1:p1" || mm.focused != mainPane {
		t.Errorf("<C-w>l on a project row: base=%#v focus=%v, want the summary of n1:p1 with focus", mm.baseComp(), mm.focused)
	}
}

func TestTreeReloadKeepsSelectionByID(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:w2")
	tree := []api.ProjectNode{m.left.tree.data[0]}
	tree[0].Workspaces = append([]api.WorkspaceNode{{ID: "n1:w0", Dir: "/repo-a", Branch: "a"}}, tree[0].Workspaces...)
	res, _ := m.Update(projectsTreeMsg{tree: tree})
	if got := res.(model).left.tree.cursorRowID(); got != "n1:w2" {
		t.Errorf("after reload cursor on %q, want n1:w2", got)
	}
}

func TestWantedRowIsSelectedAfterReload(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	m = wantAfterFetch(m, "n1:w3")
	tree := []api.ProjectNode{m.left.tree.data[0]}
	tree[0].Workspaces = append(append([]api.WorkspaceNode{}, tree[0].Workspaces...), api.WorkspaceNode{ID: "n1:w3", Dir: "/repo-new", Branch: "new"})
	m.left.tree.collapsed["n1:p1"] = true
	mm, _ := upd(m, projectsTreeMsg{tree: tree})
	if got := mm.left.tree.cursorRowID(); got != "n1:w3" {
		t.Errorf("after reload cursor on %q, want n1:w3", got)
	}
	if mm.left.tree.want != "" {
		t.Errorf("want not cleared: %q", mm.left.tree.want)
	}
}

func wantAfterFetch(m model, id string) model {
	m.left.tree.want = id
	m.loadProjects()
	return m
}

func withNewWorkspace(tree []api.ProjectNode) []api.ProjectNode {
	out := []api.ProjectNode{tree[0]}
	out[0].Workspaces = append(append([]api.WorkspaceNode{}, tree[0].Workspaces...), api.WorkspaceNode{ID: "n1:w3", Dir: "/repo-new", Branch: "new"})
	return out
}

func TestStaleTreeReplyKeepsTheRowWanted(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	old := m.left.tree.data
	m = wantAfterFetch(m, "n1:w3")
	m, _ = upd(m, projectsTreeMsg{seq: m.left.tree.fetchSeq - 1, tree: old})
	m, _ = upd(m, projectsTreeMsg{seq: m.left.tree.fetchSeq, tree: withNewWorkspace(old)})
	if got := m.left.tree.cursorRowID(); got != "n1:w3" {
		t.Errorf("a stale reply must not drop the jump: cursor on %q", got)
	}
}

func TestReplyBeforeARefetchKeepsTheRowWanted(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m = selectRow(m, "n1:p1")
	old := m.left.tree.data
	m.left.tree.loading = true
	m = wantAfterFetch(m, "n1:w3")
	m, _ = upd(m, projectsTreeMsg{seq: m.left.tree.fetchSeq, tree: old})
	m, _ = upd(m, projectsTreeMsg{seq: m.left.tree.fetchSeq, tree: withNewWorkspace(old)})
	if got := m.left.tree.cursorRowID(); got != "n1:w3" {
		t.Errorf("the fetch in flight when the row was wanted predates it: cursor on %q", got)
	}
}

func TestRefreshKeepsTree(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	res, cmd := m.runKey(seqKey("gr"))
	mm := res.(model)
	if mm.left.tree.data == nil || len(mm.left.tree.rows) == 0 || cmd == nil {
		t.Fatalf("refresh should keep the tree and fetch: tree=%v rows=%d", mm.left.tree.data, len(mm.left.tree.rows))
	}
	if !strings.Contains(mm.View().Content, "refreshing") {
		t.Error("refresh should show a refreshing hint")
	}
}

func TestUnfocusedTreeKeepsCursorBar(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w2")
	m = withFocus(m, mainPane)
	var sel string
	for _, ln := range strings.Split(ansi.Strip(treePane(m, 40, 20)), "\n") {
		if strings.Contains(ln, "repo-feat") {
			sel = ln
		}
	}
	if !strings.HasPrefix(sel, "▌") {
		t.Errorf("selected row lost its bar when unfocused: %q", sel)
	}
}

func TestWorkspaceRowBadge(t *testing.T) {
	m := projectsTestModel()
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Status: session.StatusAwaitingInput}
	m.sessions["n1:s4"] = session.Session{ID: "n1:s4", WorkspaceID: "n1:w1", Status: session.StatusDead}
	act := m.workspaceActivity()
	if a := act["n1:w1"]; a.live != 2 || a.waiting != 1 {
		t.Fatalf("w1 activity = %+v, want 2 live (dead excluded), 1 waiting", a)
	}
	line := ansi.Strip(m.projRowLine(m.left.tree.rows[3], false, true, act, 40))
	if !strings.HasSuffix(line, "◆ 2") {
		t.Errorf("waiting workspace row = %q, want a ◆ 2 badge", line)
	}
	if line := m.projRowLine(m.left.tree.rows[2], false, true, act, 40); strings.Contains(line, "◆") {
		t.Errorf("unfolded project row should not repeat the badge: %q", line)
	}
	m.left.tree.setFolded("n1:p1", true)
	if line := ansi.Strip(m.projRowLine(m.left.tree.rows[2], false, true, act, 40)); !strings.HasSuffix(line, "◆ 3") {
		t.Errorf("folded project row = %q, want the summed ◆ 3 badge", line)
	}
}

func TestProjectRowShowsSummary(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:p1")
	out := m.View().Content
	if strings.Contains(out, "Sessions") {
		t.Error("a project row should not show workspace tabs")
	}
	for _, want := range []string{"repo-feat", "feature"} {
		if !strings.Contains(out, want) {
			t.Errorf("project summary missing %q", want)
		}
	}
}

func TestHelpOverlay(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 40
	res, _ := m.runKey(seqKey("g?"))
	mm := res.(model)
	if !mm.showHelp || !strings.Contains(mm.View().Content, "force remove") {
		t.Fatal("g? should open the full key list")
	}
	res, _ = mm.runKey(tea.KeyPressMsg{Code: 'j'})
	mm = res.(model)
	if mm.showHelp || mm.left.tree.cursor != 0 {
		t.Errorf("any key should only close help: showHelp=%v cursor=%d", mm.showHelp, mm.left.tree.cursor)
	}
}

func TestTreeMoveKeepsThePane(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1")
	m = withPane(m, workspaceComp{ws: "n1:w1", cursor: 1})
	res, _ := m.runKey(tea.KeyPressMsg{Code: 'j'})
	mm := res.(model)
	if paneOf(mm).ws != "n1:w1" || paneOf(mm).cursor != 1 {
		t.Errorf("moving to n1:w2 must keep the pane: ws=%q cursor=%d, want n1:w1 on card 1", paneOf(mm).ws, paneOf(mm).cursor)
	}
}

func TestFooterFollowsFocus(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m = selectRow(m, "n1:w1")
	if f := m.currentFooter(); !strings.Contains(f, "fold") || strings.Contains(f, "tabs") {
		t.Errorf("tree footer = %q", f)
	}
	m = withFocus(m, mainPane)
	if f := m.currentFooter(); strings.Contains(f, "fold") || !strings.Contains(f, "jump") {
		t.Errorf("pane footer = %q", f)
	}
	m = withFile(m, fileComp{ws: "n1:w1", path: "a.go", diff: true})
	if f := m.currentFooter(); !strings.Contains(f, "scroll") || !strings.Contains(f, "close") {
		t.Errorf("viewer footer = %q", f)
	}
}

func TestFilterInputIsLive(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1") // hidden by the filter, so the cursor moves to the match
	res, _ := m.runKey(tea.KeyPressMsg{Code: '/'})
	m = res.(model)
	for _, r := range "feat" {
		res, _ = m.runKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	if m.left.tree.filter != "feat" || len(m.left.tree.rows) != 4 || m.left.tree.cursorRowID() != "n1:w2" {
		t.Fatalf("live filter: filter=%q rows=%+v cursor=%q", m.left.tree.filter, m.left.tree.rows, m.left.tree.cursorRowID())
	}
	res, _ = m.runKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.inputActive() || m.left.tree.filter != "feat" {
		t.Fatalf("enter should keep the filter and close the input")
	}
	res, _ = m.runKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = res.(model)
	if m.left.tree.filter != "" || viewOf(m) != viewTree || len(m.left.tree.rows) != 5 {
		t.Errorf("esc should clear the filter before leaving: filter=%q view=%v", m.left.tree.filter, viewOf(m))
	}
}

func TestOpenWorkspaceClearsFilter(t *testing.T) {
	filtered := func(id string) model {
		m := projectsTestModel()
		m.width, m.height = 120, 30
		m.left.tree.setFilter("feat")
		return selectRow(m, id)
	}
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyMsg("l")} {
		m, _ := upd(filtered("n1:w2"), k)
		if m.left.tree.filter != "" || m.left.tree.cursorRowID() != "n1:w2" || m.mainRow() != "n1:w2" {
			t.Errorf("%s on a workspace should clear the filter: filter=%q cursor=%q main=%q",
				k.String(), m.left.tree.filter, m.left.tree.cursorRowID(), m.mainRow())
		}
	}

	m, _ := upd(filtered("n1:p1"), keyMsg("l"))
	if m.left.tree.filter != "feat" {
		t.Errorf("l on a project should keep the filter: %q", m.left.tree.filter)
	}

	m = filtered("n1:w2")
	m.main = backStack{workspaceComp{ws: "n1:w2"}} // already open
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.left.tree.filter != "" {
		t.Errorf("enter on the open workspace should clear the filter: %q", m.left.tree.filter)
	}
}

func TestRevealClearsFilterThatHidesTarget(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.setFilter("feat")
	m.left.tree.reveal("n1:w1")
	if m.left.tree.cursorRowID() != "n1:w1" || m.left.tree.filter != "" {
		t.Errorf("reveal should clear a filter that hides the row: cursor=%q filter=%q", m.left.tree.cursorRowID(), m.left.tree.filter)
	}

	m.left.tree.setFilter("repo")
	m.left.tree.reveal("n1:w2")
	if m.left.tree.cursorRowID() != "n1:w2" || m.left.tree.filter != "repo" {
		t.Errorf("reveal should keep a filter that shows the row: cursor=%q filter=%q", m.left.tree.cursorRowID(), m.left.tree.filter)
	}
}

func TestBlankFilterKeepsFolding(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:p1")
	for _, k := range []tea.KeyPressMsg{{Code: '/', Text: "/"}, {Code: ' ', Text: " "}, {Code: tea.KeyEnter}} {
		m, _ = upd(m, k)
	}
	if m.left.tree.filter != "" {
		t.Fatalf("a blank filter should not apply: %q", m.left.tree.filter)
	}
	m, _ = upd(m, keyMsg("h"))
	if !m.left.tree.collapsed["n1:p1"] {
		t.Error("h should fold the project after a blank filter")
	}
	m = moveTree(m, len(m.left.tree.rows)+3)
	if m.left.tree.cursor >= len(m.left.tree.rows) {
		t.Errorf("moveTree should clamp to the last row: cursor=%d rows=%d", m.left.tree.cursor, len(m.left.tree.rows))
	}
}

func TestFilterFromPaneFiltersSessions(t *testing.T) {
	for _, width := range []int{120, 70} { // 70 collapses the tree
		m := projectsTestModel()
		m.width, m.height = width, 30
		m = selectRow(m, "n1:w1")
		m = withFocus(m, mainPane)
		m, _ = upd(m, tea.KeyPressMsg{Code: '/', Text: "/"})
		if m.inputActive() || m.focused != mainPane || !m.rootComp().raw(&ctx{m: &m}) {
			t.Errorf("width %d: / from the pane should open the session filter: tree input=%v focus=%v",
				width, m.inputActive(), m.focused)
		}
	}
}

func TestSessionCardWidthCapped(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 200, 30
	m = selectRow(m, "n1:w1")
	for _, ln := range strings.Split(paneOf(m).sessions(&ctx{m: &m}, 160, 20), "\n") {
		if w := lipgloss.Width(ln); w > maxCardWidth {
			t.Fatalf("card line is %d wide, want at most %d: %q", w, maxCardWidth, ln)
		}
	}
}

func TestHiddenAndPinnedProjectsAreMarked(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.left.tree.data[0].Hidden, m.left.tree.data[0].Pinned = true, true
	m.left.tree.showHidden = true
	m.left.tree.rebuild()
	r := m.left.tree.rows[2]
	if !r.hidden || !r.pinned {
		t.Fatalf("project row flags = hidden:%v pinned:%v, want both", r.hidden, r.pinned)
	}
	line := m.projRowLine(r, false, true, nil, 60)
	for _, want := range []string{Icon.Hidden.Glyph, Icon.Pin.Glyph} {
		if !strings.Contains(line, want) {
			t.Errorf("project row %q missing marker %q", line, want)
		}
	}
	if !strings.Contains(ansi.Strip(treePane(m, 40, 20)), "+hidden") {
		t.Error("tree title should say hidden projects are shown")
	}
}

func TestToggleHiddenFlashNamesProject(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m = selectRow(m, "n1:p1")
	_, cmd := treeKey(m, keyMsg("H"))
	res, _ := m.Update(cmd())
	if got := res.(model).flash; !strings.Contains(got, "hid argus") {
		t.Errorf("flash = %q, want it to name the hidden project", got)
	}
}

func TestToggleHiddenFlashOnUnhide(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m.left.tree.data[0].Hidden = true
	m.left.tree.showHidden = true
	m.left.tree.rebuild()
	m = selectRow(m, "n1:p1")
	_, cmd := treeKey(m, keyMsg("H"))
	res, _ := m.Update(cmd())
	if got := res.(model).flash; got != "unhid argus" {
		t.Errorf("flash = %q, want %q", got, "unhid argus")
	}
}

func TestGoneRowsNeedTheirOwnToggle(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.left.tree.data[0].Workspaces[1].IsGone = true
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{ID: "n1:p2", Name: "old", NodeID: "n1", IsGone: true})
	m.left.tree.rebuild()
	if len(m.left.tree.rows) != 4 {
		t.Fatalf("gone rows should be hidden by default: %+v", m.left.tree.rows)
	}
	res, _ := m.runKey(seqKey("z."))
	if got := len(res.(model).left.tree.rows); got != 4 {
		t.Errorf("z. (hidden) should not reveal gone rows, got %d rows", got)
	}
	res, _ = m.runKey(seqKey("zg"))
	mm := res.(model)
	if len(mm.left.tree.rows) != 6 {
		t.Fatalf("zg should reveal the gone workspace and project: %+v", mm.left.tree.rows)
	}
	if !strings.Contains(ansi.Strip(treePane(mm, 40, 20)), "+gone") {
		t.Error("tree title should say gone rows are shown")
	}
}

func TestGoneWorkspaceLabel(t *testing.T) {
	m := projectsTestModel()
	line := ansi.Strip(m.projRowLine(projectsRow{kind: rowWorkspace, label: "repo-feat", isGone: true}, false, true, nil, 60))
	if strings.Contains(line, "detached") || !strings.Contains(line, "(gone)") {
		t.Errorf("gone row = %q, want only the (gone) mark", line)
	}
}

func TestPlainWorkspaceLabel(t *testing.T) {
	m := projectsTestModel()
	rows := buildProjectRows(projectTreeComp{data: []api.ProjectNode{{
		ID: "n1:p", NodeID: "n1", Name: "notes", Kind: "plain",
		Workspaces: []api.WorkspaceNode{{ID: "n1:w", Dir: "/home/u/notes", IsMain: true}},
	}}})
	line := ansi.Strip(m.projRowLine(rows[1], false, true, nil, 60))
	if strings.Contains(line, "detached") {
		t.Errorf("plain workspace row = %q, want no detached mark", line)
	}
}

func TestRetargetWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m = selectRow(m, "n1:w2")
	res, cmd := m.runKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	m = res.(model)
	if _, open := retargetOf(m); !open || cmd == nil {
		t.Fatal("T should open the target picker and load branches")
	}
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main", Local: true}, {Name: "dev", Local: true}}})
	m = res.(model)
	for _, r := range "dev" {
		res, _ = m.runKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	res, cmd = m.runKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if _, open := retargetOf(m); open || cmd == nil {
		t.Fatal("enter should close the picker and call setTarget")
	}
	cmd()
	rc := m.client.(*recordingClient)
	p := rc.params[len(rc.params)-1].(api.WorkspaceSetTargetParams)
	if p.WorkspaceID != "n1:w2" || p.TargetBranch != "dev" {
		t.Errorf("setTarget params = %+v", p)
	}
}

func TestRetargetNeedsWorkspaceRow(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:p1")
	res, _ := m.runKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	if _, open := retargetOf(res.(model)); open || res.(model).flash == "" {
		t.Error("T on a project row should only show a hint")
	}
}

func TestSpawnSessionInWorkspace(t *testing.T) {
	for _, focus := range []container{leftSidebar, mainPane} {
		m := projectsTestModel()
		m.width, m.height = 120, 30
		m.client = &recordingClient{}
		m = selectRow(m, "n1:w2")
		m = withFocus(m, focus)
		res, cmd := m.runKey(tea.KeyPressMsg{Code: 's', Text: "s"})
		m = res.(model)
		if !spawnOpen(m) || spawnOf(m).nodeID != "n1" || !spawnOf(m).fixedCwd || spawnOf(m).cwd.Value() != "/repo-feat" || cmd == nil {
			t.Errorf("focus %v: s should start a spawn fixed to n1:/repo-feat: %+v", focus, spawnOf(m))
		}
	}
}

func TestSpawnSessionNeedsLiveWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.left.tree.data[0].Workspaces[0].IsMain = false // a bare repo: no main worktree
	m.left.tree.rebuild()
	m = selectRow(m, "n1:p1")
	res, _ := m.runKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if mm := res.(model); spawnOpen(mm) || mm.flash == "" {
		t.Error("s on a project with no main workspace should only show a hint")
	}

	m.left.tree.data[0].Workspaces[1].IsGone = true
	m.left.tree.showGone = true
	m.left.tree.rebuild()
	m = selectRow(m, "n1:w2")
	res, _ = m.runKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if mm := res.(model); spawnOpen(mm) || mm.flash == "" {
		t.Error("s on a gone workspace should only show a hint")
	}
}

func TestSessionFromProjectsRendersInPane(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1")
	m = withFocus(m, mainPane)
	mm, _ := m.enterSession("n1:s1")
	if got, want := mm.bodyWidth(), mm.frameWidth()-mm.projectsLeftW()-dividerWidth; got != want {
		t.Errorf("bodyWidth = %d, want the pane width %d", got, want)
	}
	if got := mm.bodyHeight(); got != 28 {
		t.Errorf("bodyHeight = %d, want 28 (below the frame header)", got)
	}
	out := ansi.Strip(mm.View().Content)
	if !strings.Contains(out, "Projects") {
		t.Error("the sidebar should stay visible while a session is open")
	}
	for i, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w > 120 {
			t.Fatalf("line %d is %d wide, wider than the terminal", i, w)
		}
	}
}

func TestSessionFromHomeEmbeds(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = withView(m, viewHome)
	mm, _ := m.enterSession("n1:s1")
	if mm.bodyWidth() != mm.frameWidth()-mm.projectsLeftW()-dividerWidth || mm.bodyHeight() != 28 {
		t.Errorf("Home session body = %dx%d, want the pane", mm.bodyWidth(), mm.bodyHeight())
	}
}

func TestScreenFromPaneSessionUsesPaneSize(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = withViews(m, viewTree, viewSession, viewScreen)
	cols, _ := m.termDims()
	if want := m.bodyWidth() - 2 - screenMargin; cols != want || m.bodyWidth() == 120 {
		t.Errorf("screen cols = %d, want pane-based %d", cols, want)
	}
	m.left.hidden = true
	if cols, _ := m.termDims(); cols != m.frameWidth()-2-screenMargin {
		t.Errorf("screen with the sidebar hidden: cols = %d, want full-width %d", cols, m.frameWidth()-2-screenMargin)
	}
}

func TestCtrlBInPaneSessionTogglesSidebar(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1")
	mm, _ := m.enterSession("n1:s1")
	if got := typeKeys(mm, " o"); got.bodyWidth() != got.frameWidth() || viewOf(got) != viewSession {
		t.Errorf("␣o should hide the sidebar and keep the session: width=%d view=%v", got.bodyWidth(), viewOf(got))
	}
}

func TestKillFromWorkspaceSessionsTab(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	rc := &recordingClient{}
	m.client = rc
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Repo: "repo", Tmux: session.TmuxLocation{PaneID: "%1"}}
	m = selectRow(m, "n1:w1")
	m = withFocus(m, mainPane)

	res, _ := m.runKey(seqKey("dd"))
	m = res.(model)
	if paneOf(m).killID != "n1:s1" || !strings.Contains(ansi.Strip(m.currentFooter()), "kill session repo · %1? y/n") {
		t.Fatalf("dd should ask to kill n1:s1: pending=%q footer=%q", paneOf(m).killID, m.currentFooter())
	}
	res, _ = m.runKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if mm := res.(model); paneOf(mm).killID != "" {
		t.Error("any key but y should cancel")
	}
	res, cmd := m.runKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if paneOf(res.(model)).killID != "" || cmd == nil {
		t.Fatal("y should clear the prompt and kill")
	}
	cmd()
	if p := rc.params[len(rc.params)-1].(api.SessionRef); p.SessionID != "n1:s1" {
		t.Errorf("kill sent for %q, want n1:s1", p.SessionID)
	}
}

func TestPaneCursorClampsWhenLastSessionGoes(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1") // sessions n1:s1, n1:s3
	m = withFocus(m, mainPane)
	m, _ = upd(m, keyMsg("j"))
	if paneOf(m).cursor != 1 {
		t.Fatalf("j should select the second card: cursor=%d", paneOf(m).cursor)
	}
	m, _ = upd(m, sessionsReplacedMsg([]session.Session{m.sessions["n1:s1"], m.sessions["n1:s2"]}))
	if paneOf(m).cursor != 0 {
		t.Errorf("the cursor should move to the remaining card: cursor=%d", paneOf(m).cursor)
	}
}

type failingClient struct {
	recordingClient
	err error
}

func (c *failingClient) Call(string, any, any) error { return c.err }

func TestRemovePromptNamesWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	delete(m.sessions, "n1:s2") // w2 has no live sessions
	m = selectRow(m, "n1:w2")
	m = typeKeys(m, "dd")
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "remove workspace repo-feat (feature)? y/n") {
		t.Errorf("remove prompt = %q", f)
	}
	m, _ = upd(m, keyMsg("n"))
	m, _ = upd(m, keyMsg("D"))
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "force-remove workspace repo-feat (feature)? uncommitted changes are lost · y/n") {
		t.Errorf("force-remove prompt = %q", f)
	}
}

func TestRemoveInFlightShowsAndBlocksRepeat(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.left.width = 44
	m.client = &recordingClient{}
	delete(m.sessions, "n1:s2")
	m = selectRow(m, "n1:w2")
	m = typeKeys(m, "dd")
	m, cmd := upd(m, keyMsg("y"))
	if cmd == nil || !strings.Contains(ansi.Strip(m.View().Content), "removing…") {
		t.Fatalf("a confirmed remove should show on its row:\n%s", ansi.Strip(m.View().Content))
	}
	m = typeKeys(m, "dd")
	if m.left.tree.pendingRemove != "" || !strings.Contains(m.flash, "already removing repo-feat") {
		t.Errorf("dd during a remove: pending=%q flash=%q", m.left.tree.pendingRemove, m.flash)
	}
	m, _ = upd(m, cmd())
	if strings.Contains(ansi.Strip(m.View().Content), "removing…") {
		t.Error("the mark should clear when the remove finishes")
	}
}

func TestRemoveRefusesLiveSessions(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w2") // n1:s2 is live here
	for _, k := range []string{"dd", "D"} {
		mm := typeKeys(m, k)
		if mm.left.tree.pendingRemove != "" || !strings.Contains(mm.flash, "repo-feat has 1 live session · kill it first") {
			t.Errorf("%s with a live session: pending=%q flash=%q", k, mm.left.tree.pendingRemove, mm.flash)
		}
	}
}

func TestMultiLineErrorFlashesOneLine(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m, _ = upd(m, projectsActionMsg{verb: "remove workspace", err: errString("first line\nsecond line")})
	if f := ansi.Strip(m.currentFooter()); strings.Contains(f, "\n") || !strings.Contains(f, "remove workspace: first line …") {
		t.Errorf("footer = %q, want only the first line", f)
	}
	h := homeTestModel()
	h.flash = "a\nb"
	if f := ansi.Strip(h.View().Content); !strings.Contains(f, "a …") {
		t.Errorf("Home footer should show one line:\n%s", f)
	}
}

func TestWorkspacePaneListKeys(t *testing.T) {
	t.Setenv("TMUX", "")
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1") // n1:s1, n1:s3
	m = withFocus(m, mainPane)
	m, _ = upd(m, keyMsg("G"))
	if paneOf(m).cursor != 1 {
		t.Errorf("G should select the last card: cursor=%d", paneOf(m).cursor)
	}
	m = typeKeys(m, "gg")
	if paneOf(m).cursor != 0 {
		t.Errorf("gg should select the first card: cursor=%d", paneOf(m).cursor)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if paneOf(m).cursor != 1 {
		t.Errorf("^d should page down: cursor=%d", paneOf(m).cursor)
	}
	m, _ = upd(m, keyMsg("O"))
	if m.flash == "" {
		t.Error("O should try to jump to the session's pane")
	}
}

func TestManageKeysDoNothingInThePane(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w2")
	m = withFocus(m, mainPane)
	for _, k := range []string{"a", "r", "H", "P", "T", "D"} {
		mm, _ := upd(m, keyMsg(k))
		if mm.flash != "" || createOpen(mm) || mm.left.tree.pendingRemove != "" {
			t.Errorf("%s in the pane: flash=%q", k, mm.flash)
		}
	}
	if h := ansi.Strip(m.projectsHelpView()); !strings.Contains(h, "Manage (tree)") {
		t.Error("the help should say the manage keys act in the tree")
	}
}

func TestTreeFooterListsOnlyKeysForTheRow(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 200, 30
	for row, want := range map[string]struct{ has, lacks []string }{
		homeRowID: {has: []string{"s spawn", "/ filter"}, lacks: []string{"enter open", "^ww", "fold", "a new", "dd remove"}},
		"n1:p1":   {has: []string{"h/l fold", "s spawn", "a new"}, lacks: []string{"^ww", "dd remove"}},
		"n1:w2":   {has: []string{"h/l fold", "s spawn", "a new", "dd remove"}, lacks: []string{"^ww"}},
	} {
		m = selectRow(m, row)
		f := ansi.Strip(m.currentFooter())
		for _, w := range want.has {
			if !strings.Contains(f, w) {
				t.Errorf("%s footer lacks %q: %q", row, w, f)
			}
		}
		for _, w := range want.lacks {
			if strings.Contains(f, w) {
				t.Errorf("%s footer has dead key %q: %q", row, w, f)
			}
		}
	}
}

func TestProjectHintFollowsMappedKeys(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"project-tree": {
		"<C-n>": "workspace new", "<C-r>": "project rename",
	}})
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:p1")
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "^n new workspace · ^r rename") {
		t.Errorf("the project hint names the mapped keys:\n%s", out)
	}
}

func TestProjectHintShowsBackAndManageKeys(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:p1")
	out := ansi.Strip(m.View().Content)
	if strings.Contains(out, "l unfold") {
		t.Error("project hint must not show l unfold")
	}
	if !strings.Contains(out, "esc tree:") {
		t.Error("project hint should show the back key with tree label")
	}
	m.left.tree.setFolded("n1:p1", true)
	m = selectRow(m, "n1:p1")
	out = ansi.Strip(m.View().Content)
	if strings.Contains(out, "l unfold") {
		t.Error("a folded project hint must not show l unfold")
	}
	if !strings.Contains(out, "esc tree:") {
		t.Error("a folded project hint should still show the back key with tree label")
	}
}

func TestForgetProject(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:p1")
	mm, _ := upd(m, keyMsg("F"))
	if mm.left.tree.pendingForget != "" || !strings.Contains(mm.flash, "argus has 3 live sessions · kill them first") {
		t.Errorf("F with live sessions: pending=%q flash=%q", mm.left.tree.pendingForget, mm.flash)
	}
	m.sessions = map[string]session.Session{}
	m.order = nil
	rc := &recordingClient{}
	m.client = rc
	m, _ = upd(m, keyMsg("F"))
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "forget project argus? it leaves the list; its files stay · y/n") {
		t.Fatalf("forget prompt = %q", f)
	}
	if mm, _ := upd(m, keyMsg("n")); mm.left.tree.pendingForget != "" {
		t.Error("n should cancel")
	}
	m, cmd := upd(m, keyMsg("y"))
	runCmd(cmd)
	if p, ok := paramsFor(m, api.MethodProjectForget).(api.ProjectRef); !ok || p.ProjectID != "n1:p1" {
		t.Errorf("y should forget n1:p1: calls=%v", rc.calls)
	}
}

func TestRetargetShowsContext(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m.left.tree.data[0].Workspaces[1].TargetBranch = "dev"
	m.left.tree.rebuild()
	m = selectRow(m, "n1:w2")
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "repo-feat  feature → dev") {
		t.Errorf("the workspace header should show the target:\n%s", out)
	}
	m.left.tree.data[0].Workspaces[0].TargetBranch = "main"
	m.left.tree.rebuild()
	m = selectRow(m, "n1:w1")
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "→ main") {
		t.Errorf("a workspace targeting its own branch should not show an arrow:\n%s", out)
	}
	m = selectRow(m, "n1:w2")
	m, _ = upd(m, keyMsg("T"))
	m, _ = upd(m, branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main"}, {Name: "dev"}, {Name: "feat"}}})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Target for repo-feat · now dev") || !strings.Contains(out, "dev  (current)") {
		t.Errorf("the target picker should name the workspace and mark the current target:\n%s", out)
	}
	if rt, _ := retargetOf(m); rt.pick.cursor != 1 {
		t.Errorf("the cursor should start on the current target: %d", rt.pick.cursor)
	}
}

func TestSpawnFromHomeAndProjectRows(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m.left.tree.cursor = 0 // Home
	if _, cmd := upd(m, keyMsg("s")); cmd == nil {
		t.Error("s on the Home row should start the spawn flow")
	}
	m = selectRow(m, "n1:p1")
	mm, _ := upd(m, keyMsg("s"))
	if !spawnOpen(mm) || !spawnOf(mm).fixedCwd || spawnOf(mm).cwd.Value() != "/repo" {
		t.Errorf("s on a project row should spawn in its main workspace: active=%v cwd=%q", spawnOpen(mm), spawnOf(mm).cwd.Value())
	}
	for _, row := range []string{homeRowID, "n1:p1"} {
		m = selectRow(m, row)
		if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "s spawn") {
			t.Errorf("%s footer should offer s spawn: %q", row, f)
		}
	}
}

func TestActionFlashesNameTheirObject(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	for want, cmd := range map[string]tea.Cmd{
		"removed repo-feat":         m.removeWorkspaceCmd("n1:w2", false),
		"renamed to X":              m.renameProjectCmd("n1:p1", "X"),
		"target of repo-feat → dev": m.setTargetCmd("n1:w2", "dev"),
	} {
		if got := cmd().(projectsActionMsg).ok; got != want {
			t.Errorf("flash = %q, want %q", got, want)
		}
	}
	m.client = &failingClient{err: errString("x")}
	if v := m.setHiddenCmd("n1:p1", false, "")().(projectsActionMsg).verb; v != "unhide" {
		t.Errorf("unhide error verb = %q", v)
	}
	if v := m.setPinnedCmd("n1:p1", false, "")().(projectsActionMsg).verb; v != "unpin" {
		t.Errorf("unpin error verb = %q", v)
	}
}

func TestRemoveSelectsANeighbor(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{
		ID: "n1:p2", Name: "zeta", Kind: "git", NodeID: "n1",
		Workspaces: []api.WorkspaceNode{{ID: "n1:w3", Dir: "/zeta", IsMain: true, Branch: "main"}},
	})
	m.left.tree.rebuild()
	delete(m.sessions, "n1:s2")
	m = selectRow(m, "n1:w2") // the last workspace of argus; zeta's rows follow
	m = typeKeys(m, "dd")
	m, cmd := upd(m, keyMsg("y"))
	m, _ = upd(m, cmd())
	tree := []api.ProjectNode{m.left.tree.data[0], m.left.tree.data[1]}
	tree[0].Workspaces = tree[0].Workspaces[:1] // w2 is gone
	m, _ = upd(m, projectsTreeMsg{tree: tree})
	if got := m.left.tree.cursorRowID(); got != "n1:w1" {
		t.Errorf("after the remove the cursor should go to the workspace above: %q", got)
	}
}

func TestRemoveSelectsANeighborWhenTheTreeArrivesFirst(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{
		ID: "n1:p2", Name: "zeta", Kind: "git", NodeID: "n1",
		Workspaces: []api.WorkspaceNode{{ID: "n1:w3", Dir: "/zeta", IsMain: true, Branch: "main"}},
	})
	m.left.tree.rebuild()
	delete(m.sessions, "n1:s2")
	m = selectRow(m, "n1:w2")
	m = typeKeys(m, "dd")
	m, cmd := upd(m, keyMsg("y"))
	reply := cmd()
	tree := []api.ProjectNode{m.left.tree.data[0], m.left.tree.data[1]}
	tree[0].Workspaces = tree[0].Workspaces[:1]
	m, _ = upd(m, projectsTreeMsg{tree: tree})
	m, _ = upd(m, reply)
	if got := m.left.tree.cursorRowID(); got != "n1:w1" {
		t.Errorf("a tree that drops the workspace before the reply should still select the neighbor: %q", got)
	}
	if len(m.left.tree.removing) != 0 || len(m.left.tree.neighbors) != 0 {
		t.Errorf("the remove should be settled: removing=%v neighbors=%v", m.left.tree.removing, m.left.tree.neighbors)
	}
}

func removeW2WithZetaBelow(t *testing.T) (model, tea.Msg) {
	t.Helper()
	m := projectsTestModel()
	m.client = &recordingClient{}
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{
		ID: "n1:p2", Name: "zeta", Kind: "git", NodeID: "n1",
		Workspaces: []api.WorkspaceNode{{ID: "n1:w3", Dir: "/zeta", IsMain: true, Branch: "main"}},
	})
	m.left.tree.rebuild()
	delete(m.sessions, "n1:s2")
	m = selectRow(m, "n1:w2")
	m = typeKeys(m, "dd")
	m, cmd := upd(m, keyMsg("y"))
	return m, cmd()
}

func withoutW2(m model) []api.ProjectNode {
	tree := []api.ProjectNode{m.left.tree.data[0], m.left.tree.data[1]}
	tree[0].Workspaces = tree[0].Workspaces[:1]
	return tree
}

func TestFailedRemoveForgetsTheNeighbor(t *testing.T) {
	m, _ := removeW2WithZetaBelow(t)
	m, _ = upd(m, projectsActionMsg{verb: "remove workspace", removed: "n1:w2", err: errString("dirty")})
	if len(m.left.tree.neighbors) != 0 {
		t.Fatalf("a failed remove should drop its neighbor: %v", m.left.tree.neighbors)
	}
	m, _ = upd(m, projectsTreeMsg{tree: withoutW2(m)})
	if got := m.left.tree.cursorRowID(); got == "n1:w1" {
		t.Errorf("a later tree without the workspace should not jump to the old neighbor")
	}
}

func TestRemoveLeavesAMovedCursor(t *testing.T) {
	m, reply := removeW2WithZetaBelow(t)
	m, _ = upd(m, reply)
	m = selectRow(m, "n1:w3")
	m, _ = upd(m, projectsTreeMsg{tree: withoutW2(m)})
	if got := m.left.tree.cursorRowID(); got != "n1:w3" {
		t.Errorf("a cursor moved off the removed row should stay: %q", got)
	}
	if len(m.left.tree.neighbors) != 0 {
		t.Errorf("the remove should be settled: %v", m.left.tree.neighbors)
	}
}

func TestActionLeavesTheReloadToProjectChanged(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m, cmd := upd(m, projectsActionMsg{verb: "rename", ok: "renamed to x"})
	if n := len(projectReplies(cmd)); n != 0 || m.left.tree.loading {
		t.Errorf("an action should not fetch the tree itself: %d fetches, loading=%v", n, m.left.tree.loading)
	}
	if m.flash != "renamed to x" {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestUnknownWorkspaceRefetchesTree(t *testing.T) {
	m := projectsTestModel()
	rc := &recordingClient{}
	m.client = rc
	event := func(ws string) api.Notification {
		params, _ := json.Marshal(registry.Event{Type: registry.EventAdded, Session: session.Session{ID: "n1:s9", WorkspaceID: ws}})
		return api.Notification{Method: api.MethodSessionEvent, Params: params}
	}
	runCmd(m.applyEvent(event("n1:w1")))
	if len(rc.calls) != 0 {
		t.Fatalf("a known workspace should not refetch the tree: calls=%v", rc.calls)
	}
	runCmd(m.applyEvent(event("n1:w-new")))
	if !m.left.tree.loading || len(rc.calls) == 0 || rc.calls[len(rc.calls)-1] != api.MethodProjectList {
		t.Errorf("an unknown workspace should refetch the tree: calls=%v", rc.calls)
	}
	m.left.tree.loading = false
	n := len(rc.calls)
	runCmd(m.applyEvent(event("n1:w-new")))
	if len(rc.calls) != n {
		t.Errorf("a workspace the tree still lacks should not refetch on every event: calls=%v", rc.calls)
	}
}

func TestPaneOfAMissingWorkspaceSaysSo(t *testing.T) {
	m := projectsTestModel()
	if out := ansi.Strip(workspaceComp{ws: "n1:gone"}.column(&ctx{m: &m}, 0, 80, 20)); out != "workspace not found" {
		t.Errorf("column = %q, want workspace not found", out)
	}
}

func TestProjectGitErrorShows(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.left.tree.data[0].Error = "worktree list: git missing"
	m.left.tree.rebuild()
	m = selectRow(m, "n1:p1")
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "argus (git error)") || !strings.Contains(out, "git error: worktree list: git missing") {
		t.Errorf("a project with a git error should say so in the tree and its pane:\n%s", out)
	}
}

func TestKillFailureIsFlashed(t *testing.T) {
	m := projectsTestModel()
	m.client = &failingClient{err: errString("no such pane")}
	msg := m.killCmd("n1:s1")()
	m, _ = upd(m, msg)
	if !strings.Contains(m.flash, "kill failed: no such pane") {
		t.Errorf("a failed kill should be flashed: %q", m.flash)
	}
}

func TestKillFromWorkspaceNeedsTerminalControl(t *testing.T) {
	m := projectsTestModel() // fixture sessions have no tmux pane
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1")
	m = withFocus(m, mainPane)
	res, _ := m.runKey(seqKey("dd"))
	if mm := res.(model); paneOf(mm).killID != "" || mm.flash == "" {
		t.Errorf("a session without terminal control should only show a hint: pending=%q flash=%q", paneOf(mm).killID, mm.flash)
	}
}

// projectOnCursor is the tree with the cursor on the argus project row, its
// pinned and hidden flags set as given, and a client that records calls.
func projectOnCursor(pinned, hidden bool) (model, *recordingClient) {
	m := projectsTestModel()
	m.left.tree.data[0].Pinned, m.left.tree.data[0].Hidden = pinned, hidden
	m.left.tree.showHidden = true
	m.left.tree.rebuild()
	m.left.tree.cursor = 2
	rc := &recordingClient{}
	m.client = rc
	return m, rc
}

func flagCalls(rc *recordingClient) []string {
	var out []string
	for i, method := range rc.calls {
		if p, ok := rc.params[i].(api.ProjectFlagParams); ok {
			out = append(out, fmt.Sprintf("%s=%v", method, p.Value))
		}
	}
	return out
}

func TestSharedKeyRunsTheCommandThatApplies(t *testing.T) {
	for _, tc := range []struct {
		pinned, hidden bool
		key            string
		want           string
	}{
		{false, false, "P", api.MethodProjectSetPinned + "=true"},
		{true, false, "P", api.MethodProjectSetPinned + "=false"},
		{false, false, "H", api.MethodProjectSetHidden + "=true"},
		{false, true, "H", api.MethodProjectSetHidden + "=false"},
	} {
		m, rc := projectOnCursor(tc.pinned, tc.hidden)
		_, cmd := upd(m, keyMsg(tc.key))
		runCmd(cmd)
		if got := flagCalls(rc); len(got) != 1 || got[0] != tc.want {
			t.Errorf("pinned=%v hidden=%v %s: calls %v, want [%s]", tc.pinned, tc.hidden, tc.key, got, tc.want)
		}
	}
}

func TestRemappedHalfOfAPair(t *testing.T) {
	// "p" is a new key for pin only: P still pins and unpins.
	m, rc := projectOnCursor(true, false)
	m = withKeymap(m, map[string]map[string]string{"project-tree": {"p": "project pin"}})
	_, cmd := upd(m, keyMsg("P"))
	runCmd(cmd)
	if got := flagCalls(rc); len(got) != 1 || got[0] != api.MethodProjectSetPinned+"=false" {
		t.Errorf("P on a pinned project must still unpin: %v", got)
	}
	// "p" on a pinned project: pin does not apply, so nothing runs.
	m, rc = projectOnCursor(true, false)
	m = withKeymap(m, map[string]map[string]string{"project-tree": {"p": "project pin"}})
	_, cmd = upd(m, keyMsg("p"))
	runCmd(cmd)
	if got := flagCalls(rc); len(got) != 0 {
		t.Errorf("a command that does not apply must do nothing: %v", got)
	}
	// P mapped to pin only: unpin loses P.
	m, rc = projectOnCursor(true, false)
	m = withKeymap(m, map[string]map[string]string{"project-tree": {"P": "project pin"}})
	_, cmd = upd(m, keyMsg("P"))
	runCmd(cmd)
	if got := flagCalls(rc); len(got) != 0 {
		t.Errorf("P mapped to pin must not unpin: %v", got)
	}
}

func TestHelpRowFollowsTheHalfThatApplies(t *testing.T) {
	row := func(m model, desc string) helpRow {
		for _, g := range m.helpGroups() {
			for _, r := range g.rows {
				if r.desc == desc {
					return r
				}
			}
		}
		t.Fatalf("no help row %q", desc)
		return helpRow{}
	}
	m, _ := projectOnCursor(true, false)
	m = withKeymap(m, map[string]map[string]string{"project-tree": {"P": "project unpin", "p": "project pin"}})
	if r := row(m, "pin / unpin project"); r.name != "project unpin" || !strings.HasPrefix(r.key, "P") {
		t.Errorf("a pinned project's row must show unpin and its key: name %q key %q", r.name, r.key)
	}
	if r := row(m, "hide / unhide project"); r.name != "project hide" {
		t.Errorf("a shown project's row must show hide: name %q", r.name)
	}
}

func TestWorkspaceRowsShowFolderOrBranch(t *testing.T) {
	out := ansi.Strip(homeTestModel().View().Content)
	for _, want := range []string{Icon.Folder.Glyph + " repo", Icon.Branch.Glyph + " repo-feat"} {
		if !strings.Contains(out, want) {
			t.Errorf("the tree should show %q:\n%s", want, out)
		}
	}
}

func TestTreeCursorMovesBeforeTheViewScrolls(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	for i := range 60 {
		m.left.tree.data = append(m.left.tree.data, api.ProjectNode{
			ID: fmt.Sprintf("n1:q%d", i), Name: fmt.Sprintf("proj-%02d", i), NodeID: "n1", NodeLabel: "home"})
	}
	m.left.tree.rebuild()
	m = typeKeys(m, "G")
	last := m.left.tree.rows[len(m.left.tree.rows)-1].label
	if !strings.Contains(ansi.Strip(m.View().Content), last) {
		t.Fatalf("setup: G should show the last row %q", last)
	}
	// Like j/k in the transcript: k moves the cursor up inside the view; the view
	// scrolls only once the cursor reaches the top edge.
	m = typeKeys(m, "k")
	if !strings.Contains(ansi.Strip(m.View().Content), last) {
		t.Errorf("k from the bottom scrolled the view; %q should stay visible", last)
	}
}
