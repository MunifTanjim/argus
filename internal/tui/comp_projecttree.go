package tui

import (
	"maps"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type leftSidebarState struct {
	tree   projectTreeComp
	hidden bool // the user toggled it off
	width  int  // 0 = default
}

func (l leftSidebarState) rawWidth() int {
	if l.width == 0 {
		return 34
	}
	return l.width
}

func (l leftSidebarState) handleKey(c *ctx, msg tea.KeyPressMsg) (leftSidebarState, bool) {
	switch {
	case c.m.matches(msg, projectsKeys.Widen):
		return l.resize(c, 4), true
	case c.m.matches(msg, projectsKeys.Narrow):
		return l.resize(c, -4), true
	}
	return l, false
}

func (l leftSidebarState) resize(c *ctx, d int) leftSidebarState {
	l.width = clampWidth(c.m.projectsLeftW()+d, 20, c.m.leftMaxW())
	return l
}

type projectTreeComp struct {
	data       []api.ProjectNode
	err        error
	loading    bool
	fetchSeq   int  // seq of the newest fetch
	refetch    bool // a change arrived during a fetch; fetch again after it
	loaded     bool // a project list arrived without an error
	rows       []projectsRow
	cursor     int
	want       string          // workspace id to reveal and select on the next load
	collapsed  map[string]bool // node/project id -> collapsed
	showHidden bool
	showGone   bool
	filter     string // case-insensitive match on project, workspace, and branch names

	input              textinput.Model
	inputMode          projInputMode
	inputTarget        string // project id the input acts on
	pendingRemove      string // workspace id awaiting a remove confirmation
	pendingRemoveForce bool
	pendingForget      string          // project id awaiting a forget confirmation
	removing           map[string]bool // workspaces with a remove in flight
	offerSpawn         *spawnOffer     // pending "start an agent with this issue?" answer

	createSeq int // last create picker's seq, so a closed picker's late result can be told apart

	asked map[string]bool // workspace ids a session named that the tree lacked
}

type spawnOffer struct{ nodeID, cwd, prompt string }

func newProjectTree() projectTreeComp {
	return projectTreeComp{collapsed: make(map[string]bool), loading: true, rows: []projectsRow{homeRow()}}
}

func (t projectTreeComp) section() string { return "project-tree" }

func (t projectTreeComp) raw(*ctx) bool {
	return t.inputMode != pmNone || t.pendingRemove != "" || t.pendingForget != "" || t.offerSpawn != nil
}

func (t projectTreeComp) fullScreen(*ctx) fullLevel { return notFull }
func (t projectTreeComp) close(*ctx) tea.Cmd        { return nil }
func (t projectTreeComp) offers(*ctx) []binding     { return sectionOffers[t.section()].keys }
func (t projectTreeComp) commands(*ctx) []binding   { return sectionLists[t.section()].own }
func (t projectTreeComp) pageStep(c *ctx) int       { return c.m.baseComp().pageStep(c) }
func (t projectTreeComp) layer() layer              { return baseLayer }

func (t projectTreeComp) spins(c *ctx) bool {
	if c.m.anyWorking() {
		return true
	}
	for _, p := range t.data {
		for _, w := range p.Workspaces {
			if w.Setup != nil && w.Setup.State == "running" {
				return true
			}
		}
	}
	return false
}

func (t projectTreeComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	switch {
	case t.inputMode != pmNone:
		var cmd tea.Cmd
		t, cmd = t.inputKey(c, msg)
		return t, cmd, true
	case t.pendingRemove != "":
		var cmd tea.Cmd
		t, cmd = t.removeConfirm(c, msg)
		return t, cmd, true
	case t.pendingForget != "":
		id := t.pendingForget
		t.pendingForget = ""
		if msg.String() == "y" {
			return t, c.m.forgetProjectCmd(id), true
		}
		return t, nil, true
	case t.offerSpawn != nil:
		o := t.offerSpawn
		t.offerSpawn = nil
		if msg.String() == "y" {
			return t, openPresetSpawn(c, o.nodeID, o.cwd, o.prompt), true
		}
		return t, nil, true
	}
	m, k, n := c.m, projectsKeys, len(t.rows)
	var cmd tea.Cmd
	switch {
	case m.matches(msg, k.Back):
		if t.filter != "" {
			t.setFilter("")
		}
	case m.matches(msg, k.Up):
		t = t.move(cursorUp(t.cursor))
	case m.matches(msg, k.Down):
		t = t.move(cursorDown(t.cursor, n))
	case m.matches(msg, k.Top):
		t = t.move(0)
	case m.matches(msg, k.Bottom):
		t = t.move(cursorBottom(n))
	case m.matches(msg, k.HalfUp):
		t = t.move(max(0, t.cursor-m.cardListPageStep()))
	case m.matches(msg, k.HalfDown):
		t = t.move(min(cursorBottom(n), t.cursor+m.cardListPageStep()))
	case m.matches(msg, k.Left):
		t = t.treeLeft()
	case m.matches(msg, k.Right):
		t = t.treeRight(c)
	case m.matches(msg, k.Enter):
		t = t.enter(c)
	case m.matches(msg, k.New):
		t, cmd = t.newWorkspace(c)
	case m.matches(msg, k.Rename):
		t, cmd = t.renameProject(c)
	case m.matches(msg, k.Forget):
		t = t.forgetProject(c)
	case m.matches(msg, k.Hide, k.Unhide):
		cmd = t.setHidden(c, m.matches(msg, k.Hide), m.matches(msg, k.Unhide))
	case m.matches(msg, k.Pin, k.Unpin):
		cmd = t.setPinned(c, m.matches(msg, k.Pin), m.matches(msg, k.Unpin))
	case m.matches(msg, k.Remove):
		t = t.removeWorkspace(c, false)
	case m.matches(msg, k.ForceRemove):
		t = t.removeWorkspace(c, true)
	case m.matches(msg, k.Target):
		cmd = t.retarget(c)
	case m.matches(msg, k.RunSetup):
		cmd = t.runSetup(c)
	case m.matches(msg, k.Filter):
		t, cmd = t.run(c, treeFilter)
	case m.matches(msg, k.Spawn):
		r, _ := t.cursorRow()
		cmd = spawnSession(c, r)
	case m.matches(msg, k.SetupLog):
		cmd = openSetupLog(c, t.selectedWorkspaceID())
	case m.matches(msg, k.ShowHidden):
		t, cmd = t.run(c, treeToggleHidden)
	case m.matches(msg, k.ShowGone):
		t, cmd = t.run(c, treeToggleGone)
	case m.matches(msg, k.Refresh):
		t, cmd = t.run(c, treeReload)
	default:
		return t, nil, false
	}
	return t, cmd, true
}

// treeOp is a change to the tree that its own keys and a workspace pane's keys
// make.
type treeOp int

const (
	treeFilter treeOp = iota
	treeToggleHidden
	treeToggleGone
	treeReload
)

func (t projectTreeComp) run(c *ctx, op treeOp) (projectTreeComp, tea.Cmd) {
	var cmd tea.Cmd
	switch op {
	case treeFilter:
		c.focusTree()
		cmd = t.startInput(pmFilter, "", t.filter)
	case treeToggleHidden:
		t.showHidden = !t.showHidden
		t.rebuild()
	case treeToggleGone:
		t.showGone = !t.showGone
		t.rebuild()
	case treeReload:
		t.err = nil
		cmd = t.load(c.m.client)
	}
	return t, cmd
}

func (t projectTreeComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case projectsTreeMsg:
		var follow tea.Cmd
		// Only the reply to the last fetch, with no refetch after it, can show
		// that a wanted row is not there.
		final := msg.seq == t.fetchSeq && !t.refetch
		if msg.seq == t.fetchSeq {
			t.loading = false
			if t.refetch {
				t.refetch = false
				follow = t.load(c.m.client)
			}
		}
		t.err = msg.err
		if msg.err == nil {
			t.data, t.loaded = msg.tree, true
			if t.data == nil {
				t.data = []api.ProjectNode{}
			}
		}
		t.rebuild()
		if t.want != "" {
			t.reveal(t.want)
			if t.cursorRowID() != t.want {
				t.selectRow(t.want) // a project or node row
			}
			if final || t.cursorRowID() == t.want {
				t.want = ""
			}
		}
		return t, follow
	case projectsActionMsg:
		delete(t.removing, msg.removed)
		if msg.err != nil {
			c.setFlash(msg.verb + ": " + msg.err.Error())
			return t, nil
		}
		flash := msg.ok
		if flash == "" {
			flash = msg.verb + " done"
		}
		c.setFlash(flash)
		// After a remove, move only a cursor that still sits on the removed row.
		if msg.removed == "" || t.cursorRowID() == msg.removed {
			t.want = msg.selectID
		}
		cmd := t.load(c.m.client)
		return t, cmd
	case createDoneMsg:
		if p, ok := c.m.frontCreate(); !ok || p.seq != msg.seq {
			// A closed picker's call finished: report it without touching a newer
			// picker, the cursor, or the keys the user pressed since.
			if msg.err != nil {
				c.setFlash("create workspace: " + msg.err.Error())
				return t, nil
			}
			c.setFlash(createdFlash(msg.res))
			cmd := t.load(c.m.client)
			return t, cmd
		}
		if msg.err != nil {
			return t, nil
		}
		t.want = msg.res.WorkspaceID
		c.setFlash(createdFlash(msg.res))
		if msg.source == api.SourceIssue && msg.res.Prompt != "" {
			nodeID, _, _ := session.SplitCompositeID(msg.res.WorkspaceID)
			t.offerSpawn = &spawnOffer{nodeID: nodeID, cwd: msg.res.Dir, prompt: msg.res.Prompt}
		}
		cmd := t.load(c.m.client)
		return t, cmd
	}
	return t, nil
}

