package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
	"github.com/charmbracelet/x/vt"
)

type shownView int

const (
	viewHome shownView = iota
	viewSession
	viewScreen
	viewHistoryProjects
	viewHistorySessions
	viewHistoryTranscript
	viewLogs
	viewTree
	viewTerminals
)

func viewOf(m model) shownView {
	v, _ := viewFor(m, m.baseComp())
	return v
}

// ok is false for an overlay.
func viewFor(m model, comp component) (shownView, bool) {
	switch c := comp.(type) {
	case nil:
		return viewHome, true
	case screenComp:
		return viewScreen, true
	case logsComp:
		return viewLogs, true
	case terminalsComp:
		return viewTerminals, true
	case historyComp:
		if c.inProject {
			return viewHistorySessions, true
		}
		return viewHistoryProjects, true
	case transcriptComp:
		if c.live {
			return viewSession, true
		}
		return viewHistoryTranscript, true
	case homeComp:
		return viewHome, true
	case workspaceComp, summaryComp:
		return viewTree, true
	}
	return 0, false
}

// withView sets the back stack and the focus that a real path to v leaves.
func withView(m model, v shownView) model {
	if v != viewTree && m.focused == leftSidebar {
		m.focused = mainPane
	}
	switch {
	case v == viewSession || v == viewScreen:
		return withViews(m, viewHome, v)
	case v == viewHistoryTranscript && !m.viewer:
		return withViews(m, viewHistorySessions, v)
	}
	m.main = backStack{mainComp(m, v)}
	return m
}

func withViews(m model, vs ...shownView) model {
	m.main = nil
	for _, v := range vs {
		m.main = m.main.push(mainComp(m, v))
	}
	return m
}

func mainComp(m model, v shownView) component {
	switch v {
	case viewSession:
		return newLiveTranscript("")
	case viewHistoryTranscript:
		return newTranscript()
	case viewLogs:
		return newLogsComp()
	case viewTerminals:
		return terminalsComp{}
	case viewHistoryProjects:
		return historyComp{}
	case viewHistorySessions:
		return historyComp{inProject: true}
	case viewScreen:
		return screenComp{}
	case viewTree:
		return rowComp(m)
	}
	return m.homePane()
}

func rowComp(m model) component {
	r, _ := m.cursorRow()
	switch r.kind {
	case rowWorkspace:
		return workspaceComp{ws: r.id}
	case rowProject, rowNode:
		return summaryComp{kind: r.kind, id: r.id}
	}
	return m.homePane()
}

func (m *model) leaveView() { m.underOverlays(func() { m.main, _ = m.main.pop() }) }

func returnView(m model) shownView {
	m.leaveView()
	return viewOf(m)
}

// baseKey bypasses the overlays.
func (m model) baseKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	i := m.baseTop() - 1
	if i < 0 {
		return m, nil
	}
	over := m.main[i+1:]
	m.main = m.main[: i+1 : i+1]
	c := &ctx{m: &m}
	comp, cmd, _ := m.main[i].handleKey(c, msg)
	m.main = append(m.main.replaceAt(i, comp), over...)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func withFocus(m model, k container) model {
	m.focused = k
	return m
}

func withFile(m model, f fileComp) model {
	c := &ctx{m: &m}
	f.show(c)
	m.apply(c)
	return m
}

func (m model) hasOpenFile() bool {
	_, ok := m.openFile()
	return ok
}

func fileOf(m model) fileComp {
	f, _ := m.openFile()
	return f
}

func openCommit(m model, cm api.Commit) (model, tea.Cmd) {
	var cmd tea.Cmd
	m.right.changes, cmd = m.right.changes.openCommit(&ctx{m: &m}, cm)
	return m, cmd
}

