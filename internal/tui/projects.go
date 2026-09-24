package tui

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type projInputMode int

const (
	pmNone projInputMode = iota
	pmRename
	pmFilter
)

type projectsFocus int

const (
	focusTree projectsFocus = iota
	focusPane
)

type projRowKind int

const (
	rowNode projRowKind = iota
	rowProject
	rowWorkspace
)

// projectsRow is one flattened line of the tree, honoring the collapsed set.
type projectsRow struct {
	kind    projRowKind
	depth   int
	id      string // node id, composite project id, or composite workspace id
	label   string
	branch  string   // workspace only
	plain   bool     // workspace only: the directory of a plain project
	target  string   // workspace only: resolved target branch
	ws      []string // workspace ids under this row (itself for a workspace row)
	isGone  bool
	isMain  bool
	hidden  bool // project only
	pinned  bool // project only
	hasKids bool // node/project: whether it can collapse
}

type projectsState struct {
	tree          []api.ProjectNode
	err           error
	loading       bool
	rows          []projectsRow
	cursor        int             // index into rows (tree pane)
	want          string          // workspace id to reveal and select on the next tree load
	wsCursor      int             // index into the selected workspace's sessions (right pane)
	collapsed     map[string]bool // node/project id -> collapsed
	focus         projectsFocus
	leftW         int    // left sidebar width (0 = default); resizable
	sidebarHidden bool   // user toggled the left sidebar off
	showHidden    bool   // include hidden projects in the tree
	showGone      bool   // include projects and workspaces whose directory is gone
	filter        string // case-insensitive match on project, workspace, and branch names
	showHelp      bool

	// Management: a text input (new branch / rename / filter) and a remove confirmation.
	input              textinput.Model
	inputMode          projInputMode
	inputTarget        string // project id the input acts on
	pendingRemove      string // workspace id awaiting a remove confirmation
	pendingRemoveForce bool   // the pending remove is a force-remove

	create     createState
	createSeq  int         // last picker seq, so a hidden picker's late result can be told apart
	offerSpawn *spawnOffer // pending "start an agent with this issue?" answer
	retarget   *retargetState

	tab     wsTab
	dataWS  string // workspace id the pane was last synced to
	changes changesState
	files   filesState
}

// rebuild re-flattens the tree, keeping the cursor on the same row id.
func (p *projectsState) rebuild() {
	sel := p.cursorRowID()
	p.rows = buildProjectRows(*p)
	p.selectRow(sel)
}

func (p projectsState) cursorRowID() string {
	if p.cursor >= 0 && p.cursor < len(p.rows) {
		return p.rows[p.cursor].id
	}
	return ""
}

// selectRow moves the cursor to the row with id, or clamps it when that row is
// gone. It reports whether the row was found.
func (p *projectsState) selectRow(id string) bool {
	for i, r := range p.rows {
		if r.id == id {
			p.cursor = i
			return true
		}
	}
	p.cursor = min(p.cursor, cursorBottom(len(p.rows)))
	return false
}

// reveal unfolds the node and project holding workspace wsID and selects it.
func (p *projectsState) reveal(wsID string) {
	for _, pr := range p.tree {
		for _, w := range pr.Workspaces {
			if w.ID == wsID {
				delete(p.collapsed, pr.NodeID)
				delete(p.collapsed, pr.ID)
				p.rows = buildProjectRows(*p)
				p.selectRow(wsID)
				return
			}
		}
	}
}

// isFolded reports whether a row renders folded; a filter shows every match.
func (p *projectsState) isFolded(id string) bool {
	return p.filter == "" && p.collapsed[id]
}

func (p *projectsState) setFolded(id string, folded bool) {
	if folded {
		p.collapsed[id] = true
	} else {
		delete(p.collapsed, id)
	}
	p.rebuild()
}