func (t projectTreeComp) footerPrompt(c *ctx) string {
	m := c.m
	switch {
	case t.inputMode == pmRename:
		return asstStyle.Render("rename: " + t.input.View() + "  enter rename · esc cancel")
	case t.inputMode == pmFilter:
		return asstStyle.Render("filter: " + t.input.View() + "  enter keep · esc clear")
	case t.pendingRemove != "":
		return asstStyle.Render(m.removePrompt())
	case t.pendingForget != "":
		p, _ := m.findProject(t.pendingForget)
		return asstStyle.Render("forget project " + p.Name + "? it leaves the list; its files stay · y/n")
	case t.offerSpawn != nil:
		return asstStyle.Render("start an agent with this issue? y/n")
	}
	return ""
}

func (t projectTreeComp) footer(c *ctx) []binding {
	k := projectsKeys
	bindings := append(t.rowBindings(), clearFilterKey(k.Back, t.filter != ""))
	return append(bindings, listKeys.Quit, c.m.sideKey(k.ToggleFiles), k.Help)
}

func (t projectTreeComp) rowBindings() []binding {
	k := projectsKeys
	r, _ := t.cursorRow()
	switch r.kind {
	case rowHome:
		return []binding{k.Spawn, k.Filter}
	case rowWorkspace:
		return []binding{k.Left, k.Spawn, k.New, k.Remove, k.Filter}
	case rowProject:
		return []binding{k.Left, k.Spawn, k.New, k.Filter}
	}
	return []binding{k.Left, k.Filter}
}

