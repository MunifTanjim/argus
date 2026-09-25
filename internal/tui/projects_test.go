package tui

import (
	"encoding/json"
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
	m.projects.sidebarHidden = false
	m.projects.collapsed = map[string]bool{}
	m.projects.tree = []api.ProjectNode{{
		ID: "n1:p1", Name: "argus", Kind: "git", Dir: "/repo/.git", NodeID: "n1", NodeLabel: "home",
		Workspaces: []api.WorkspaceNode{
			{ID: "n1:w1", Dir: "/repo", IsMain: true, Branch: "main"},
			{ID: "n1:w2", Dir: "/repo-feat", Branch: "feature"},
		},
	}}
	m.projects.rebuild()
	m.sessions = map[string]session.Session{
		"n1:s1": {ID: "n1:s1", WorkspaceID: "n1:w1", Repo: "repo"},
		"n1:s2": {ID: "n1:s2", WorkspaceID: "n1:w2", Repo: "repo"},
		"n1:s3": {ID: "n1:s3", WorkspaceID: "n1:w1", Repo: "repo"},
	}
	m.mode = modeProjects
	return m
}

func TestBuildProjectRows(t *testing.T) {
	m := projectsTestModel()
	// A single node shows no node row: project, then its two workspaces.
	if len(m.projects.rows) != 4 {
		t.Fatalf("got %d rows, want 4 (Home first): %+v", len(m.projects.rows), m.projects.rows)
	}
	if m.projects.rows[1].kind != rowProject || m.projects.rows[1].depth != 0 {
		t.Fatalf("unexpected first row: %+v", m.projects.rows[1])
	}
	if m.projects.rows[2].kind != rowWorkspace || m.projects.rows[3].kind != rowWorkspace {
		t.Fatalf("workspaces missing: %+v", m.projects.rows)
	}
}

func TestBuildProjectRowsShowsNodesWhenMany(t *testing.T) {
	tree := append(projectsTestModel().projects.tree, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2"})
	rows := buildProjectRows(projectsState{tree: tree})
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
	for _, r := range buildProjectRows(projectsState{tree: tree}) {
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
	rows := buildProjectRows(projectsState{tree: m.projects.tree, collapsed: map[string]bool{"n1:p1": true}, filter: "FEAT"})
	// The branch match shows its project and only that workspace, even when folded.
	if len(rows) != 2 || rows[1].id != "n1:w2" {
		t.Fatalf("filter rows = %+v, want project + n1:w2", rows)
	}
	if rows := buildProjectRows(projectsState{tree: m.projects.tree, filter: "argus"}); len(rows) != 3 {
		t.Errorf("a project-name match should keep all workspaces, got %d rows", len(rows))
	}
	if rows := buildProjectRows(projectsState{tree: m.projects.tree, filter: "zzz"}); len(rows) != 0 {
		t.Errorf("no match should give no rows, got %+v", rows)
	}
}

func TestPaneSessionsFilteredByWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:w1")
	if got := m.selectedWorkspaceID(); got != "n1:w1" {
		t.Fatalf("selectedWorkspaceID = %q, want n1:w1", got)
	}
	ss := m.paneSessions()
	if len(ss) != 2 { // s1 and s3 are in w1
		t.Fatalf("got %d sessions for w1, want 2", len(ss))
	}
	for _, s := range ss {
		if s.WorkspaceID != "n1:w1" {
			t.Errorf("session %s has workspace %q, want n1:w1", s.ID, s.WorkspaceID)
		}
	}

	m.projects.selectRow("n1:w2")
	if got := len(m.paneSessions()); got != 1 {
		t.Errorf("got %d sessions for w2, want 1", got)
	}
}

func TestProjectsCollapseTogglesRows(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:p1")
	res, _ := m.projectsEnter()
	mm := res.(model)
	// Collapsing the project hides its two workspaces.
	if len(mm.projects.rows) != 2 {
		t.Fatalf("after collapse got %d rows, want 2 (Home, project)", len(mm.projects.rows))
	}
}

func TestProjectsEnterOnWorkspaceFocusesPane(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:w1")
	res, _ := m.projectsEnter()
	mm := res.(model)
	if mm.projects.focus != focusPane {
		t.Errorf("focus = %v, want focusPane", mm.projects.focus)
	}
}

func TestProjectsViewRenders(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:w1")
	out := m.projectsView()
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
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: '>'})
	mm := res.(model)
	if mm.projectsLeftW() <= before {
		t.Errorf("widen: leftW %d did not grow past %d", mm.projectsLeftW(), before)
	}
	res, _ = mm.handleProjectsKey(tea.KeyPressMsg{Code: '<'})
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
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	mm := res.(model)
	if mm.sidebarVisible() {
		t.Error("ctrl+b should hide the sidebar")
	}
	if mm.projects.focus != focusPane {
		t.Error("hiding the sidebar should move focus to the pane")
	}
	res, _ = mm.handleProjectsKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !res.(model).sidebarVisible() {
		t.Error("ctrl+b again should show the sidebar")
	}
}