func treeKey(m model, msg tea.KeyPressMsg) (model, tea.Cmd) {
	c := &ctx{m: &m}
	comp, cmd, _ := m.left.tree.handleKey(c, msg)
	m.left.tree = comp.(projectTreeComp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func moveTree(m model, i int) model {
	m.left.tree = m.left.tree.move(i)
	return m
}

func openCreate(m model, projectID string) model {
	c := &ctx{m: &m}
	m.left.tree, _ = m.left.tree.startCreate(c, projectID)
	m.apply(c)
	return m
}

func closePicker(m model) model {
	m.popups = m.popups.closeFront()
	return m
}

func createOf(m model) createPicker {
	p, _ := m.frontCreate()
	return p
}

func createOpen(m model) bool {
	_, ok := m.frontCreate()
	return ok
}

func withCreate(m model, edit func(*createPicker)) model {
	p, ok := m.frontCreate()
	if !ok {
		panic("withCreate: no create picker open")
	}
	edit(&p)
	m.popups = m.popups.replaceFront(p)
	return m
}

func retargetOf(m model) (retargetPicker, bool) {
	p, ok := m.popups.front().(retargetPicker)
	return p, ok
}

func (m model) pickerOpen() bool {
	_, create := m.frontCreate()
	_, retarget := retargetOf(m)
	return create || retarget
}

func (m model) inputActive() bool { return m.left.tree.inputMode != pmNone }

func historyOf(m model) historyComp {
	if i := m.historyAt(); i >= 0 {
		return m.main[i].(historyComp)
	}
	return historyComp{}
}

func withHistoryProjects(m model, ps ...session.HistoryProject) model {
	m = withView(m, viewHistoryProjects)
	mm, _ := m.updateHistory(histProjectsMsg{projects: ps})
	return mm.(model)
}

func withHistorySessions(m model, p session.HistoryProject, page session.HistorySessionPage) model {
	m = withHistoryProjects(m, p)
	mm, _ := m.baseKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm, _ = mm.(model).updateHistory(histSessionsMsg{projectDir: p.ProjectDir, page: page})
	return mm.(model)
}

func logsOf(m model) logsComp {
	l, _ := m.baseComp().(logsComp)
	return l
}

func mainView(m model) string {
	return m.baseComp().view(&ctx{m: &m}, m.bodyWidth(), m.bodyHeight())
}

// paneView draws the main pane as the frame does: the dock under a live
// transcript, and the footer when the layout is bare.
func paneView(m model) string {
	l := m.layout()
	out := m.mainColumn(&ctx{m: &m}, l)
	if l.bare {
		out = pinFooter(out, m.currentFooter(), m.width, m.height)
	}
	return out
}

// framed reports whether the frame draws its title and sidebars.
func framed(m model) bool { return !m.layout().bare }

func homeOf(m model) homeComp { return m.homePane() }

func paneOf(m model) workspaceComp {
	p, _ := m.rootComp().(workspaceComp)
	return p
}

func withHome(m model, h homeComp) model {
	m.memory.home.home = h
	if _, ok := m.rootComp().(homeComp); ok {
		m.main = m.main.replaceAt(0, h)
	}
	return m
}

func withPane(m model, p workspaceComp) model {
	if _, ok := m.rootComp().(workspaceComp); !ok {
		panic("withPane: root is not a workspace pane")
	}
	m.main = m.main.replaceAt(0, p)
	return m
}

func selectRow(m model, id string) model {
	m.left.tree.selectRow(id)
	focus := m.focused
	m.showRow(id)
	m.focused = focus
	m, _ = m.syncMemory()
	return m
}

func standsFor(m model, comp component, v shownView) bool {
	got, ok := viewFor(m, comp)
	return ok && got == v
}

func isWorkspace(c component) bool {
	_, ok := c.(workspaceComp)
	return ok
}

func trOf(m model) transcriptComp {
	t, _ := m.baseComp().(transcriptComp)
	return t
}

func withTr(m model, edit func(t *transcriptComp)) model {
	i := m.baseTop() - 1
	t := m.main[i].(transcriptComp)
	edit(&t)
	m.main = m.main.replaceAt(i, t)
	return m
}

func withEntries(m model, entries []transcript.Entry) model {
	return withTr(m, func(t *transcriptComp) { t.transcript.entries = entries })
}

// tvOf's changes stay in the view until put stores them in m.
func tvOf(m *model) tview {
	t := m.baseComp().(transcriptComp)
	return t.bind(&ctx{m: m})
}

func tvIn(m model) tview { return tvOf(&m) }

func tvOver(m *model, t transcriptComp) tview { return t.bind(&ctx{m: m}) }

func bareTv() tview {
	m := testModel()
	return tvOver(&m, newTranscript())
}

func (m tview) put() {
	m.c.m.main = m.c.m.main.replaceAt(m.c.m.baseTop()-1, *m.transcriptComp)
	m.c.m.apply(m.c)
}

func onTr(m model, f func(v tview) tea.Cmd) (model, tea.Cmd) {
	v := tvOf(&m)
	cmd := f(v)
	v.put()
	return m, cmd
}

// openLive is enterSession without its stream.
func openLive(m model, id string) model {
	m.enterMain(newLiveTranscript(id))
	m.focused = mainPane
	m.dock = m.dock.follow(&ctx{m: &m}, id)
	return m
}

func withLive(m model, id string) model {
	m = withView(m, viewSession)
	m = withTr(m, func(t *transcriptComp) { t.sessionID = id })
	m.dock.session = id
	return m
}

func withScreenOf(m model, id string) model {
	s, ok := m.liveScreen()
	if !ok {
		panic("withScreenOf: no live screen open")
	}
	s.sessionID = id
	m.main = m.main.replaceTop(s)
	return m
}

func spawnOf(m model) spawnComp {
	s, _ := m.spawnTop()
	return s
}

func spawnOpen(m model) bool {
	_, ok := m.spawnTop()
	return ok
}

func spawnStepOf(m model) spawnStep { return spawnOf(m).step }

func withSpawn(m model, edit func(s *spawnComp, c *ctx)) model {
	s, ok := m.spawnTop()
	if !ok {
		panic("withSpawn: no spawn flow open")
	}
	c := &ctx{m: &m}
	edit(&s, c)
	m.main = m.main.replaceTop(s)
	m.apply(c)
	return m
}

func spawnView(m model) string {
	return spawnOf(m).view(&ctx{m: &m}, m.bodyWidth(), m.bodyHeight())
}

func withTerm(m model, termID string, term *vt.Emulator) model {
	s, ok := m.liveScreen()
	if !ok {
		panic("withTerm: no live screen open")
	}
	s.termID, s.term = termID, term
	m.main = m.main.replaceTop(s)
	return m
}

func leaveScreen(m model) (model, tea.Cmd) {
	res, cmd := m.handleScreenKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	return res.(model), cmd
}

func screenView(m model) string {
	s, _ := m.liveScreen()
	return s.view(&ctx{m: &m}, m.bodyWidth(), m.bodyHeight())
}

func scr(m model) screenComp {
	s, _ := m.liveScreen()
	return s
}

func (m *model) closeFileView() tea.Cmd {
	f, ok := m.openFile()
	if !ok {
		return nil
	}
	c := &ctx{m: m}
	f.leave(c)
	return m.apply(c)
}

func (m model) enterScreen(id string) (model, tea.Cmd) {
	c := &ctx{m: &m}
	open := attachScreen(c, id)
	cmd := m.apply(c)
	return m, tea.Batch(cmd, open)
}

func (m *model) beginPresetSpawn(nodeID, cwd, prompt string) tea.Cmd {
	c := &ctx{m: m}
	cmd := openPresetSpawn(c, nodeID, cwd, prompt)
	return tea.Batch(cmd, m.apply(c))
}

func treePane(m model, w, h int) string { return m.left.tree.view(&ctx{m: &m}, w, h) }

func isPane(comp component) bool {
	switch comp.(type) {
	case homeComp, workspaceComp, summaryComp:
		return true
	}
	return false
}

func (m model) onHomeRow() bool {
	r, ok := m.left.tree.cursorRow()
	return ok && r.kind == rowHome
}

func (m model) selectedWorkspaceID() string { return m.left.tree.selectedWorkspaceID() }

func (m model) cursorRow() (projectsRow, bool) { return m.left.tree.cursorRow() }

// A hidden tree never holds focus in effect.
func (m model) treeFocused() bool {
	return m.focused == leftSidebar && m.sidebarVisible()
}