// setFilter applies q and, if the selected row no longer matches, selects the
// first matching workspace.
func (p *projectsState) setFilter(q string) {
	sel := p.cursorRowID()
	p.filter = strings.TrimSpace(q)
	p.rows = buildProjectRows(*p)
	if p.selectRow(sel) {
		return
	}
	p.cursor = 0
	for i, r := range p.rows {
		if r.kind == rowWorkspace {
			p.cursor = i
			return
		}
	}
}

type spawnOffer struct{ nodeID, cwd, prompt string }

// retargetState is the T flow: a branch picker that sets a workspace's target.
type retargetState struct {
	workspaceID string
	projectID   string
	pick        branchPicker
}

type wsTab int

const (
	tabSessions wsTab = iota
	tabChanges
	tabFiles
)

// changesState is the Changes tab: a changed-file list plus an inline diff.
type changesState struct {
	against  string // "" = uncommitted; api.AgainstTarget = vs the target branch
	files    []api.ChangedFile
	cursor   int
	loading  bool
	err      error
	viewing  bool // showing the diff of the selected file
	diff     string
	diffPath string
	notShown bool
	scroll   int
}

// filesState is the Files tab: a directory listing plus a file viewer.
type filesState struct {
	dir      string // current repo-relative dir ("" = root)
	entries  []api.DirEntry
	cursor   int
	loading  bool
	err      error
	viewing  bool // showing a file's content
	content  string
	filePath string
	notShown bool
	scroll   int
}

func (m model) fetchProjects() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.ProjectListResult
		err := client.Call(api.MethodProjectList, nil, &res)
		return projectsTreeMsg{tree: res.Projects, err: err}
	}
}

// actListProjects opens the workspace sidebar from the session list, selecting
// the workspace of the session under the list cursor.
func (m model) actListProjects(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.loading, m.projects.err = true, nil
	if m.cursor < len(m.order) {
		if ws := m.sessions[m.order[m.cursor]].WorkspaceID; ws != "" {
			m.projects.want = ws
			m.projects.reveal(ws)
		}
	}
	return m, tea.Batch(m.fetchProjects(), m.maybeSpin())
}

// buildProjectRows flattens st.tree under st's fold, visibility, and filter
// settings.
func buildProjectRows(st projectsState) []projectsRow {
	q := strings.ToLower(st.filter)
	folded := func(id string) bool { return q == "" && st.collapsed[id] }
	var order []string
	byNode := map[string][]api.ProjectNode{}
	label := map[string]string{}
	for _, p := range st.tree {
		if (p.Hidden && !st.showHidden) || (p.IsGone && !st.showGone) {
			continue
		}
		p.Workspaces = visibleWorkspaces(p.Workspaces, st.showGone)
		if q != "" {
			var ok bool
			if p, ok = matchProject(p, q); !ok {
				continue
			}
		}
		if _, ok := byNode[p.NodeID]; !ok {
			order = append(order, p.NodeID)
		}
		byNode[p.NodeID] = append(byNode[p.NodeID], p)
		label[p.NodeID] = projNodeLabel(p)
	}
	sort.Slice(order, func(i, j int) bool {
		if label[order[i]] != label[order[j]] {
			return label[order[i]] < label[order[j]]
		}
		return order[i] < order[j]
	})
	// A single node adds a tree level with nothing to choose between.
	single := len(order) == 1
	var rows []projectsRow
	for _, nid := range order {
		depth := 0
		if !single {
			var ws []string
			for _, p := range byNode[nid] {
				ws = append(ws, workspaceIDs(p)...)
			}
			rows = append(rows, projectsRow{kind: rowNode, id: nid, label: label[nid], ws: ws, hasKids: true})
			if folded(nid) {
				continue
			}
			depth = 1
		}
		for _, p := range byNode[nid] {
			rows = append(rows, projectsRow{
				kind: rowProject, depth: depth, id: p.ID, label: p.Name, ws: workspaceIDs(p),
				isGone: p.IsGone, hidden: p.Hidden, pinned: p.Pinned, hasKids: len(p.Workspaces) > 0,
			})
			if folded(p.ID) {
				continue
			}
			for _, w := range p.Workspaces {
				rows = append(rows, projectsRow{
					kind: rowWorkspace, depth: depth + 1, id: w.ID, label: filepath.Base(w.Dir),
					branch: w.Branch, plain: p.Kind == "plain", target: w.TargetBranch, ws: []string{w.ID}, isGone: w.IsGone, isMain: w.IsMain,
				})
			}
		}
	}
	return rows
}