func TestNarrowingMovesFocusOffTheTree(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w1")
	m, _ = upd(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	if m.projects.focus != focusPane {
		t.Errorf("the pane should take focus when the tree collapses: focus=%v", m.projects.focus)
	}

	m = projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.cursor = 0
	m, _ = upd(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	if m.mode != modeList || m.projects.focus != focusPane {
		t.Errorf("on the Home row the Home pane should take focus: mode=%v focus=%v", m.mode, m.projects.focus)
	}
}

func TestHelpFitsCommonTerminal(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.showHelp = true
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"cycle focus", "kill session", "change target branch", "quit (tree)"} {
		if !strings.Contains(out, want) {
			t.Errorf("help at 120x30 is missing %q:\n%s", want, out)
		}
	}
}

func TestTogglesBelowBreakpointOnlyHint(t *testing.T) {
	for _, mode := range []viewMode{modeProjects, modeList} {
		m := projectsTestModel()
		m.mode = mode
		m.projects.focus = focusPane
		m.width, m.height = 70, 30
		mm, _ := upd(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		if mm.projects.sidebarHidden || !strings.Contains(mm.flash, "needs 80 columns") {
			t.Errorf("mode %v: ^b at 70 cols: hidden=%v flash=%q", mode, mm.projects.sidebarHidden, mm.flash)
		}
		m.width = 100
		m.projects.filesHidden = false
		mm, _ = upd(m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
		if mm.projects.filesHidden || !strings.Contains(mm.flash, "needs 120 columns") {
			t.Errorf("mode %v: ^e at 100 cols: hidden=%v flash=%q", mode, mm.projects.filesHidden, mm.flash)
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
	if out := m.projectsView(); !strings.Contains(out, "argus") {
		t.Error("collapsed projectsView did not render")
	}
}

func TestEnterSessionRecordsOrigin(t *testing.T) {
	// From the projects sidebar, exit should return here, not the flat list.
	m := projectsTestModel()
	mm, _ := m.enterSession("n1:s1")
	if mm.mode != modeSession {
		t.Fatalf("mode = %v, want modeSession", mm.mode)
	}
	if mm.sessionReturn != modeProjects {
		t.Errorf("sessionReturn = %v, want modeProjects", mm.sessionReturn)
	}

	// From the flat list, exit should return to the list.
	l := testModel()
	l.replyDrafts = map[string]string{}
	l.mode = modeList
	l.sessions = map[string]session.Session{"s1": {ID: "s1"}}
	ll, _ := l.enterSession("s1")
	if ll.sessionReturn != modeList {
		t.Errorf("sessionReturn = %v, want modeList", ll.sessionReturn)
	}
}

func TestSpinnerArmsOnProjectsScreen(t *testing.T) {
	m := projectsTestModel()
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Status: session.StatusWorking}
	m.mode = modeProjects
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
	m.projects.selectRow("n1:p1")
	res, _ := m.actNewWorkspace()
	mm := res.(model)
	if !mm.projects.create.active || mm.projects.create.projectID != "n1:p1" {
		t.Fatalf("new-workspace picker not open: %+v", mm.projects.create)
	}
}

func TestRemoveWorkspaceConfirm(t *testing.T) {
	m := projectsTestModel()
	delete(m.sessions, "n1:s2") // a live session would refuse the remove
	m.projects.selectRow("n1:w2")
	res, _ := m.actRemoveWorkspace(false)
	mm := res.(model)
	if mm.projects.pendingRemove != "n1:w2" {
		t.Fatalf("pendingRemove = %q, want n1:w2", mm.projects.pendingRemove)
	}
	res2, cmd := mm.handleRemoveConfirm(tea.KeyPressMsg{Code: 'y'})
	if res2.(model).projects.pendingRemove != "" {
		t.Error("pendingRemove not cleared after confirm")
	}
	if cmd == nil {
		t.Error("expected a remove command on y")
	}
}

func TestRemoveMainWorktreeRefused(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:w1")
	res, _ := m.actRemoveWorkspace(false)
	if res.(model).projects.pendingRemove != "" {
		t.Error("removing the main worktree should not arm a confirmation")
	}
}

func TestToggleHiddenEmitsCommand(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:p1")
	if _, cmd := m.actToggleHidden(); cmd == nil {
		t.Error("expected a set-hidden command")
	}
}

func TestShowHiddenFiltersRows(t *testing.T) {
	m := projectsTestModel()
	m.projects.tree[0].Hidden = true
	m.projects.rows = buildProjectRows(m.projects)
	if len(m.projects.rows) != 0 {
		t.Fatalf("hidden project should be filtered, got %d rows", len(m.projects.rows))
	}
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'z'})
	if len(res.(model).projects.rows) == 0 {
		t.Error("z should reveal hidden projects")
	}
}

func TestTreeLeftRight(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	press := func(m model, code rune) model {
		res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: code})
		return res.(model)
	}

	m.projects.selectRow("n1:w2")
	m = press(m, 'h') // workspace: step to the parent project
	if got := m.projects.cursorRowID(); got != "n1:p1" {
		t.Fatalf("h on a workspace: cursor on %q, want n1:p1", got)
	}
	m = press(m, 'h') // unfolded project: fold
	if !m.projects.collapsed["n1:p1"] || len(m.projects.rows) != 2 {
		t.Fatalf("h on an unfolded project should fold it: %+v", m.projects.rows)
	}
	m = press(m, 'l') // folded project: unfold
	if m.projects.collapsed["n1:p1"] || len(m.projects.rows) != 4 {
		t.Fatalf("l on a folded project should unfold it: %+v", m.projects.rows)
	}
	m = press(m, 'l') // unfolded project: step into the first workspace
	if got := m.projects.cursorRowID(); got != "n1:w1" {
		t.Fatalf("l on an unfolded project: cursor on %q, want n1:w1", got)
	}
	m = press(m, 'l') // workspace: focus the pane
	if m.projects.focus != focusPane {
		t.Errorf("l on a workspace should focus the pane")
	}
}

func TestFocusPaneNeedsWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:p1")
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyTab})
	mm := res.(model)
	if mm.projects.focus != focusTree || mm.flash == "" {
		t.Errorf("tab on a project row: focus=%v flash=%q, want tree focus and a hint", mm.projects.focus, mm.flash)
	}
}

func TestTreeReloadKeepsSelectionByID(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:w2")
	tree := []api.ProjectNode{m.projects.tree[0]}
	tree[0].Workspaces = append([]api.WorkspaceNode{{ID: "n1:w0", Dir: "/repo-a", Branch: "a"}}, tree[0].Workspaces...)
	res, _ := m.Update(projectsTreeMsg{tree: tree})
	if got := res.(model).projects.cursorRowID(); got != "n1:w2" {
		t.Errorf("after reload cursor on %q, want n1:w2", got)
	}
}

func TestCreateSelectsNewWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:p1")
	res, _ := m.Update(projectsActionMsg{verb: "create workspace", selectID: "n1:w3"})
	m = res.(model)
	tree := []api.ProjectNode{m.projects.tree[0]}
	tree[0].Workspaces = append(append([]api.WorkspaceNode{}, tree[0].Workspaces...), api.WorkspaceNode{ID: "n1:w3", Dir: "/repo-new", Branch: "new"})
	m.projects.collapsed["n1:p1"] = true
	res, _ = m.Update(projectsTreeMsg{tree: tree})
	mm := res.(model)
	if got := mm.projects.cursorRowID(); got != "n1:w3" {
		t.Errorf("after create cursor on %q, want n1:w3", got)
	}
	if mm.projects.want != "" {
		t.Errorf("want not cleared: %q", mm.projects.want)
	}
}

func TestRefreshKeepsTree(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: 'r'})
	mm := res.(model)
	if mm.projects.tree == nil || len(mm.projects.rows) == 0 || cmd == nil {
		t.Fatalf("refresh should keep the tree and fetch: tree=%v rows=%d", mm.projects.tree, len(mm.projects.rows))
	}
	if !strings.Contains(mm.projectsView(), "refreshing") {
		t.Error("refresh should show a refreshing hint")
	}
}