func (t projectTreeComp) view(c *ctx, w, h int) string {
	m := c.m
	focused := m.focused == leftSidebar && len(m.popups) == 0
	title := "Projects"
	if t.showHidden {
		title += "  +hidden"
	}
	if t.showGone {
		title += "  +gone"
	}
	if t.filter != "" {
		title += "  /" + t.filter
	}
	head := paneTitle(title, focused)
	if t.loading && t.data != nil {
		head += dimStyle.Render("  refreshing…")
	}
	margin := strings.Repeat(" ", screenMargin)
	head = margin + truncateLine(head, w) + "\n\n"
	act := m.workspaceActivity()
	lines := make([]string, len(t.rows))
	for i, r := range t.rows {
		lines[i] = treeMarker(i == t.cursor, focused) + m.projRowLine(r, i == t.cursor, focused, act, w)
	}
	switch {
	case t.err != nil:
		lines = append(lines, margin+truncateLine(dimStyle.Render("error: "+t.err.Error()), w))
	case t.data == nil:
		lines = append(lines, margin+dimStyle.Render("loading projects…"))
	case len(t.rows) == 1 && t.filter != "":
		lines = append(lines, margin+dimStyle.Render("no matches"))
	case len(t.rows) == 1:
		lines = append(lines, margin+dimStyle.Render("no projects"))
	}
	return head + strings.Join(windowSpan(lines, t.cursor, t.cursor+1, max(1, h-2)), "\n")
}

func (t projectTreeComp) flatten() []projectsRow {
	return append([]projectsRow{homeRow()}, buildProjectRows(t)...)
}

func (t *projectTreeComp) rebuild() {
	sel := t.cursorRowID()
	t.rows = t.flatten()
	t.selectRow(sel)
}

func (t projectTreeComp) cursorRowID() string {
	if r, ok := t.cursorRow(); ok {
		return r.id
	}
	return ""
}

