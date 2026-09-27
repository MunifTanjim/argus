package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

// A key goes to exactly one handler, in this order: the live screen, the front
// popup, a raw focused component, the open help, the focus manager's keys, the
// focused container's keys, and the focused component. A colon opens the
// command line when none of the first four takes it, no sequence is pending,
// and the component has commands.

func (m model) focusedComp() component {
	if s, ok := m.liveScreen(); ok {
		return s
	}
	switch m.focused {
	case leftSidebar:
		return m.left.tree
	case rightSidebar:
		return m.right.current()
	case sessionDock:
		return m.dock
	}
	return m.main.top()
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The live screen is a passthrough (ctrl+c → Claude SIGINT, ctrl+] leaves).
	// Route it before the global quit so ctrl+c reaches Claude.
	if m.topScreen() >= 0 {
		return m.handleScreenKey(msg)
	}
	if msg.String() == "ctrl+c" {
		return m.quit()
	}
	if m.opensCmdLine(msg) {
		return m.openCmdLine()
	}
	if !m.keysRaw() {
		return m.resolveKey(msg)
	}
	if len(m.keyBuf) > 0 {
		m.clearKeys()
	}
	return m.runKey(msg)
}

func (m model) runKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.topScreen() >= 0 {
		return m.handleScreenKey(msg)
	}
	if p := m.popups.front(); p != nil {
		return m.popupKey(p, msg)
	}
	if m.focusedComp().raw(&ctx{m: &m}) {
		return m.componentKey(msg)
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	if mm, cmd, ok := m.focusKey(msg); ok {
		return mm, cmd
	}
	m, cmd, ok := m.containerKey(msg)
	if ok {
		return m, cmd
	}
	return m.componentKey(msg)
}

func (m model) focusKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if mm, cmd, ok := m.handlePaneKey(msg); ok {
		return mm, cmd, true
	}
	if mm, cmd, ok := m.handleSidebarToggle(msg); ok {
		return mm, cmd, true
	}
	switch {
	case m.offered(projectsKeys.Help) && m.matches(msg, projectsKeys.Help):
		if m.onTreeScreen() {
			m.flash = ""
		}
		m.showHelp = true
		return m, nil, true
	case m.offered(listKeys.Quit) && m.matches(msg, listKeys.Quit):
		mm, cmd := m.quit()
		return mm, cmd, true
	}
	return m, nil, false
}

func (m model) offered(b binding) bool {
	return offersKey(m.focusedComp().offers(&ctx{m: &m}), b)
}

func offersKey(bs []binding, b binding) bool {
	return slices.ContainsFunc(bs, func(o binding) bool { return o.name == b.name })
}

// The sidebar toggles act the same from every container.
func (m model) handleSidebarToggle(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.viewer {
		return m, nil, false
	}
	switch {
	case m.matches(msg, projectsKeys.ToggleSidebar):
		m.flash = ""
		m.toggleSidebar()
	case m.matches(msg, projectsKeys.ToggleFiles):
		m.flash = ""
		m.toggleFiles()
	default:
		return m, nil, false
	}
	return m, nil, true
}

// containerKey runs the focused container's keys: the sidebars resize, and the
// right sidebar switches tabs.
func (m model) containerKey(msg tea.KeyPressMsg) (model, tea.Cmd, bool) {
	var ok bool
	switch m.focused {
	case rightSidebar:
		m.flash = ""
		m.right, ok = m.right.handleKey(&ctx{m: &m}, msg)
	case leftSidebar:
		m.flash = ""
		m.left, ok = m.left.handleKey(&ctx{m: &m}, msg)
	}
	return m, nil, ok
}

func (m model) componentKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	var comp component
	var cmd tea.Cmd
	switch m.focused {
	case leftSidebar:
		comp, cmd, _ = m.left.tree.handleKey(c, msg)
		m.left.tree = comp.(projectTreeComp)
	case rightSidebar:
		comp, cmd, _ = m.right.current().handleKey(c, msg)
		m.right = m.right.store(comp)
	case sessionDock:
		comp, cmd, _ = m.dock.handleKey(c, msg)
		m.dock = comp.(dockComp)
	default:
		cmd = m.mainKey(c, msg)
	}
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

// mainKey gives msg to the main pane's top component; an overlay that leaves a
// key passes it to the component under it.
func (m *model) mainKey(c *ctx, msg tea.KeyPressMsg) tea.Cmd {
	var cmds []tea.Cmd
	for i := len(m.main) - 1; i >= 0; i-- {
		comp, cmd, used := m.main[i].handleKey(c, msg)
		m.main = m.main.replaceAt(i, comp)
		cmds = append(cmds, cmd)
		if used || !comp.layer().over() {
			break
		}
	}
	return tea.Batch(cmds...)
}