func visibleWorkspaces(ws []api.WorkspaceNode, showGone bool) []api.WorkspaceNode {
	if showGone {
		return ws
	}
	var out []api.WorkspaceNode
	for _, w := range ws {
		if !w.IsGone {
			out = append(out, w)
		}
	}
	return out
}

func matchProject(p api.ProjectNode, q string) (api.ProjectNode, bool) {
	if strings.Contains(strings.ToLower(p.Name), q) {
		return p, true
	}
	var ws []api.WorkspaceNode
	for _, w := range p.Workspaces {
		if strings.Contains(strings.ToLower(filepath.Base(w.Dir)), q) || strings.Contains(strings.ToLower(w.Branch), q) {
			ws = append(ws, w)
		}
	}
	p.Workspaces = ws
	return p, len(ws) > 0
}

func workspaceIDs(p api.ProjectNode) []string {
	ids := make([]string, len(p.Workspaces))
	for i, w := range p.Workspaces {
		ids[i] = w.ID
	}
	return ids
}

// sidebarMinWidth is the terminal width below which the left sidebar auto-
// collapses (compact mode), mirroring Crush's breakpoint behavior.
const sidebarMinWidth = 80

func (m model) projectsLeftW() int {
	w := m.projects.leftW
	if w == 0 {
		w = 34
	}
	return clampWidth(w, 20, m.width-30)
}

// sidebarVisible reports whether the left sidebar is shown: not toggled off by
// the user and the terminal is wide enough (else it auto-collapses).
func (m model) sidebarVisible() bool {
	return !m.projects.sidebarHidden && m.width >= sidebarMinWidth
}

func projNodeLabel(p api.ProjectNode) string {
	if p.NodeLabel != "" {
		return p.NodeLabel
	}
	if p.NodeID != "" {
		return p.NodeID
	}
	return "this machine"
}

func (m model) cursorRow() (projectsRow, bool) {
	if m.projects.cursor >= 0 && m.projects.cursor < len(m.projects.rows) {
		return m.projects.rows[m.projects.cursor], true
	}
	return projectsRow{}, false
}

// selectedWorkspaceID is the workspace id under the tree cursor, or "" when the
// cursor is on a node or project row.
func (m model) selectedWorkspaceID() string {
	if r, ok := m.cursorRow(); ok && r.kind == rowWorkspace {
		return r.id
	}
	return ""
}