func (t projectTreeComp) cursorRow() (projectsRow, bool) {
	if t.cursor >= 0 && t.cursor < len(t.rows) {
		return t.rows[t.cursor], true
	}
	return projectsRow{}, false
}

func (t projectTreeComp) selectedWorkspaceID() string {
	if r, ok := t.cursorRow(); ok && r.kind == rowWorkspace {
		return r.id
	}
	return ""
}

func (t projectTreeComp) cursorProjectID() string {
	c := t.cursor
	if c < 0 || c >= len(t.rows) {
		return ""
	}
	switch t.rows[c].kind {
	case rowProject:
		return t.rows[c].id
	case rowWorkspace:
		for i := c; i >= 0; i-- {
			if t.rows[i].kind == rowProject {
				return t.rows[i].id
			}
		}
	}
	return ""
}

func (t *projectTreeComp) selectRow(id string) bool {
	for i, r := range t.rows {
		if r.id == id {
			t.cursor = i
			return true
		}
	}
	t.cursor = min(t.cursor, cursorBottom(len(t.rows)))
	return false
}

// removeNeighbor is the row to select once workspace id leaves the tree.
func (t projectTreeComp) removeNeighbor(id string) string {
	for i, r := range t.rows {
		if r.id != id {
			continue
		}
		if i > 0 && t.rows[i-1].kind == rowWorkspace {
			return t.rows[i-1].id
		}
		if i+1 < len(t.rows) && t.rows[i+1].kind == rowWorkspace && t.rows[i+1].depth == r.depth {
			return t.rows[i+1].id
		}
		for j := i - 1; j >= 0; j-- {
			if t.rows[j].depth < r.depth {
				return t.rows[j].id
			}
		}
	}
	return ""
}

func (t projectTreeComp) row(id string) (projectsRow, bool) {
	for _, r := range t.rows {
		if r.id == id {
			return r, true
		}
	}
	return projectsRow{}, false
}

func (t *projectTreeComp) reveal(wsID string) {
	for _, pr := range t.data {
		for _, w := range pr.Workspaces {
			if w.ID == wsID {
				delete(t.collapsed, pr.NodeID)
				delete(t.collapsed, pr.ID)
				t.rows = t.flatten()
				if !t.selectRow(wsID) && t.filter != "" {
					t.filter = ""
					t.rows = t.flatten()
					t.selectRow(wsID)
				}
				return
			}
		}
	}
}

// follow unfolds the rows that hold id only if that makes id visible. The
// cursor stays when the filter or the hidden setting hides the row.
func (t *projectTreeComp) follow(id string) {
	for _, p := range t.data {
		holds := p.ID == id || slices.ContainsFunc(p.Workspaces, func(w api.WorkspaceNode) bool { return w.ID == id })
		if !holds {
			continue
		}
		cand := maps.Clone(t.collapsed)
		delete(cand, p.NodeID)
		if p.ID != id {
			delete(cand, p.ID)
		}
		probe := *t
		probe.collapsed = cand
		probe.rebuild()
		if slices.ContainsFunc(probe.rows, func(r projectsRow) bool { return r.id == id }) {
			t.collapsed = cand
			t.rebuild()
		}
		break
	}
	for i, r := range t.rows {
		if r.id == id {
			t.cursor = i
			return
		}
	}
}

// A filter shows every match.
func (t projectTreeComp) isFolded(id string) bool {
	return t.filter == "" && t.collapsed[id]
}

func (t *projectTreeComp) setFolded(id string, folded bool) {
	if folded {
		t.collapsed[id] = true
	} else {
		delete(t.collapsed, id)
	}
	t.rebuild()
}

func (t *projectTreeComp) setFilter(q string) {
	sel := t.cursorRowID()
	t.filter = strings.TrimSpace(q)
	t.rows = t.flatten()
	if t.selectRow(sel) {
		return
	}
	t.cursor = 0
	for i, r := range t.rows {
		if r.kind == rowWorkspace {
			t.cursor = i
			return
		}
	}
}

func (t *projectTreeComp) dropFilter(id string) {
	if t.filter == "" {
		return
	}
	t.filter = ""
	t.reveal(id)
}

func (t projectTreeComp) findProject(id string) (api.ProjectNode, bool) {
	for _, p := range t.data {
		if p.ID == id {
			return p, true
		}
	}
	return api.ProjectNode{}, false
}

func (t projectTreeComp) findWorkspace(id string) (api.WorkspaceNode, bool) {
	for _, p := range t.data {
		for _, w := range p.Workspaces {
			if w.ID == id {
				return w, true
			}
		}
	}
	return api.WorkspaceNode{}, false
}

