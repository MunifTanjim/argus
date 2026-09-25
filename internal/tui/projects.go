package tui

import (
	"fmt"
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
	focusFiles
)

type projRowKind int

const (
	rowNode projRowKind = iota
	rowProject
	rowWorkspace
	rowHome
)

const homeRowID = "home"

func homeRow() projectsRow { return projectsRow{kind: rowHome, id: homeRowID} }

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
	inputTarget        string          // project id the input acts on
	pendingRemove      string          // workspace id awaiting a remove confirmation
	pendingKill        string          // session id awaiting a kill confirmation
	pendingRemoveForce bool            // the pending remove is a force-remove
	removing           map[string]bool // workspaces with a remove in flight

	sideTab     sideTab
	ftree       fileTree
	changes     changesState
	filesHidden bool // user toggled the right sidebar off
	filesW      int  // right sidebar width (0 = default); resizable
	fileView    fileViewState

	create     createState
	createSeq  int         // last picker seq, so a hidden picker's late result can be told apart
	offerSpawn *spawnOffer // pending "start an agent with this issue?" answer
	retarget   *retargetState

	dataWS string // workspace id the pane was last synced to
}

// flatten is the tree's rows: Home first, then the projects.
func (p projectsState) flatten() []projectsRow {
	return append([]projectsRow{homeRow()}, buildProjectRows(p)...)
}