// paneSessions are the live sessions in the selected workspace, id-sorted.
func (m model) paneSessions() []session.Session {
	wsID := m.selectedWorkspaceID()
	if wsID == "" {
		return nil
	}
	var out []session.Session
	for _, s := range m.sessions {
		if s.WorkspaceID == wsID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type wsActivity struct{ live, working, waiting int }

func (m model) workspaceActivity() map[string]wsActivity {
	out := map[string]wsActivity{}
	for _, s := range m.sessions {
		if s.WorkspaceID == "" || s.Offline || s.Status == session.StatusDead {
			continue
		}
		a := out[s.WorkspaceID]
		a.live++
		switch s.Status {
		case session.StatusWorking:
			a.working++
		case session.StatusAwaitingInput:
			a.waiting++
		}
		out[s.WorkspaceID] = a
	}
	return out
}

func (m model) activityBadge(act map[string]wsActivity, ws []string) string {
	var a wsActivity
	for _, id := range ws {
		b := act[id]
		a.live, a.working, a.waiting = a.live+b.live, a.working+b.working, a.waiting+b.waiting
	}
	n := strconv.Itoa(a.live)
	switch {
	case a.live == 0:
		return ""
	case a.waiting > 0:
		return lipgloss.NewStyle().Foreground(statusColor(session.StatusAwaitingInput)).Render(statusGlyph(session.StatusAwaitingInput) + " " + n)
	case a.working > 0:
		return lipgloss.NewStyle().Foreground(ColorOngoing).Render(SpinnerFrames[m.spin%len(SpinnerFrames)] + " " + n)
	}
	return StyleDim.Render(statusGlyph(session.StatusIdle) + " " + n)
}

func (m model) handleProjectsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.inputActive() {
		return m.handleProjectsInputKey(msg)
	}
	if m.projects.pendingRemove != "" {
		return m.handleRemoveConfirm(msg)
	}
	if o := m.projects.offerSpawn; o != nil {
		m.projects.offerSpawn = nil
		if msg.String() == "y" {
			return m, m.beginPresetSpawn(o.nodeID, o.cwd, o.prompt)
		}
		return m, nil
	}
	if m.projects.create.active {
		return m.handleCreateKey(msg)
	}
	if m.projects.retarget != nil {
		return m.handleRetargetKey(msg)
	}
	if m.projects.showHelp {
		m.projects.showHelp = false
		return m, nil
	}
	m.flash = ""
	switch {
	case key.Matches(msg, projectsKeys.Help):
		m.projects.showHelp = true
		return m, nil
	case key.Matches(msg, projectsKeys.Back):
		return m.projectsBack()
	case key.Matches(msg, projectsKeys.Filter):
		return m.startInput(pmFilter, "", m.projects.filter)
	case key.Matches(msg, projectsKeys.ShowHidden):
		m.projects.showHidden = !m.projects.showHidden
		m.projects.rebuild()
		return m.ensureTabData()
	case key.Matches(msg, projectsKeys.ShowGone):
		m.projects.showGone = !m.projects.showGone
		m.projects.rebuild()
		return m.ensureTabData()
	case key.Matches(msg, projectsKeys.Refresh):
		m.projects.loading, m.projects.err = true, nil
		return m, m.fetchProjects()
	case key.Matches(msg, projectsKeys.Widen):
		m.projects.leftW = clampWidth(m.projectsLeftW()+4, 20, m.width-30)
		return m, nil
	case key.Matches(msg, projectsKeys.Narrow):
		m.projects.leftW = clampWidth(m.projectsLeftW()-4, 20, m.width-30)
		return m, nil
	case key.Matches(msg, projectsKeys.ToggleSidebar):
		m.projects.sidebarHidden = !m.projects.sidebarHidden
		if !m.sidebarVisible() {
			m.projects.focus = focusPane
			return m.ensureTabData()
		}
		return m, nil
	case key.Matches(msg, projectsKeys.Focus):
		if !m.sidebarVisible() {
			return m, nil
		}
		if m.projects.focus == focusTree {
			return m.focusPane()
		}
		m.projects.focus = focusTree
		return m, nil
	}
	// A hidden/collapsed sidebar forces the main pane to own all input.
	if m.projects.focus == focusTree && m.sidebarVisible() {
		return m.handleProjectsTreeKey(msg)
	}
	return m.handleProjectsPaneKey(msg)
}

func (m model) focusPane() (tea.Model, tea.Cmd) {
	if m.selectedWorkspaceID() == "" {
		m.flash = "select a workspace to open its pane"
		return m, nil
	}
	m.projects.focus = focusPane
	return m.ensureTabData()
}

// projectsBack closes an open viewer, then steps out of the pane, then clears
// the filter, then leaves.
func (m model) projectsBack() (tea.Model, tea.Cmd) {
	if m.projects.focus == focusTree {
		if m.projects.filter != "" {
			m.projects.setFilter("")
			return m.ensureTabData()
		}
		m.mode = modeList
		return m, nil
	}
	switch {
	case m.projects.tab == tabChanges && m.projects.changes.viewing:
		m.projects.changes.viewing = false
	case m.projects.tab == tabFiles && m.projects.files.viewing:
		m.projects.files.viewing = false
	case m.projects.tab == tabFiles && m.projects.files.dir != "":
		return m.filesUp()
	case m.sidebarVisible():
		m.projects.focus = focusTree
	default:
		m.mode = modeList // no sidebar to return focus to
	}
	return m, nil
}

// moveTree moves the tree cursor and syncs the pane to the new selection.
func (m model) moveTree(i int) (tea.Model, tea.Cmd) {
	m.projects.cursor = min(i, cursorBottom(len(m.projects.rows)))
	return m.ensureTabData()
}

func (m model) handleProjectsTreeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := len(m.projects.rows)
	switch {
	case key.Matches(msg, projectsKeys.Up):
		return m.moveTree(cursorUp(m.projects.cursor))
	case key.Matches(msg, projectsKeys.Down):
		return m.moveTree(cursorDown(m.projects.cursor, n))
	case key.Matches(msg, projectsKeys.Top):
		return m.moveTree(0)
	case key.Matches(msg, projectsKeys.Bottom):
		return m.moveTree(cursorBottom(n))
	case key.Matches(msg, projectsKeys.HalfUp):
		return m.moveTree(max(0, m.projects.cursor-m.cardListPageStep()))
	case key.Matches(msg, projectsKeys.HalfDown):
		return m.moveTree(min(cursorBottom(n), m.projects.cursor+m.cardListPageStep()))
	case key.Matches(msg, projectsKeys.Left):
		return m.treeLeft()
	case key.Matches(msg, projectsKeys.Right):
		return m.treeRight()
	case key.Matches(msg, projectsKeys.Enter):
		return m.projectsEnter()
	case key.Matches(msg, projectsKeys.New):
		return m.actNewWorkspace()
	case key.Matches(msg, projectsKeys.Rename):
		return m.actRenameProject()
	case key.Matches(msg, projectsKeys.Hide):
		return m.actToggleHidden()
	case key.Matches(msg, projectsKeys.Pin):
		return m.actTogglePinned()
	case key.Matches(msg, projectsKeys.Remove):
		return m.actRemoveWorkspace(false)
	case key.Matches(msg, projectsKeys.ForceRemove):
		return m.actRemoveWorkspace(true)
	case key.Matches(msg, projectsKeys.Target):
		return m.actRetarget()
	}
	return m, nil
}