func (t projectTreeComp) projectOfWorkspace(wsID string) string {
	for _, p := range t.data {
		for _, w := range p.Workspaces {
			if w.ID == wsID {
				return p.ID
			}
		}
	}
	return ""
}

func (t projectTreeComp) fetch(client Client) tea.Cmd {
	seq := t.fetchSeq
	return func() tea.Msg {
		var res api.ProjectListResult
		err := client.Call(api.MethodProjectList, nil, &res)
		return projectsTreeMsg{tree: res.Projects, seq: seq, err: err}
	}
}

// load fetches once more after a running fetch replies, so a change made during
// it is not lost.
func (t *projectTreeComp) load(client Client) tea.Cmd {
	if t.loading {
		t.refetch = true
		return nil
	}
	t.fetchSeq++
	t.loading = true
	return t.fetch(client)
}

// loadMissing asks once per id: the node announces a workspace it registers
// later.
func (t *projectTreeComp) loadMissing(client Client, ws string) tea.Cmd {
	if _, ok := t.findWorkspace(ws); ok || t.asked[ws] {
		return nil
	}
	if t.asked == nil {
		t.asked = map[string]bool{}
	}
	t.asked[ws] = true
	return t.load(client)
}

func (t projectTreeComp) move(i int) projectTreeComp {
	t.cursor = min(i, cursorBottom(len(t.rows)))
	return t
}

func (t projectTreeComp) treeLeft() projectTreeComp {
	r, ok := t.cursorRow()
	if !ok {
		return t
	}
	if r.hasKids && t.filter == "" && !t.collapsed[r.id] {
		t.setFolded(r.id, true)
		return t
	}
	for i := t.cursor - 1; i >= 0; i-- {
		if t.rows[i].depth < r.depth {
			return t.move(i)
		}
	}
	return t
}

func (t projectTreeComp) treeRight(c *ctx) projectTreeComp {
	r, ok := t.cursorRow()
	switch {
	case !ok:
	case r.kind == rowHome || r.kind == rowWorkspace:
		c.openRow(r.id)
	case !r.hasKids:
	case t.isFolded(r.id):
		t.setFolded(r.id, false)
	default:
		return t.move(t.cursor + 1)
	}
	return t
}

func (t projectTreeComp) enter(c *ctx) projectTreeComp {
	if id := t.cursorRowID(); id != "" {
		c.openRow(id)
	}
	return t
}

func newProjectsInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetWidth(40)
	return ti
}

func (t *projectTreeComp) startInput(mode projInputMode, target, initial string) tea.Cmd {
	t.inputMode = mode
	t.inputTarget = target
	t.input = newProjectsInput()
	t.input.SetValue(initial)
	return t.input.Focus()
}

func (t projectTreeComp) inputKey(c *ctx, msg tea.KeyPressMsg) (projectTreeComp, tea.Cmd) {
	if msg.Code == tea.KeyEscape {
		mode := t.inputMode
		t.inputMode = pmNone
		if mode == pmFilter {
			t.setFilter("")
		}
		return t, nil
	}
	if msg.String() == "enter" {
		val := strings.TrimSpace(t.input.Value())
		mode, target := t.inputMode, t.inputTarget
		t.inputMode = pmNone
		if val == "" || mode != pmRename {
			return t, nil
		}
		return t, c.m.renameProjectCmd(target, val)
	}
	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	if t.inputMode != pmFilter {
		return t, cmd
	}
	t.setFilter(t.input.Value())
	return t, cmd
}

func (t projectTreeComp) removeConfirm(c *ctx, msg tea.KeyPressMsg) (projectTreeComp, tea.Cmd) {
	wsID, force := t.pendingRemove, t.pendingRemoveForce
	t.pendingRemove, t.pendingRemoveForce = "", false
	if msg.String() != "y" {
		return t, nil
	}
	if t.removing == nil {
		t.removing = map[string]bool{}
	}
	t.removing[wsID] = true
	return t, c.m.removeWorkspaceCmd(wsID, force)
}

func (t projectTreeComp) newWorkspace(c *ctx) (projectTreeComp, tea.Cmd) {
	projID := t.cursorProjectID()
	if projID == "" {
		c.setFlash("select a project or workspace first")
		return t, nil
	}
	return t.startCreate(c, projID)
}