// repairFocus moves focus off a container that is hidden or empty. The live
// screen takes every key, so a dock under it keeps focus for the return.
func (m model) repairFocus() (model, tea.Cmd) {
	_, onScreen := m.liveScreen()
	switch {
	case m.focused == sessionDock && !m.dockShown() && !onScreen:
		m.focused = mainPane
	case m.focused == rightSidebar && (!m.filesVisible() || m.currentWorkspace() == ""):
		// The cursor left the workspaces; the tree is where it moved.
		toTree := m.filesVisible() && isPane(m.baseComp()) && m.sidebarVisible()
		m.focused = mainPane
		if toTree {
			m.focused = leftSidebar
		}
	case m.focused == leftSidebar && !m.sidebarVisible():
		// With no tree on screen, the Home row's pane is the Home pane itself.
		if _, onHome := m.baseComp().(homeComp); onHome && m.onHomeRow() {
			cmd := m.enterHome()
			return m, cmd
		}
		m.focused = mainPane
		if _, ok := m.baseComp().(workspaceComp); ok {
			cmd := m.syncPane()
			return m, cmd
		}
	}
	return m, nil
}

type pane int

const (
	paneTree pane = iota
	paneMain
	paneFiles
	paneDock
)

func (m model) currentPane() pane {
	switch {
	case m.onTreeScreen():
		switch {
		case m.treeFocused():
			return paneTree
		case m.focused == rightSidebar && m.filesVisible():
			return paneFiles
		}
	case m.inSession():
		switch {
		case m.focused == rightSidebar:
			return paneFiles
		case m.focused == sessionDock:
			return paneDock
		}
	}
	return paneMain
}

func (m model) filesReachable() bool {
	return m.filesVisible() && m.currentWorkspace() != ""
}

// A pane command with no pane in its direction still uses the key.
func (m model) handlePaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	pk := paneKeys
	m.flash = ""
	var (
		mm  tea.Model = m
		cmd tea.Cmd
	)
	switch {
	case m.matches(msg, pk.Left):
		mm, cmd = m.paneLeft()
	case m.matches(msg, pk.Right):
		mm, cmd = m.paneRight()
	case m.matches(msg, pk.Down):
		if m.inSession() && m.currentPane() == paneMain && m.sessionInteraction() != nil {
			mm = m.focusSessionPane(paneDock)
		}
	case m.matches(msg, pk.Up):
	case m.matches(msg, pk.Next):
		mm, cmd = m.cyclePane(1)
	case m.matches(msg, pk.Prev):
		mm, cmd = m.cyclePane(-1)
	default:
		return m, nil, false
	}
	return mm, cmd, true
}

func (m model) paneLeft() (tea.Model, tea.Cmd) {
	switch m.currentPane() {
	case paneFiles:
		if m.onTreeScreen() {
			return m.focusPane()
		}
		m.focused = mainPane
	case paneMain:
		if m.onTreeScreen() {
			if m.sidebarVisible() {
				m.focused = leftSidebar
			}
			return m, nil
		}
		return m.openTree()
	}
	return m, nil
}

func (m model) paneRight() (tea.Model, tea.Cmd) {
	switch m.currentPane() {
	case paneTree:
		return m.focusPane()
	case paneMain:
		if m.filesReachable() {
			m.focused = rightSidebar
		}
	}
	return m, nil
}

func (m model) cyclePane(d int) (tea.Model, tea.Cmd) {
	switch {
	case m.onTreeScreen():
		return m.cycleFocus(d)
	case m.inSession():
		order := []pane{paneMain}
		if m.filesReachable() {
			order = append(order, paneFiles)
		}
		if m.sessionInteraction() != nil {
			order = append(order, paneDock)
		}
		cur, i := m.currentPane(), 0
		for j, p := range order {
			if p == cur {
				i = j
			}
		}
		return m.focusSessionPane(order[(i+d+len(order))%len(order)]), nil
	}
	return m, nil
}

func (m model) focusSessionPane(p pane) model {
	m.focused = mainPane
	switch p {
	case paneFiles:
		m.focused = rightSidebar
	case paneDock:
		m.focused = sessionDock
		if m.idleComposerActive() {
			m.sizeIdleReply()
		}
	}
	return m
}

// openTree leaves the main pane's component for the tree screen: on the
// session's workspace row from a session, else on the Home row. The offline
// viewer and a hidden sidebar have no tree.
func (m model) openTree() (tea.Model, tea.Cmd) {
	if m.viewer || !m.sidebarVisible() {
		return m, nil
	}
	ws := m.currentWorkspace()
	cmd := m.resetMain(func() component { return m.rowPane() })
	m.focused = leftSidebar
	if ws == "" || !m.left.tree.selectRow(ws) {
		m.left.tree.cursor = 0
	}
	sync := m.syncPane()
	return m, tea.Batch(cmd, sync)
}