// treeRight unfolds a folded row, steps into an unfolded one, or focuses a
// workspace's pane.
func (m model) treeRight() (tea.Model, tea.Cmd) {
	r, ok := m.cursorRow()
	switch {
	case !ok || (r.kind != rowWorkspace && !r.hasKids):
		return m, nil
	case r.kind == rowWorkspace:
		return m.focusPane()
	case m.projects.isFolded(r.id):
		m.projects.setFolded(r.id, false)
		return m, nil
	}
	return m.moveTree(m.projects.cursor + 1)
}

// treeLeft folds an unfolded row, or steps to its parent row.
func (m model) treeLeft() (tea.Model, tea.Cmd) {
	r, ok := m.cursorRow()
	if !ok {
		return m, nil
	}
	if r.hasKids && m.projects.filter == "" && !m.projects.collapsed[r.id] {
		m.projects.setFolded(r.id, true)
		return m, nil
	}
	for i := m.projects.cursor - 1; i >= 0; i-- {
		if m.projects.rows[i].depth < r.depth {
			return m.moveTree(i)
		}
	}
	return m, nil
}

func (m model) projectsEnter() (tea.Model, tea.Cmd) {
	r, ok := m.cursorRow()
	switch {
	case !ok:
		return m, nil
	case r.kind == rowWorkspace:
		return m.focusPane()
	case r.hasKids && m.projects.filter == "":
		m.projects.setFolded(r.id, !m.projects.collapsed[r.id])
	}
	return m, nil
}

// --- view ---------------------------------------------------------------------