func (t projectTreeComp) startCreate(c *ctx, projectID string) (projectTreeComp, tea.Cmd) {
	p, _ := t.findProject(projectID)
	t.createSeq++
	pick := newCreatePicker(projectID, p.Name, p.DefaultBranch, t.createSeq)
	cmd := pick.name.Focus()
	c.openPopup(pick)
	return t, cmd
}

func (t projectTreeComp) renameProject(c *ctx) (projectTreeComp, tea.Cmd) {
	projID := t.cursorProjectID()
	if projID == "" {
		c.setFlash("select a project first")
		return t, nil
	}
	p, _ := t.findProject(projID)
	cmd := t.startInput(pmRename, projID, p.Name)
	return t, cmd
}

func (t projectTreeComp) forgetProject(c *ctx) projectTreeComp {
	projID := t.cursorProjectID()
	if projID == "" {
		c.setFlash("select a project first")
		return t
	}
	p, _ := t.findProject(projID)
	act := c.m.workspaceActivity()
	live := 0
	for _, w := range p.Workspaces {
		live += act[w.ID].live
	}
	if live > 0 {
		it := "it"
		if live > 1 {
			it = "them"
		}
		c.setFlash(p.Name + " has " + plural(live, "live session") + " · kill " + it + " first")
		return t
	}
	t.pendingForget = projID
	return t
}

func (t projectTreeComp) setHidden(c *ctx, hide, unhide bool) tea.Cmd {
	projID := t.cursorProjectID()
	if projID == "" {
		return nil
	}
	p, _ := t.findProject(projID)
	switch {
	case p.Hidden && unhide:
		return c.m.setHiddenCmd(projID, false, "unhid "+p.Name)
	case !p.Hidden && hide:
		ok := "hid " + p.Name
		if !t.showHidden {
			ok += " · " + c.m.keyText(projectsKeys.ShowHidden) + " shows hidden"
		}
		return c.m.setHiddenCmd(projID, true, ok)
	}
	return nil
}

func (t projectTreeComp) setPinned(c *ctx, pin, unpin bool) tea.Cmd {
	projID := t.cursorProjectID()
	if projID == "" {
		return nil
	}
	p, _ := t.findProject(projID)
	switch {
	case p.Pinned && unpin:
		return c.m.setPinnedCmd(projID, false, "unpinned "+p.Name)
	case !p.Pinned && pin:
		return c.m.setPinnedCmd(projID, true, "pinned "+p.Name)
	}
	return nil
}

func (t projectTreeComp) removeWorkspace(c *ctx, force bool) projectTreeComp {
	r, ok := t.cursorRow()
	switch {
	case !ok:
		return t
	case r.kind != rowWorkspace:
		c.setFlash("select a workspace to remove")
		return t
	case r.isMain:
		c.setFlash("cannot remove the main worktree")
		return t
	case t.removing[r.id]:
		c.setFlash("already removing " + r.label)
		return t
	}
	if n := c.m.workspaceActivity()[r.id].live; n > 0 {
		it := "it"
		if n > 1 {
			it = "them"
		}
		c.setFlash(r.label + " has " + plural(n, "live session") + " · kill " + it + " first")
		return t
	}
	t.pendingRemove, t.pendingRemoveForce = r.id, force
	return t
}

func (t projectTreeComp) retarget(c *ctx) tea.Cmd {
	wsID := t.selectedWorkspaceID()
	if wsID == "" {
		c.setFlash("select a workspace to change its target")
		return nil
	}
	projID := t.cursorProjectID()
	pick := newBranchPicker()
	pick.current = c.m.targetOf(wsID)
	label := wsID
	if r, ok := t.cursorRow(); ok {
		label = r.label
	}
	c.openPopup(retargetPicker{workspaceID: wsID, projectID: projID, label: label, pick: pick})
	return c.m.fetchBranchesCmd(projID)
}

func (t projectTreeComp) runSetup(c *ctx) tea.Cmd {
	wsID := t.selectedWorkspaceID()
	if wsID == "" {
		c.setFlash("select a workspace to run its setup")
		return nil
	}
	c.setFlash("running setup")
	client := c.m.client
	return func() tea.Msg {
		err := client.Call(api.MethodWorkspaceRunSetup, api.WorkspaceRef{WorkspaceID: wsID}, nil)
		return projectsActionMsg{verb: "run setup", ok: "running setup", err: err}
	}
}

func (m model) updateTree(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	comp, cmd := m.left.tree.update(c, msg)
	m.left.tree = comp.(projectTreeComp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}