func TestUnfocusedTreeKeepsCursorBar(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w2")
	m.projects.focus = focusPane
	var sel string
	for _, ln := range strings.Split(ansi.Strip(m.projectsTreePane(40, 20)), "\n") {
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
	line := ansi.Strip(m.projRowLine(m.projects.rows[2], false, true, act, 40))
	if !strings.HasSuffix(line, "◆ 2") {
		t.Errorf("waiting workspace row = %q, want a ◆ 2 badge", line)
	}
	if line := m.projRowLine(m.projects.rows[1], false, true, act, 40); strings.Contains(line, "◆") {
		t.Errorf("unfolded project row should not repeat the badge: %q", line)
	}
	m.projects.setFolded("n1:p1", true)
	if line := ansi.Strip(m.projRowLine(m.projects.rows[1], false, true, act, 40)); !strings.HasSuffix(line, "◆ 3") {
		t.Errorf("folded project row = %q, want the summed ◆ 3 badge", line)
	}
}

func TestProjectRowShowsSummary(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:p1")
	out := m.projectsView()
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
	m.width, m.height = 160, 30
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: '?'})
	mm := res.(model)
	if !mm.projects.showHelp || !strings.Contains(mm.projectsView(), "force remove") {
		t.Fatal("? should open the full key list")
	}
	res, _ = mm.handleProjectsKey(tea.KeyPressMsg{Code: 'j'})
	mm = res.(model)
	if mm.projects.showHelp || mm.projects.cursor != 0 {
		t.Errorf("any key should only close help: showHelp=%v cursor=%d", mm.projects.showHelp, mm.projects.cursor)
	}
}

func TestTreeMoveSyncsPane(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w1")
	m.projects.dataWS = "n1:w1"
	m.projects.wsCursor = 2
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'j'})
	mm := res.(model)
	if mm.projects.dataWS != "n1:w2" || mm.projects.wsCursor != 0 {
		t.Errorf("moving to n1:w2 should reset the pane: dataWS=%q wsCursor=%d", mm.projects.dataWS, mm.projects.wsCursor)
	}
}

func TestFooterFollowsFocus(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m.projects.selectRow("n1:w1")
	if f := m.projectsFooter(); !strings.Contains(f, "fold") || strings.Contains(f, "tabs") {
		t.Errorf("tree footer = %q", f)
	}
	m.projects.focus = focusPane
	if f := m.projectsFooter(); strings.Contains(f, "tabs") || !strings.Contains(f, "tree") {
		t.Errorf("pane footer = %q", f)
	}
	m.projects.fileView = fileViewState{ws: "n1:w1", path: "a.go", diff: true}
	if f := m.projectsFooter(); !strings.Contains(f, "scroll") || !strings.Contains(f, "close") {
		t.Errorf("viewer footer = %q", f)
	}
}

func TestFilterInputIsLive(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w1") // hidden by the filter, so the cursor moves to the match
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: '/'})
	m = res.(model)
	for _, r := range "feat" {
		res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	if m.projects.filter != "feat" || len(m.projects.rows) != 3 || m.projects.cursorRowID() != "n1:w2" {
		t.Fatalf("live filter: filter=%q rows=%+v cursor=%q", m.projects.filter, m.projects.rows, m.projects.cursorRowID())
	}
	res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.inputActive() || m.projects.filter != "feat" {
		t.Fatalf("enter should keep the filter and close the input")
	}
	res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = res.(model)
	if m.projects.filter != "" || m.mode != modeProjects || len(m.projects.rows) != 4 {
		t.Errorf("esc should clear the filter before leaving: filter=%q mode=%v", m.projects.filter, m.mode)
	}
}

func TestRevealClearsFilterThatHidesTarget(t *testing.T) {
	m := projectsTestModel()
	m.projects.setFilter("feat")
	m.projects.reveal("n1:w1")
	if m.projects.cursorRowID() != "n1:w1" || m.projects.filter != "" {
		t.Errorf("reveal should clear a filter that hides the row: cursor=%q filter=%q", m.projects.cursorRowID(), m.projects.filter)
	}

	m.projects.setFilter("repo")
	m.projects.reveal("n1:w2")
	if m.projects.cursorRowID() != "n1:w2" || m.projects.filter != "repo" {
		t.Errorf("reveal should keep a filter that shows the row: cursor=%q filter=%q", m.projects.cursorRowID(), m.projects.filter)
	}
}

