package tui

import tea "charm.land/bubbletea/v2"

// homeView is the view that the Home row remembers.
type homeView int

const (
	homeSessions homeView = iota
	homeHistory
	homeLogs
	homeSession
	homeTerminals
)

// home and historyCursor keep the Home pane's cursor and the History tab's
// project cursor while other views show.
type homeEntry struct {
	view          homeView
	session       string
	home          homeComp
	historyCursor int
}

// viewMemory is the main pane view that each tree row remembers for this run:
// a session for each workspace that had one open, and Home's view.
type viewMemory struct {
	home homeEntry
	ws   map[string]string
}

func (m model) syncMemory() (model, tea.Cmd) {
	m.remember()
	m.prune()
	if m.rowGone() {
		// showHome can enter the session Home remembers, which focuses the main
		// pane.
		focused := m.focused
		cmd := m.showHome()
		m.focused = focused
		return m, cmd
	}
	return m, nil
}

func (m *model) remember() {
	if m.memory.ws == nil {
		m.memory.ws = map[string]string{}
	}
	switch b := m.baseComp().(type) {
	case transcriptComp:
		if b.live {
			m.rememberSession(b.sessionID)
		}
	case screenComp:
		if b.terminalID == "" {
			m.rememberSession(b.sessionID)
		}
	case workspaceComp:
		delete(m.memory.ws, b.ws)
	case homeComp:
		b.killID = ""
		m.memory.home.view, m.memory.home.session, m.memory.home.home = homeSessions, "", b
	case historyComp:
		m.memory.home.view, m.memory.home.session = homeHistory, ""
		m.memory.home.historyCursor = b.projCursor
	case logsComp:
		m.memory.home.view, m.memory.home.session = homeLogs, ""
	case terminalsComp:
		if b.nodeID == "" {
			m.memory.home.view, m.memory.home.session = homeTerminals, ""
		}
	}
}

func (m *model) rememberSession(id string) {
	s, ok := m.sessions[id]
	switch {
	case !ok:
	case s.WorkspaceID != "":
		m.memory.ws[s.WorkspaceID] = id
	default:
		m.memory.home.view, m.memory.home.session = homeSession, id
	}
}

func (m *model) prune() {
	listed := m.listedWorkspaces()
	for ws, id := range m.memory.ws {
		if _, ok := m.sessions[id]; !ok || (listed != nil && !listed[ws]) {
			delete(m.memory.ws, ws)
		}
	}
	if e := m.memory.home; e.view == homeSession {
		if _, ok := m.sessions[e.session]; !ok {
			m.memory.home.view, m.memory.home.session = homeSessions, ""
		}
	}
}

func (m model) listedWorkspaces() map[string]bool {
	t := m.left.tree
	if !t.loaded || t.err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, p := range t.data {
		for _, w := range p.Workspaces {
			out[w.ID] = true
		}
	}
	return out
}

// forgetBack drops the session of a live transcript that back took off the main
// pane from the memory of its row.
func (m *model) forgetBack(popped component) {
	t, ok := popped.(transcriptComp)
	if !ok || !t.live {
		return
	}
	if ws := m.sessions[t.sessionID].WorkspaceID; ws != "" && m.memory.ws[ws] == t.sessionID {
		delete(m.memory.ws, ws)
	}
	if e := m.memory.home; e.view == homeSession && e.session == t.sessionID {
		m.memory.home.view, m.memory.home.session = homeSessions, ""
	}
}

func (m model) mainRow() string {
	switch b := m.baseComp().(type) {
	case workspaceComp:
		return b.ws
	case summaryComp:
		return b.id
	case terminalsComp:
		if b.nodeID != "" {
			return b.nodeID
		}
	case transcriptComp:
		if b.live {
			return m.sessionRow(b.sessionID)
		}
	case screenComp:
		if b.row != "" {
			return b.row
		}
		return m.sessionRow(b.sessionID)
	}
	return homeRowID
}

func (m model) sessionRow(id string) string {
	s, ok := m.sessions[id]
	if !ok {
		return ""
	}
	if s.WorkspaceID != "" {
		return s.WorkspaceID
	}
	return homeRowID
}

func (m *model) openRow(id string) tea.Cmd {
	if r, ok := m.left.tree.row(id); ok && r.kind == rowWorkspace {
		m.left.tree.dropFilter(id)
	}
	cmd := m.showRow(id)
	m.focused = mainPane
	return cmd
}

func (m *model) showRow(id string) tea.Cmd {
	if m.mainRow() == id {
		return nil
	}
	if id == homeRowID {
		return m.showHome()
	}
	r, ok := m.left.tree.row(id)
	switch {
	case !ok:
		return nil
	case r.kind != rowWorkspace:
		cmd := m.resetMain(func() component { return summaryComp{kind: r.kind, id: id} })
		if r.kind == rowNode {
			cmd = tea.Batch(cmd, m.hostInfoCmd(id))
		}
		return cmd
	}
	cmd := m.resetMain(func() component { return workspaceComp{ws: id} })
	if s := m.memory.ws[id]; s != "" {
		mm, open := m.enterSession(s)
		*m = mm
		cmd = tea.Batch(cmd, open)
	}
	return cmd
}

func (m *model) showHome() tea.Cmd {
	e := m.memory.home
	switch {
	case e.view == homeHistory:
		cmd := m.resetMain(func() component { return historyComp{projCursor: e.historyCursor} })
		return tea.Batch(cmd, m.fetchHistProjects())
	case e.view == homeLogs && m.hasLogsTab():
		return m.resetMain(func() component { return newLogsComp() })
	case e.view == homeTerminals:
		return tea.Batch(m.resetMain(func() component { return terminalsComp{} }), m.loadTerminalsCmd())
	case e.view == homeSession:
		cmd := m.resetMain(func() component { return e.home })
		mm, open := m.enterSession(e.session)
		*m = mm
		return tea.Batch(cmd, open)
	}
	return m.resetMain(func() component { return e.home })
}

func (m *model) focusTree() {
	if !m.treeReachable() || m.focused == leftSidebar {
		return
	}
	m.focused = leftSidebar
	m.left.tree.follow(m.mainRow())
}

// The spawn flow returns focus to the tree, so the cursor stays on the row the
// flow started from.
func (m model) leaveTree(was container) model {
	if was == leftSidebar && m.focused != leftSidebar && m.spawnAt() < 0 {
		m.left.tree.follow(m.mainRow())
	}
	return m
}

func (m model) homePane() homeComp {
	if h, ok := m.rootComp().(homeComp); ok {
		return h
	}
	return m.memory.home.home
}

func (m model) onRowPane() bool {
	switch b := m.baseComp().(type) {
	case workspaceComp, summaryComp:
		return true
	case terminalsComp:
		return b.nodeID != ""
	}
	return false
}

func (m model) rowGone() bool {
	listed := m.listedWorkspaces()
	if listed == nil {
		return false
	}
	switch b := m.baseComp().(type) {
	case workspaceComp:
		return !listed[b.ws]
	case summaryComp:
		_, ok := b.row(&ctx{m: &m})
		return !ok
	case terminalsComp:
		if b.nodeID == "" {
			return false
		}
		_, ok := m.left.tree.row(b.nodeID)
		return !ok
	}
	return false
}