func (m model) projectsView() string {
	if m.spawn.active() {
		return m.spawnView()
	}
	title := Icon.Claude.Render() + " " + headerStyle.Render("argus") + dimStyle.Render("  ·  projects")
	if m.projects.loading && m.projects.tree != nil {
		title += dimStyle.Render("  ·  refreshing…")
	}
	footer := m.projectsFooter()

	if m.projects.err != nil {
		return pinFooter(title+"\n\n"+dimStyle.Render("error: "+m.projects.err.Error()), footer, m.width, m.height)
	}
	if m.projects.tree == nil {
		return pinFooter(title+"\n\n"+dimStyle.Render("loading projects…"), footer, m.width, m.height)
	}

	h := max(1, m.height-4)
	if m.projects.showHelp {
		return pinFooter(title+"\n\n"+composeH(m.width, h, flexPanel(m.projectsHelpView())), footer, m.width, m.height)
	}

	// Collapsed sidebar: the main pane owns the full width.
	if !m.sidebarVisible() {
		return pinFooter(title+"\n\n"+composeH(m.width, h, flexPanel(m.projectsMain(m.width, h))), footer, m.width, m.height)
	}

	// Columns: left sidebar (fixed, resizable) | center (flex). composeH also
	// takes a third panel for a right sidebar; it is unused until its content
	// is designed. centerW mirrors composeH's flex split so content wraps to fit.
	leftW := m.projectsLeftW()
	centerW := max(1, m.width-leftW-dividerWidth)
	body := composeH(m.width, h,
		fixedPanel(m.projectsTreePane(leftW, h), leftW),
		flexPanel(m.projectsMain(centerW, h)),
	)
	return pinFooter(title+"\n\n"+body, footer, m.width, m.height)
}

// projectsMain is the center column: the workspace tabs, or an overview when the
// cursor is on a node or project row.
func (m model) projectsMain(w, h int) string {
	if m.projects.create.active {
		return m.createView(w, h)
	}
	if rt := m.projects.retarget; rt != nil {
		return StylePrimaryBold.Render("Change target branch") + "\n\n" + rt.pick.view(w, max(1, h-2), false)
	}
	r, ok := m.cursorRow()
	if !ok {
		return dimStyle.Render("no projects")
	}
	if r.kind != rowWorkspace {
		return m.projectsSummary(r, w)
	}
	return truncateLine(m.wsTabHeader(r), w) + "\n\n" + m.projectsPaneContent(w, max(1, h-2))
}

func paneTitle(text string, focused bool) string {
	if focused {
		return StyleAccentBold.Render(text)
	}
	return StyleDim.Render(text)
}

func (m model) projectsTreePane(w, avail int) string {
	focused := m.projects.focus == focusTree
	title := "Projects"
	if m.projects.showHidden {
		title += "  +hidden"
	}
	if m.projects.showGone {
		title += "  +gone"
	}
	if m.projects.filter != "" {
		title += "  /" + m.projects.filter
	}
	head := truncateLine(paneTitle(title, focused), w) + "\n\n"
	if len(m.projects.rows) == 0 {
		if m.projects.filter != "" {
			return head + dimStyle.Render("no matches")
		}
		return head + dimStyle.Render("no projects")
	}
	act := m.workspaceActivity()
	lines := make([]string, len(m.projects.rows))
	for i, r := range m.projects.rows {
		lines[i] = m.projRowLine(r, i == m.projects.cursor, focused, act, w)
	}
	return head + strings.Join(windowSpan(lines, m.projects.cursor, m.projects.cursor+1, max(1, avail-2)), "\n")
}

// The bar dims without focus instead of hiding, so the selection stays visible
// after focus moves to another pane.
func cursorLine(text string, sel, focused bool) string {
	switch {
	case !sel:
		return "  " + text
	case focused:
		return lipgloss.NewStyle().Foreground(ColorFocus).Render("▌") + " " + cursorStyle.Render(text)
	}
	return StyleDim.Render("▌") + " " + text
}