// rebuild re-flattens the tree, keeping the cursor on the same row id.
func (p *projectsState) rebuild() {
	sel := p.cursorRowID()
	p.rows = p.flatten()
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

func (p *projectsState) row(id string) (projectsRow, bool) {
	for _, r := range p.rows {
		if r.id == id {
			return r, true
		}
	}
	return projectsRow{}, false
}

// reveal unfolds the node and project holding workspace wsID and selects it,
// clearing a filter that hides it.
func (p *projectsState) reveal(wsID string) {
	for _, pr := range p.tree {
		for _, w := range pr.Workspaces {
			if w.ID == wsID {
				delete(p.collapsed, pr.NodeID)
				delete(p.collapsed, pr.ID)
				p.rows = p.flatten()
				if !p.selectRow(wsID) && p.filter != "" {
					p.filter = ""
					p.rows = p.flatten()
					p.selectRow(wsID)
				}
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
	p.rows = p.flatten()
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

// sideTab is the right sidebar's tab.
type sideTab int

const (
	sideFiles sideTab = iota
	sideChanges
)

var sideTabNames = []string{"Files", "Changes"}

// changesState is the Changes tab: one workspace's changed files and its commits
// since the target branch. A diff opens in the pane through fileView.
type changesState struct {
	ws      string
	against string // "" = uncommitted; api.AgainstTarget = vs the target branch
	gen     int    // bumped by reload, so a list requested before it is dropped
	files   []api.ChangedFile
	cursor  int // over the changed files, then the commits
	loading bool
	err     error

	commits        []api.Commit
	commitsLoading bool
	commitsErr     error

	commit       *api.Commit // the commit drilled into; nil shows the list
	commitFiles  []api.ChangedFile
	commitCursor int
	commitErr    error
}

func (m model) fetchProjects() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.ProjectListResult
		err := client.Call(api.MethodProjectList, nil, &res)
		return projectsTreeMsg{tree: res.Projects, err: err}
	}
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

func (m model) projectsLeftW() int { return clampWidth(m.rawLeftW(), 20, m.leftMaxW()) }

func (m model) rawLeftW() int {
	if m.projects.leftW == 0 {
		return 34
	}
	return m.projects.leftW
}

func (m model) rawFilesW() int {
	if m.projects.filesW == 0 {
		return filesDefaultW
	}
	return m.projects.filesW
}

// minPaneWidth is the room the center pane keeps however wide the sidebars get.
const minPaneWidth = 30

// leftMaxW and filesMaxW bound each sidebar so that, with the other one at its
// own width, the pane keeps minPaneWidth columns.
func (m model) leftMaxW() int {
	w := m.frameWidth() - dividerWidth - minPaneWidth
	if m.filesVisible() {
		w -= m.rawFilesW() + screenMargin + dividerWidth
	}
	return w
}

func (m model) filesMaxW() int {
	w := m.frameWidth() - screenMargin - dividerWidth - minPaneWidth
	if m.sidebarVisible() {
		w -= m.rawLeftW() + dividerWidth
	}
	return w
}

// sidebarVisible reports whether the left sidebar is shown: not toggled off by
// the user and the terminal is wide enough (else it auto-collapses).
func (m *model) toggleSidebar() {
	if m.width < sidebarMinWidth {
		m.flash = fmt.Sprintf("the tree needs %d columns", sidebarMinWidth)
		return
	}
	m.projects.sidebarHidden = !m.projects.sidebarHidden
}

func (m *model) toggleFiles() {
	if m.width < filesMinWidth {
		m.flash = fmt.Sprintf("the right sidebar needs %d columns", filesMinWidth)
		return
	}
	m.projects.filesHidden = !m.projects.filesHidden
}

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
	return m.badgeFor(a)
}

// homeBadge counts every live session, with or without a workspace.
func (m model) homeBadge() string {
	var a wsActivity
	for _, s := range m.sessions {
		if s.Offline || s.Status == session.StatusDead {
			continue
		}
		a.live++
		switch s.Status {
		case session.StatusWorking:
			a.working++
		case session.StatusAwaitingInput:
			a.waiting++
		}
	}
	return m.badgeFor(a)
}

func (m model) badgeFor(a wsActivity) string {
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
	if id := m.projects.pendingKill; id != "" {
		m.projects.pendingKill = ""
		if msg.String() == "y" {
			return m, m.killCmd(id)
		}
		return m, nil
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
	if m.projects.focus == focusFiles && m.filesVisible() {
		return m.handleFilesKey(msg)
	}
	if !m.sidebarVisible() && m.projects.focus == focusTree && !key.Matches(msg, projectsKeys.ToggleSidebar) {
		mm, _ := m.leaveHiddenTree()
		return mm.handleKey(msg)
	}
	switch {
	case key.Matches(msg, projectsKeys.Help):
		m.projects.showHelp = true
		return m, nil
	case m.treeFocused() && msg.String() == "q":
		return m.quit()
	case key.Matches(msg, projectsKeys.Back):
		return m.projectsBack()
	case key.Matches(msg, projectsKeys.Filter):
		if !m.sidebarVisible() {
			m.flash = "the filter needs the tree · ^b shows the tree"
			return m, nil
		}
		m.projects.focus = focusTree
		return m.startInput(pmFilter, "", m.projects.filter)
	case key.Matches(msg, projectsKeys.Spawn):
		return m.actSpawnSession()
	case key.Matches(msg, projectsKeys.ShowHidden):
		m.projects.showHidden = !m.projects.showHidden
		m.projects.rebuild()
		return m.syncPane()
	case key.Matches(msg, projectsKeys.ShowGone):
		m.projects.showGone = !m.projects.showGone
		m.projects.rebuild()
		return m.syncPane()
	case key.Matches(msg, projectsKeys.Refresh):
		m.projects.loading, m.projects.err = true, nil
		return m, m.fetchProjects()
	case key.Matches(msg, projectsKeys.Widen), key.Matches(msg, projectsKeys.Narrow):
		d := 4
		if key.Matches(msg, projectsKeys.Narrow) {
			d = -4
		}
		if m.projects.focus == focusFiles {
			m.projects.filesW = clampWidth(m.projectsFilesW()+d, 20, m.filesMaxW())
		} else {
			m.projects.leftW = clampWidth(m.projectsLeftW()+d, 20, m.leftMaxW())
		}
		return m, nil
	case key.Matches(msg, projectsKeys.ToggleSidebar):
		m.toggleSidebar()
		return m.leaveHiddenTree()
	case key.Matches(msg, projectsKeys.Focus):
		return m.cycleFocus(1)
	case key.Matches(msg, projectsKeys.FocusPrev):
		return m.cycleFocus(-1)
	case key.Matches(msg, projectsKeys.ToggleFiles):
		m.toggleFiles()
		return m, nil
	}
	// A hidden/collapsed sidebar forces the main pane to own all input.
	if m.projects.focus == focusTree && m.sidebarVisible() {
		return m.handleProjectsTreeKey(msg)
	}
	return m.handleProjectsPaneKey(msg)
}

// leaveHiddenTree moves focus off the tree once it no longer shows (ctrl+b, or a
// terminal narrower than sidebarMinWidth). With no tree on screen, the Home
// row's pane is the Home pane itself.
func (m model) leaveHiddenTree() (model, tea.Cmd) {
	if m.mode != modeProjects || m.projects.focus != focusTree || m.sidebarVisible() {
		return m, nil
	}
	if m.onHomeRow() {
		mm, cmd := m.enterHome()
		return mm.(model), cmd
	}
	m.projects.focus = focusPane
	mm, cmd := m.syncPane()
	return mm.(model), cmd
}

// enterHome moves focus into the Home pane (the homepage's session list).
func (m model) enterHome() (tea.Model, tea.Cmd) {
	m.projects.focus = focusPane
	m.mode = modeList
	return m, m.maybeSpin()
}

func (m model) onHomeRow() bool {
	r, ok := m.cursorRow()
	return ok && r.kind == rowHome
}

func (m model) focusPane() (tea.Model, tea.Cmd) {
	if m.onHomeRow() {
		return m.enterHome()
	}
	if m.selectedWorkspaceID() == "" {
		m.flash = "select a workspace to open its pane"
		return m, nil
	}
	m.projects.focus = focusPane
	return m.syncPane()
}

// treeFocused reports whether the tree has focus and is on screen; a hidden tree
// never holds focus in effect.
func (m model) treeFocused() bool {
	return m.projects.focus == focusTree && m.sidebarVisible()
}

// cycleFocus moves focus to the next visible, focusable panel: tree, pane,
// files. The Home row's pane is the Home pane itself.
func (m model) cycleFocus(d int) (tea.Model, tea.Cmd) {
	var order []projectsFocus
	if m.sidebarVisible() {
		order = append(order, focusTree)
	}
	order = append(order, focusPane)
	if m.filesVisible() && m.currentWorkspace() != "" {
		order = append(order, focusFiles)
	}
	i := 0
	for j, f := range order {
		if f == m.projects.focus {
			i = j
		}
	}
	next := order[(i+d+len(order))%len(order)]
	if next == focusPane {
		return m.focusPane()
	}
	m.projects.focus = next
	return m, nil
}

// handleFilesKey drives the file tree on the projects screen and from a
// session; esc returns focus to the pane.
func (m model) handleFilesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := projectsKeys
	switch {
	case key.Matches(msg, k.Back):
		if m.projects.sideTab == sideChanges && m.projects.changes.commit != nil {
			m.projects.changes.commit = nil
			return m, nil
		}
		m.projects.focus = focusPane
		return m, nil
	case key.Matches(msg, k.Focus), key.Matches(msg, k.FocusPrev):
		if m.mode != modeProjects {
			m.projects.focus = focusPane
			return m, nil
		}
		if key.Matches(msg, k.FocusPrev) {
			return m.cycleFocus(-1)
		}
		return m.cycleFocus(1)
	case key.Matches(msg, k.ToggleFiles):
		m.projects.filesHidden = true
		return m, nil
	case key.Matches(msg, k.ToggleSidebar):
		m.projects.sidebarHidden = !m.projects.sidebarHidden
		return m, nil
	case key.Matches(msg, k.Widen), key.Matches(msg, k.Narrow):
		d := 4
		if key.Matches(msg, k.Narrow) {
			d = -4
		}
		m.projects.filesW = clampWidth(m.projectsFilesW()+d, 20, m.filesMaxW())
		return m, nil
	case key.Matches(msg, k.SideTabNext), key.Matches(msg, k.SideTabPrev):
		d := 1
		if key.Matches(msg, k.SideTabPrev) {
			d = -1
		}
		m.projects.sideTab = sideTab((int(m.projects.sideTab) + d + len(sideTabNames)) % len(sideTabNames))
		return m, nil
	case key.Matches(msg, k.Help) && m.mode == modeProjects:
		m.projects.showHelp = true
		return m, nil
	}
	if m.projects.sideTab == sideChanges {
		return m.changesKey(msg)
	}
	if key.Matches(msg, k.Refresh) {
		return m.reloadFileTree()
	}
	return m.applyTreeRequest(m.projects.ftree.key(msg, m.cardListPageStep()))
}

// reloadFileTree drops every cached listing and fetches the root and each
// unfolded directory again, so the tree keeps its shape.
func (m model) reloadFileTree() (tea.Model, tea.Cmd) {
	ws := m.projects.ftree.ws
	expanded := m.projects.ftree.expanded
	m.projects.ftree = newFileTree(ws)
	m.projects.ftree.expanded = expanded
	dirs := []string{""}
	for d := range expanded {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	cmds := make([]tea.Cmd, len(dirs))
	for i, d := range dirs {
		m.projects.ftree.dirs[d] = &treeDir{loading: true}
		cmds[i] = m.fetchListDir(ws, d)
	}
	return m, tea.Batch(cmds...)
}

func (m model) applyTreeRequest(req treeRequest) (tea.Model, tea.Cmd) {
	switch {
	case req.loadDir != nil:
		return m, m.fetchListDir(m.projects.ftree.ws, *req.loadDir)
	case req.openFile != nil:
		return m.openFile(*req.openFile)
	}
	return m, nil
}

// projectsBack closes an open viewer, then steps out of the pane; on the tree it
// only clears a filter.
func (m model) projectsBack() (tea.Model, tea.Cmd) {
	if m.treeFocused() {
		if m.projects.filter != "" {
			m.projects.setFilter("")
			return m.syncPane()
		}
		return m, nil
	}
	switch {
	case m.projects.fileView.open():
		m.closeFileView()
	case m.sidebarVisible():
		m.projects.focus = focusTree
	default: // no tree to return to: go to the Home pane
		m.projects.cursor = 0
		return m.enterHome()
	}
	return m, nil
}

// moveTree moves the tree cursor and syncs the pane to the new selection.
func (m model) moveTree(i int) (tea.Model, tea.Cmd) {
	m.projects.cursor = min(i, cursorBottom(len(m.projects.rows)))
	return m.syncPane()
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
	if m.onHomeRow() {
		return m.enterHome()
	}
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
	if m.onHomeRow() {
		return m.enterHome()
	}
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
	// The Home row draws the framed Home layout, the same as when the Home pane
	// has focus, so the tabs and footer do not move when focus changes; only the
	// footer's keys differ.
	if m.onHomeRow() && m.sidebarVisible() && !m.projects.showHelp {
		mm := m
		mm.mode, mm.cursor = modeList, -1
		return mm.embedInProjects(mm.renderList(), m.projectsFooter())
	}
	title := m.frameTitle()
	footer := m.projectsFooter()

	h := max(1, m.height-4)
	if m.projects.showHelp {
		return m.helpScreen()
	}

	// Collapsed sidebar: the main pane owns the full width.
	if !m.sidebarVisible() {
		return pinFooter(title+"\n\n"+m.framedBody(m.projectsMain(m.bodyWidthFor(-dividerWidth), h), h), footer, m.width, m.height)
	}

	// centerW mirrors framedBody's flex split so content wraps to fit.
	leftW := m.projectsLeftW()
	centerW := m.bodyWidthFor(leftW)
	return pinFooter(title+"\n\n"+m.framedBody(m.projectsMain(centerW, h), h), footer, m.width, m.height)
}

// helpScreen draws the key help over the whole frame; any key closes it.
func (m model) helpScreen() string {
	help := indentBlock(m.projectsHelpView(), strings.Repeat(" ", screenMargin))
	footer := m.footer(helpAs(projectsKeys.Help, "any key", "close"))
	return pinFooter(m.frameTitle()+"\n\n"+composeH(m.width, max(1, m.height-4), flexPanel(help)), footer, m.width, m.height)
}

// projectsMain is the center column: the workspace's sessions, or an overview
// when the cursor is on a node or project row.
func (m model) projectsMain(w, h int) string {
	if m.onHomeRow() && !m.projects.create.active && m.projects.retarget == nil {
		return m.homePreview() // the Home list centers itself
	}
	cardW := min(w, maxCardWidth)
	// An open file or diff widens only its content, like Logs: the header stays
	// in the card column so it does not jump.
	if r, ok := m.cursorRow(); ok && r.kind == rowWorkspace && m.projects.fileView.open() &&
		!m.projects.create.active && m.projects.retarget == nil {
		wideW := min(w, maxContentWidth)
		head := centerBlock(truncateLine(m.wsHeader(r), cardW), cardW, w)
		return head + "\n\n" + centerBlock(m.fileViewBody(wideW, max(1, h-2)), wideW, w)
	}
	return centerBlock(m.projectsColumn(cardW, h), cardW, w)
}

func (m model) projectsColumn(w, h int) string {
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
	return truncateLine(m.wsHeader(r), w) + "\n\n" + m.projectsSessionPane(w, max(1, h-2))
}

// homePreview is the Sessions tab rendered at pane size for the tree-focused
// Home row; no card is selected, since the list does not have focus.
func (m model) homePreview() string {
	mm := m
	mm.mode, mm.cursor = modeList, -1
	return mm.listView()
}

func paneTitle(text string, focused bool) string {
	if focused {
		return StyleAccentBold.Render(text)
	}
	return StyleDim.Render(text)
}

func (m model) projectsTreePane(w, avail int) string {
	// A picker or the spawn flow in the pane takes the keys while focus stays here.
	focused := m.projects.focus == focusTree && !m.projects.create.active && m.projects.retarget == nil && !m.spawn.active()
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
	head := paneTitle(title, focused)
	if m.projects.loading && m.projects.tree != nil {
		head += dimStyle.Render("  refreshing…")
	}
	margin := strings.Repeat(" ", screenMargin)
	head = margin + truncateLine(head, w) + "\n\n"
	act := m.workspaceActivity()
	lines := make([]string, len(m.projects.rows))
	for i, r := range m.projects.rows {
		lines[i] = m.treeMarker(i == m.projects.cursor, focused) + m.projRowLine(r, i == m.projects.cursor, focused, act, w)
	}
	switch {
	case m.projects.err != nil:
		lines = append(lines, margin+truncateLine(dimStyle.Render("error: "+m.projects.err.Error()), w))
	case m.projects.tree == nil:
		lines = append(lines, margin+dimStyle.Render("loading projects…"))
	case len(m.projects.rows) == 1 && m.projects.filter != "":
		lines = append(lines, margin+dimStyle.Render("no matches"))
	case len(m.projects.rows) == 1:
		lines = append(lines, margin+dimStyle.Render("no projects"))
	}
	return head + strings.Join(windowSpan(lines, m.projects.cursor, m.projects.cursor+1, max(1, avail-2)), "\n")
}

// treeMarker fills the screen margin so row text lines up with the Projects
// title.
func (m model) treeMarker(sel, focused bool) string {
	switch {
	case !sel:
		return strings.Repeat(" ", screenMargin)
	case focused:
		return lipgloss.NewStyle().Foreground(ColorFocus).Render("▌") + " "
	}
	return StyleDim.Render("▌") + " "
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
	case rowHome:
		text = Icon.Home.Render() + " " + StyleSecondaryBold.Render("Home")
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
		if m.projects.removing[r.id] {
			text += dimStyle.Render("  removing…")
		}
	}
	if r.isGone {
		text += dimStyle.Render(" (gone)")
	}
	var badge string
	switch {
	case r.kind == rowHome:
		badge = m.homeBadge()
	case r.kind == rowWorkspace || m.projects.isFolded(r.id):
		badge = m.activityBadge(act, r.ws)
	}
	if sel && focused {
		text = cursorStyle.Render(text)
	}
	return withBadge(text, badge, w)
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
	hint := "n new workspace · R rename"
	if m.projects.isFolded(p.ID) {
		hint = "l unfold · " + hint
	}
	b.WriteString("\n" + dimStyle.Render(truncateLine(hint, w)))
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

// embedded reports whether the current view renders inside the projects frame
// (the projects screen is the only root; every other mode is its pane). The
// single-transcript viewer, and the welcome splash of a focused Home with no
// sessions, use the whole terminal.
func (m model) embedded() bool {
	if m.viewer || m.homeSplash() {
		return false
	}
	return m.mode != modeProjects || m.spawn.active()
}

// homeSplash reports whether the focused Home pane shows the welcome splash.
// The tree-focused Home row previews the Home pane as modeList too, but frames
// the splash in the pane.
func (m model) homeSplash() bool {
	return m.mode == modeList && m.projects.focus != focusTree && len(m.order) == 0 && !m.spawn.active()
}

// bodyWidth is the width the framed views lay out in: the pane between the
// sidebars that show, else the terminal. Input handling and rendering both read
// it, so scroll math and drawing agree.
func (m model) bodyWidth() int {
	if m.viewer || m.homeSplash() {
		return m.width
	}
	if !m.embedded() {
		return m.frameWidth() // the projects screen itself; its panes use bodyWidthFor
	}
	w := m.frameWidth()
	switch {
	case m.sidebarVisible():
		w -= m.projectsLeftW() + dividerWidth
	case m.fullBleed():
		w = m.width
	}
	if m.filesVisible() {
		w -= m.projectsFilesW() + screenMargin + dividerWidth
	}
	return max(1, w)
}

// bodyWidthFor is the projects screen's pane width next to a left panel of
// width leftW (-dividerWidth for none), minus the right sidebar when it shows.
func (m model) bodyWidthFor(leftW int) int {
	w := m.frameWidth() - leftW - dividerWidth
	if m.filesVisible() {
		w -= m.projectsFilesW() + screenMargin + dividerWidth
	}
	return max(1, w)
}

const (
	filesMinWidth = 120
	filesDefaultW = 34
)

func (m model) filesVisible() bool {
	return !m.projects.filesHidden && m.width >= filesMinWidth && !m.viewer && !m.homeSplash()
}

func (m model) projectsFilesW() int { return clampWidth(m.rawFilesW(), 20, m.filesMaxW()) }

// currentWorkspace is the workspace the file tree shows: the tree cursor's on
// the projects screen, or the open session's; "" elsewhere.
func (m model) currentWorkspace() string {
	switch m.mode {
	case modeProjects:
		return m.selectedWorkspaceID()
	case modeSession, modeScreen:
		return m.sessions[m.selectedID].WorkspaceID
	}
	return ""
}

// fileViewState is a file or, with diff set, a changed file's diff, shown in
// place of the pane content.
type fileViewState struct {
	ws, path          string
	lines             []string // highlighted once on load; rendering only windows them
	diff              bool
	against           string // diff only: the Changes mode it was opened in
	rev               string // diff only: the commit sha, or "" for the working tree
	notShown, loading bool
	err               error
	scroll            int
}

func (f fileViewState) open() bool { return f.path != "" }

func viewLines(s string) []string {
	if s = strings.TrimSuffix(s, "\n"); s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (m model) openFile(p string) (tea.Model, tea.Cmd) {
	ws := m.projects.ftree.ws
	m.projects.fileView = fileViewState{ws: ws, path: p, loading: true}
	m.projects.focus = focusPane
	return m, m.fetchReadFile(ws, p)
}

// closeFileView closes the open file or diff and returns focus to the sidebar
// it was opened from, when the sidebar still shows.
func (m *model) closeFileView() {
	m.projects.fileView = fileViewState{}
	if m.filesVisible() && m.currentWorkspace() != "" {
		m.projects.focus = focusFiles
	}
}

func (m model) fileViewBody(w, h int) string {
	f := m.projects.fileView
	switch {
	case f.err != nil:
		return dimStyle.Render("error: " + f.err.Error())
	case f.loading:
		return dimStyle.Render("loading " + f.path + "…")
	case f.notShown:
		return dimStyle.Render(f.path + ": binary or too large")
	case f.diff && len(f.lines) == 0:
		return dimStyle.Render(f.path + ": no changes")
	}
	title := f.path
	if f.rev != "" {
		title = f.rev[:min(len(f.rev), 7)] + " · " + f.path
	}
	return dimStyle.Render(title) + "\n" + scrollView(f.lines, f.scroll, max(1, h-1), w)
}

// handleFileViewKey scrolls or closes the open file and swallows keys meant for
// the content behind it; handled is false when no file is open or the key
// moves focus or toggles a panel.
func (m model) handleFileViewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	f := &m.projects.fileView
	if !f.open() {
		return m, nil, false
	}
	k := projectsKeys
	switch {
	case key.Matches(msg, k.Back):
		m.closeFileView()
	case key.Matches(msg, k.Up):
		f.scroll = max(0, f.scroll-1)
	case key.Matches(msg, k.Down):
		f.scroll = min(f.scroll+1, cursorBottom(len(f.lines)))
	case key.Matches(msg, k.HalfUp):
		f.scroll = max(0, f.scroll-m.cardListPageStep())
	case key.Matches(msg, k.HalfDown):
		f.scroll = min(f.scroll+m.cardListPageStep(), cursorBottom(len(f.lines)))
	case key.Matches(msg, k.Focus), key.Matches(msg, k.FocusPrev), key.Matches(msg, k.Help),
		key.Matches(msg, k.ToggleSidebar), key.Matches(msg, k.ToggleFiles),
		key.Matches(msg, sessionKeys.Files), key.Matches(msg, sessionKeys.Focus):
		return m, nil, false
	}
	// Any other key would act on the content hidden behind the file.
	return m, nil, true
}

// fullBleed reports whether the pane spans the whole terminal width with no
// margin: the Logs tab, whose long lines read best edge to edge.
func (m model) fullBleed() bool { return m.mode == modeLogs && !m.sidebarVisible() }

// screenMargin is the left margin of every projects-screen state, so content
// never touches the terminal edge and nothing shifts when the sidebar toggles.
const screenMargin = 2

// frameWidth is the terminal width inside the screen margin.
func (m model) frameWidth() int { return max(1, m.width-screenMargin) }

// bodyHeight is the height the framed views lay out in: below the frame header
// when embedded, else the terminal.
func (m model) bodyHeight() int {
	if m.embedded() {
		return max(1, m.height-2)
	}
	return m.height
}

// frameTitle is the status bar shared by every projects-screen state: the argus
// mark on the left, and on the right the global state worth seeing from any
// pane (sessions waiting, a dropped connection, quarantine).
func (m model) frameTitle() string {
	left := strings.Repeat(" ", screenMargin) + Icon.Claude.Render() + " " + headerStyle.Render("argus")
	var parts []string
	if n := m.waitingCount(); n > 0 {
		attn := lipgloss.NewStyle().Foreground(statusColor(session.StatusAwaitingInput))
		parts = append(parts, attn.Render(statusGlyph(session.StatusAwaitingInput)+" "+strconv.Itoa(n)+" need you"))
	}
	if m.reconnecting {
		parts = append(parts, dimStyle.Render("reconnecting…"))
	}
	if m.quarantined() {
		parts = append(parts, StyleErrorBold.Render("⚠ QUARANTINED")+dimStyle.Render(" · argus lock pin"))
	}
	if len(parts) == 0 {
		return left
	}
	right := strings.Join(parts, dimStyle.Render(" · "))
	gap := max(1, m.width-screenMargin-lipgloss.Width(left)-lipgloss.Width(right))
	return truncateLine(left+strings.Repeat(" ", gap)+right, m.width)
}

// waitingCount is the number of reachable sessions waiting for the user.
func (m model) waitingCount() int {
	n := 0
	for _, s := range m.sessions {
		if s.Status == session.StatusAwaitingInput && !s.Offline {
			n++
		}
	}
	return n
}

// embedInProjects frames a view as the projects pane, under the frame header
// and next to the sidebar. The view keeps its own header inside the pane.
// embedInProjects frames a view: the status bar, the sidebars around the pane,
// and the footer across the whole frame below them.
func (m model) embedInProjects(content, footer string) string {
	body := m.frameTitle() + "\n\n" + m.framedBody(content, max(1, m.bodyHeight()-footerRows))
	return pinFooter(body, footer, m.width, m.height)
}

// footerRows is the height a pinned footer takes: a blank row and the footer.
const footerRows = 2

// pin places a view's footer: below the whole frame when the view is framed
// (embedInProjects draws it), else at the bottom of the terminal.
func (m model) pin(body, footer string) string {
	if m.embedded() {
		return body
	}
	return pinFooter(body, footer, m.bodyWidth(), m.bodyHeight())
}

// currentFooter is the footer of the view the frame embeds.
func (m model) currentFooter() string {
	if m.spawn.active() {
		return m.spawnFooter()
	}
	switch m.mode {
	case modeSession:
		return m.sessionFooter()
	case modeScreen:
		return m.screenFooter()
	case modeHistoryProjects:
		return m.historyProjectsFooter()
	case modeHistorySessions:
		return m.historySessionsFooter()
	case modeHistoryTranscript:
		return m.historyTranscriptFooter()
	case modeLogs:
		return m.logsFooter()
	case modeProjects:
		return m.projectsFooter()
	}
	return m.listFooter()
}

// framedBody lays out the left sidebar (which owns the left margin), the pane,
// and the right sidebar (which owns the right margin), each when it shows.
func (m model) framedBody(pane string, h int) string {
	var panels []hpanel
	switch {
	case m.fullBleed():
		panels = append(panels, flexPanel(pane))
	case !m.sidebarVisible():
		panels = append(panels, flexPanel(indentBlock(pane, strings.Repeat(" ", screenMargin))))
	default:
		leftW := m.projectsLeftW()
		panels = append(panels, fixedPanel(m.projectsTreePane(leftW, h), leftW+screenMargin), flexPanel(pane))
	}
	if m.filesVisible() {
		fw := m.projectsFilesW()
		panels = append(panels, fixedPanel(m.filesPane(fw, h), fw+screenMargin))
	}
	return composeH(m.width, h, panels...)
}

// filesPane is the right sidebar: the tab strip, then the current workspace's
// tree or changes, or a hint.
func (m model) filesPane(w, h int) string {
	gutter := strings.Repeat(" ", screenMargin)
	focused := m.projects.focus == focusFiles
	head := gutter + truncateLine(m.sideTabStrip(focused), max(1, w-screenMargin)) + "\n\n"
	switch {
	case m.currentWorkspace() == "":
		return head + gutter + dimStyle.Render("select a workspace")
	case m.projects.sideTab == sideChanges:
		return head + m.changesView(w, max(1, h-2), focused)
	}
	return head + m.projects.ftree.view(w, max(1, h-2), focused)
}

func (m model) sideTabStrip(focused bool) string {
	parts := make([]string, len(sideTabNames))
	for i, n := range sideTabNames {
		parts[i] = StyleDim.Render(n)
		if sideTab(i) == m.projects.sideTab {
			parts[i] = paneTitle(n, focused)
			if !focused {
				parts[i] = StylePrimaryBold.Render(n)
			}
		}
	}
	return strings.Join(parts, StyleDim.Render("  "))
}

// homeBrand prefixes a pane header with the argus mark, unless the frame header
// above it already shows one.
func (m model) homeBrand() string {
	if m.embedded() {
		return ""
	}
	return Icon.Claude.Render() + " " + headerStyle.Render("argus") + "    "
}

// withBrand is "argus · rest" full screen, or just rest under the frame header.
func (m model) withBrand(rest string) string {
	if m.embedded() {
		return rest
	}
	return "argus · " + rest
}