func TestBlankFilterKeepsFolding(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:p1")
	for _, k := range []tea.KeyPressMsg{{Code: '/', Text: "/"}, {Code: ' ', Text: " "}, {Code: tea.KeyEnter}} {
		m, _ = upd(m, k)
	}
	if m.projects.filter != "" {
		t.Fatalf("a blank filter should not apply: %q", m.projects.filter)
	}
	m, _ = upd(m, keyMsg("h"))
	if !m.projects.collapsed["n1:p1"] {
		t.Error("h should fold the project after a blank filter")
	}
	res, _ := m.moveTree(len(m.projects.rows) + 3)
	m = res.(model)
	if m.projects.cursor >= len(m.projects.rows) {
		t.Errorf("moveTree should clamp to the last row: cursor=%d rows=%d", m.projects.cursor, len(m.projects.rows))
	}
}

func TestFilterFromPaneFocusesTree(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w1")
	m.projects.focus = focusPane
	m, _ = upd(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if !m.inputActive() || m.projects.focus != focusTree {
		t.Errorf("/ from the pane should filter in the tree: input=%v focus=%v", m.inputActive(), m.projects.focus)
	}

	m = projectsTestModel()
	m.width, m.height = 70, 30 // tree collapsed
	m.projects.selectRow("n1:w1")
	m.projects.focus = focusPane
	m, _ = upd(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if m.inputActive() || !strings.Contains(m.flash, "shows the tree") {
		t.Errorf("/ with no tree on screen should only hint: input=%v flash=%q", m.inputActive(), m.flash)
	}
}

func TestSessionCardWidthCapped(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 200, 30
	m.projects.selectRow("n1:w1")
	for _, ln := range strings.Split(m.projectsSessionPane(160, 20), "\n") {
		if w := lipgloss.Width(ln); w > maxCardWidth {
			t.Fatalf("card line is %d wide, want at most %d: %q", w, maxCardWidth, ln)
		}
	}
}

func TestHiddenAndPinnedProjectsAreMarked(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.tree[0].Hidden, m.projects.tree[0].Pinned = true, true
	m.projects.showHidden = true
	m.projects.rebuild()
	r := m.projects.rows[1]
	if !r.hidden || !r.pinned {
		t.Fatalf("project row flags = hidden:%v pinned:%v, want both", r.hidden, r.pinned)
	}
	line := m.projRowLine(r, false, true, nil, 60)
	for _, want := range []string{Icon.Hidden.Glyph, Icon.Pin.Glyph} {
		if !strings.Contains(line, want) {
			t.Errorf("project row %q missing marker %q", line, want)
		}
	}
	if !strings.Contains(ansi.Strip(m.projectsTreePane(40, 20)), "+hidden") {
		t.Error("tree title should say hidden projects are shown")
	}
}

func TestToggleHiddenFlashNamesProject(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m.projects.selectRow("n1:p1")
	_, cmd := m.actToggleHidden()
	res, _ := m.Update(cmd())
	if got := res.(model).flash; !strings.Contains(got, "hid argus") {
		t.Errorf("flash = %q, want it to name the hidden project", got)
	}
}

func TestToggleHiddenFlashOnUnhide(t *testing.T) {
	m := projectsTestModel()
	m.client = &recordingClient{}
	m.projects.tree[0].Hidden = true
	m.projects.showHidden = true
	m.projects.rebuild()
	m.projects.selectRow("n1:p1")
	_, cmd := m.actToggleHidden()
	res, _ := m.Update(cmd())
	if got := res.(model).flash; got != "unhid argus" {
		t.Errorf("flash = %q, want %q", got, "unhid argus")
	}
}

func TestGoneRowsNeedTheirOwnToggle(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.tree[0].Workspaces[1].IsGone = true
	m.projects.tree = append(m.projects.tree, api.ProjectNode{ID: "n1:p2", Name: "old", NodeID: "n1", IsGone: true})
	m.projects.rebuild()
	if len(m.projects.rows) != 3 {
		t.Fatalf("gone rows should be hidden by default: %+v", m.projects.rows)
	}
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'z'})
	if got := len(res.(model).projects.rows); got != 3 {
		t.Errorf("z (hidden) should not reveal gone rows, got %d rows", got)
	}
	res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: 'o'})
	mm := res.(model)
	if len(mm.projects.rows) != 5 {
		t.Fatalf("o should reveal the gone workspace and project: %+v", mm.projects.rows)
	}
	if !strings.Contains(ansi.Strip(mm.projectsTreePane(40, 20)), "+gone") {
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
	rows := buildProjectRows(projectsState{tree: []api.ProjectNode{{
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
	m.projects.selectRow("n1:w2")
	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	m = res.(model)
	if m.projects.retarget == nil || cmd == nil {
		t.Fatal("T should open the target picker and load branches")
	}
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main", Local: true}, {Name: "dev", Local: true}}})
	m = res.(model)
	for _, r := range "dev" {
		res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	res, cmd = m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.projects.retarget != nil || cmd == nil {
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
	m.projects.selectRow("n1:p1")
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	if mm := res.(model); mm.projects.retarget != nil || mm.flash == "" {
		t.Error("T on a project row should only show a hint")
	}
}

func TestSpawnSessionInWorkspace(t *testing.T) {
	for _, focus := range []projectsFocus{focusTree, focusPane} {
		m := projectsTestModel()
		m.width, m.height = 120, 30
		m.client = &recordingClient{}
		m.projects.selectRow("n1:w2")
		m.projects.focus = focus
		res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: 's', Text: "s"})
		m = res.(model)
		if !m.spawn.active() || m.spawn.nodeID != "n1" || !m.spawn.fixedCwd || m.spawn.cwd.Value() != "/repo-feat" || cmd == nil {
			t.Errorf("focus %v: s should start a spawn fixed to n1:/repo-feat: %+v", focus, m.spawn)
		}
	}
}

func TestSpawnSessionNeedsLiveWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.tree[0].Workspaces[0].IsMain = false // a bare repo: no main worktree
	m.projects.rebuild()
	m.projects.selectRow("n1:p1")
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if mm := res.(model); mm.spawn.active() || mm.flash == "" {
		t.Error("s on a project with no main workspace should only show a hint")
	}

	m.projects.tree[0].Workspaces[1].IsGone = true
	m.projects.showGone = true
	m.projects.rebuild()
	m.projects.selectRow("n1:w2")
	res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if mm := res.(model); mm.spawn.active() || mm.flash == "" {
		t.Error("s on a gone workspace should only show a hint")
	}
}

func TestSessionFromProjectsRendersInPane(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w1")
	m.projects.focus = focusPane
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
	m.mode = modeList
	mm, _ := m.enterSession("n1:s1")
	if mm.bodyWidth() != mm.frameWidth()-mm.projectsLeftW()-dividerWidth || mm.bodyHeight() != 28 {
		t.Errorf("Home session body = %dx%d, want the pane", mm.bodyWidth(), mm.bodyHeight())
	}
}

func TestScreenFromPaneSessionUsesPaneSize(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.mode, m.sessionReturn, m.screenReturn = modeScreen, modeProjects, modeSession
	cols, _ := m.termDims()
	if want := m.bodyWidth() - 2; cols != want || m.bodyWidth() == 120 {
		t.Errorf("screen cols = %d, want pane-based %d", cols, want)
	}
	m.projects.sidebarHidden = true
	if cols, _ := m.termDims(); cols != m.frameWidth()-2 {
		t.Errorf("screen with the sidebar hidden: cols = %d, want full-width %d", cols, m.frameWidth()-2)
	}
}

func TestCtrlBInPaneSessionTogglesSidebar(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w1")
	mm, _ := m.enterSession("n1:s1")
	res, _ := mm.handleKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if got := res.(model); got.bodyWidth() != got.frameWidth() || got.mode != modeSession {
		t.Errorf("ctrl+b should hide the sidebar and keep the session: width=%d mode=%v", got.bodyWidth(), got.mode)
	}
}

func TestKillFromWorkspaceSessionsTab(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	rc := &recordingClient{}
	m.client = rc
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Repo: "repo", Tmux: session.TmuxLocation{PaneID: "%1"}}
	m.projects.selectRow("n1:w1")
	m.projects.focus = focusPane

	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = res.(model)
	if m.projects.pendingKill != "n1:s1" || !strings.Contains(ansi.Strip(m.projectsFooter()), "kill session repo · %1? y/n") {
		t.Fatalf("x should ask to kill n1:s1: pending=%q footer=%q", m.projects.pendingKill, m.projectsFooter())
	}
	res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if mm := res.(model); mm.projects.pendingKill != "" {
		t.Error("any key but y should cancel")
	}
	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if res.(model).projects.pendingKill != "" || cmd == nil {
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
	m.projects.selectRow("n1:w1") // sessions n1:s1, n1:s3
	m.projects.focus = focusPane
	m, _ = upd(m, keyMsg("j"))
	if m.projects.wsCursor != 1 {
		t.Fatalf("j should select the second card: cursor=%d", m.projects.wsCursor)
	}
	m, _ = upd(m, sessionsReplacedMsg([]session.Session{m.sessions["n1:s1"], m.sessions["n1:s2"]}))
	if m.projects.wsCursor != 0 {
		t.Errorf("the cursor should move to the remaining card: cursor=%d", m.projects.wsCursor)
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
	m.projects.selectRow("n1:w2")
	m, _ = upd(m, keyMsg("x"))
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "remove workspace repo-feat (feature)? y/n") {
		t.Errorf("remove prompt = %q", f)
	}
	m, _ = upd(m, keyMsg("n"))
	m, _ = upd(m, keyMsg("X"))
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "force-remove workspace repo-feat (feature)? uncommitted changes are lost · y/n") {
		t.Errorf("force-remove prompt = %q", f)
	}
}

func TestRemoveInFlightShowsAndBlocksRepeat(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	delete(m.sessions, "n1:s2")
	m.projects.selectRow("n1:w2")
	m, _ = upd(m, keyMsg("x"))
	m, cmd := upd(m, keyMsg("y"))
	if cmd == nil || !strings.Contains(ansi.Strip(m.View().Content), "removing…") {
		t.Fatalf("a confirmed remove should show on its row:\n%s", ansi.Strip(m.View().Content))
	}
	m, _ = upd(m, keyMsg("x"))
	if m.projects.pendingRemove != "" || !strings.Contains(m.flash, "already removing repo-feat") {
		t.Errorf("x during a remove: pending=%q flash=%q", m.projects.pendingRemove, m.flash)
	}
	m, _ = upd(m, cmd())
	if strings.Contains(ansi.Strip(m.View().Content), "removing…") {
		t.Error("the mark should clear when the remove finishes")
	}
}

func TestRemoveRefusesLiveSessions(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w2") // n1:s2 is live here
	for _, k := range []string{"x", "X"} {
		mm, _ := upd(m, keyMsg(k))
		if mm.projects.pendingRemove != "" || !strings.Contains(mm.flash, "repo-feat has 1 live session · kill it first") {
			t.Errorf("%s with a live session: pending=%q flash=%q", k, mm.projects.pendingRemove, mm.flash)
		}
	}
}

func TestMultiLineErrorFlashesOneLine(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m, _ = upd(m, projectsActionMsg{verb: "remove workspace", err: errString("first line\nsecond line")})
	if f := ansi.Strip(m.projectsFooter()); strings.Contains(f, "\n") || !strings.Contains(f, "remove workspace: first line …") {
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
	m.projects.selectRow("n1:w1") // n1:s1, n1:s3
	m.projects.focus = focusPane
	m, _ = upd(m, keyMsg("G"))
	if m.projects.wsCursor != 1 {
		t.Errorf("G should select the last card: cursor=%d", m.projects.wsCursor)
	}
	m, _ = upd(m, keyMsg("g"))
	if m.projects.wsCursor != 0 {
		t.Errorf("g should select the first card: cursor=%d", m.projects.wsCursor)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if m.projects.wsCursor != 1 {
		t.Errorf("^d should page down: cursor=%d", m.projects.wsCursor)
	}
	m, _ = upd(m, keyMsg("O"))
	if m.flash == "" {
		t.Error("O should try to jump to the session's pane")
	}
}

func TestManageKeysInPaneHintTheTree(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w2")
	m.projects.focus = focusPane
	for _, k := range []string{"n", "R", "H", "P", "T", "X"} {
		mm, _ := upd(m, keyMsg(k))
		if mm.flash != "manage keys work in the tree · esc to go there" || mm.projects.create.active || mm.projects.pendingRemove != "" {
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
		homeRowID: {has: []string{"enter open", "tab pane", "s spawn", "/ filter"}, lacks: []string{"fold", "n new", "x remove"}},
		"n1:p1":   {has: []string{"h/l fold", "s spawn", "n new"}, lacks: []string{"tab pane", "x remove"}},
		"n1:w2":   {has: []string{"h/l fold", "tab pane", "s spawn", "n new", "x remove"}},
	} {
		m.projects.selectRow(row)
		f := ansi.Strip(m.projectsFooter())
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

func TestProjectHintOffersUnfoldOnlyWhenFolded(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:p1")
	if strings.Contains(ansi.Strip(m.View().Content), "l unfold") {
		t.Error("an unfolded project should not offer l unfold")
	}
	m.projects.setFolded("n1:p1", true)
	m.projects.selectRow("n1:p1")
	if !strings.Contains(ansi.Strip(m.View().Content), "l unfold") {
		t.Error("a folded project should offer l unfold")
	}
}

func TestForgetProject(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:p1")
	mm, _ := upd(m, keyMsg("F"))
	if mm.projects.pendingForget != "" || !strings.Contains(mm.flash, "argus has 3 live sessions · kill them first") {
		t.Errorf("F with live sessions: pending=%q flash=%q", mm.projects.pendingForget, mm.flash)
	}
	m.sessions = map[string]session.Session{}
	m.order = nil
	rc := &recordingClient{}
	m.client = rc
	m, _ = upd(m, keyMsg("F"))
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "forget project argus? it leaves the list; its files stay · y/n") {
		t.Fatalf("forget prompt = %q", f)
	}
	if mm, _ := upd(m, keyMsg("n")); mm.projects.pendingForget != "" {
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
	m.projects.tree[0].Workspaces[1].TargetBranch = "dev"
	m.projects.rebuild()
	m.projects.selectRow("n1:w2")
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "repo-feat  feature → dev") {
		t.Errorf("the workspace header should show the target:\n%s", out)
	}
	m.projects.tree[0].Workspaces[0].TargetBranch = "main"
	m.projects.rebuild()
	m.projects.selectRow("n1:w1")
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "→ main") {
		t.Errorf("a workspace targeting its own branch should not show an arrow:\n%s", out)
	}
	m.projects.selectRow("n1:w2")
	m, _ = upd(m, keyMsg("T"))
	m, _ = upd(m, branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main"}, {Name: "dev"}, {Name: "feat"}}})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Target for repo-feat · now dev") || !strings.Contains(out, "dev  (current)") {
		t.Errorf("the target picker should name the workspace and mark the current target:\n%s", out)
	}
	if m.projects.retarget.pick.cursor != 1 {
		t.Errorf("the cursor should start on the current target: %d", m.projects.retarget.pick.cursor)
	}
}

func TestSpawnFromHomeAndProjectRows(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m.projects.cursor = 0 // Home
	if _, cmd := upd(m, keyMsg("s")); cmd == nil {
		t.Error("s on the Home row should start the spawn flow")
	}
	m.projects.selectRow("n1:p1")
	mm, _ := upd(m, keyMsg("s"))
	if !mm.spawn.active() || !mm.spawn.fixedCwd || mm.spawn.cwd.Value() != "/repo" {
		t.Errorf("s on a project row should spawn in its main workspace: active=%v cwd=%q", mm.spawn.active(), mm.spawn.cwd.Value())
	}
	for _, row := range []string{homeRowID, "n1:p1"} {
		m.projects.selectRow(row)
		if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "s spawn") {
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
	m.projects.tree = append(m.projects.tree, api.ProjectNode{
		ID: "n1:p2", Name: "zeta", Kind: "git", NodeID: "n1",
		Workspaces: []api.WorkspaceNode{{ID: "n1:w3", Dir: "/zeta", IsMain: true, Branch: "main"}},
	})
	m.projects.rebuild()
	delete(m.sessions, "n1:s2")
	m.projects.selectRow("n1:w2") // the last workspace of argus; zeta's rows follow
	m, _ = upd(m, keyMsg("x"))
	m, cmd := upd(m, keyMsg("y"))
	m, _ = upd(m, cmd())
	tree := []api.ProjectNode{m.projects.tree[0], m.projects.tree[1]}
	tree[0].Workspaces = tree[0].Workspaces[:1] // w2 is gone
	m, _ = upd(m, projectsTreeMsg{tree: tree})
	if got := m.projects.cursorRowID(); got != "n1:w1" {
		t.Errorf("after the remove the cursor should go to the workspace above: %q", got)
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
	if !m.projects.loading || len(rc.calls) == 0 || rc.calls[len(rc.calls)-1] != api.MethodProjectList {
		t.Errorf("an unknown workspace should refetch the tree: calls=%v", rc.calls)
	}
}

func TestProjectGitErrorShows(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.tree[0].Error = "worktree list: git missing"
	m.projects.rebuild()
	m.projects.selectRow("n1:p1")
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
	m.projects.selectRow("n1:w1")
	m.projects.focus = focusPane
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if mm := res.(model); mm.projects.pendingKill != "" || mm.flash == "" {
		t.Errorf("a session without terminal control should only show a hint: pending=%q flash=%q", mm.projects.pendingKill, mm.flash)
	}
}