func (m model) projRowLine(r projectsRow, sel, focused bool, act map[string]wsActivity, w int) string {
	indent := strings.Repeat("  ", r.depth)
	var text string
	switch r.kind {
	case rowNode:
		text = indent + collapseMark(m.projects.isFolded(r.id), true) + Icon.Node.Render() + " " + StyleSecondaryBold.Render(r.label)
	case rowProject:
		text = indent + collapseMark(m.projects.isFolded(r.id), r.hasKids) + projectLabel(r.label, r.hidden, r.pinned)
	case rowWorkspace:
		bullet := "• "
		if r.isMain {
			bullet = "★ "
		}
		text = indent + bullet + r.label
		switch {
		case r.isGone, r.plain:
		case r.branch != "":
			text += dimStyle.Render("  " + r.branch)
		default:
			text += dimStyle.Render("  (detached)")
		}
	}
	if r.isGone {
		text += dimStyle.Render(" (gone)")
	}
	var badge string
	if r.kind == rowWorkspace || m.projects.isFolded(r.id) {
		badge = m.activityBadge(act, r.ws)
	}
	return withBadge(cursorLine(text, sel, focused), badge, w)
}

func withBadge(line, badge string, w int) string {
	if badge == "" {
		return truncateLine(line, w)
	}
	bw := lipgloss.Width(badge)
	left := truncateLine(line, max(1, w-bw-1))
	return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-bw)) + badge
}

func projectLabel(name string, hidden, pinned bool) string {
	if hidden {
		name = StyleMuted.Render(name) + " " + Icon.Hidden.Render()
	}
	if pinned {
		name += " " + Icon.Pin.Render()
	}
	return name
}

func collapseMark(collapsed, hasKids bool) string {
	if !hasKids {
		return "  "
	}
	if collapsed {
		return "▸ "
	}
	return "▾ "
}

// projectsSummary is the center column for a node or project row: what the row
// holds, with the live session count per workspace.
func (m model) projectsSummary(r projectsRow, w int) string {
	act := m.workspaceActivity()
	var b strings.Builder
	if r.kind == rowNode {
		b.WriteString(StylePrimaryBold.Render(r.label) + "\n\n")
		for _, p := range m.projects.tree {
			if p.NodeID != r.id || (p.Hidden && !m.projects.showHidden) || (p.IsGone && !m.projects.showGone) {
				continue
			}
			p.Workspaces = visibleWorkspaces(p.Workspaces, m.projects.showGone)
			line := "  " + projectLabel(p.Name, p.Hidden, p.Pinned) + dimStyle.Render("  "+plural(len(p.Workspaces), "workspace"))
			b.WriteString(withBadge(line, m.activityBadge(act, workspaceIDs(p)), w) + "\n")
		}
		return b.String()
	}
	p, _ := m.findProject(r.id)
	dir := p.Root
	if dir == "" {
		dir = p.Dir
	}
	b.WriteString(truncateLine(StylePrimaryBold.Render(p.Name)+projectLabel("", p.Hidden, p.Pinned)+dimStyle.Render("  "+dir), w) + "\n\n")
	for _, ws := range visibleWorkspaces(p.Workspaces, m.projects.showGone) {
		row := projectsRow{
			kind: rowWorkspace, id: ws.ID, label: filepath.Base(ws.Dir), branch: ws.Branch, plain: p.Kind == "plain", target: ws.TargetBranch,
			ws: []string{ws.ID}, isGone: ws.IsGone, isMain: ws.IsMain,
		}
		b.WriteString(m.projRowLine(row, false, false, act, w) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(truncateLine("l unfold · n new workspace · R rename", w)))
	return b.String()
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func (m model) projectsSessionPane(w, avail int) string {
	ss := m.paneSessions()
	if len(ss) == 0 {
		return dimStyle.Render("no sessions in this workspace")
	}
	focused := m.projects.focus == focusPane
	cardW := min(w, maxCardWidth)
	cards := make([]string, len(ss))
	for i, s := range ss {
		cards[i] = m.sessionCard(s, focused && i == m.projects.wsCursor, cardW, true)
	}
	cursor := 0
	if focused {
		cursor = m.projects.wsCursor
	}
	return renderCardList(cards, cursor, avail)
}
