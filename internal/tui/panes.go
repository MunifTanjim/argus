package tui

import tea "charm.land/bubbletea/v2"

type pane int

const (
	paneTree pane = iota
	paneMain
	paneFiles
	paneDock
)

func (m model) currentPane() pane {
	switch m.mode {
	case modeProjects:
		switch {
		case m.treeFocused():
			return paneTree
		case m.projects.focus == focusFiles && m.filesVisible():
			return paneFiles
		}
	case modeSession:
		switch {
		case m.projects.focus == focusFiles:
			return paneFiles
		case m.focus == focusDock:
			return paneDock
		}
	}
	return paneMain
}

func (m model) filesReachable() bool {
	return m.filesVisible() && m.currentWorkspace() != ""
}

// handlePaneKey runs the focus-*-pane commands. A command with no pane in its
// direction still uses the key.
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
		if m.mode == modeSession && m.currentPane() == paneMain && m.sessionInteraction() != nil {
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
		if m.mode == modeProjects {
			return m.focusPane()
		}
		m.projects.focus = focusPane
	case paneMain:
		if m.mode == modeProjects {
			if m.sidebarVisible() {
				m.projects.focus = focusTree
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
			m.projects.focus = focusFiles
		}
	}
	return m, nil
}

func (m model) cyclePane(d int) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeProjects:
		return m.cycleFocus(d)
	case modeSession:
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
	m.projects.focus = focusPane
	m.focus = focusHistory
	switch p {
	case paneFiles:
		m.projects.focus = focusFiles
	case paneDock:
		m.focus = focusDock
		if m.idleComposerActive() {
			m.sizeIdleReply()
		}
	}
	return m
}

// openTree leaves a screen other than projects for the tree: on the session's
// workspace row from a session, else on the Home row. The offline viewer and a
// hidden sidebar have no tree.
func (m model) openTree() (tea.Model, tea.Cmd) {
	if m.viewer || !m.sidebarVisible() {
		return m, nil
	}
	ws := m.currentWorkspace()
	var cmd tea.Cmd
	if m.mode == modeSession {
		cmd = m.closeSessionStreams()
	}
	m.mode = modeProjects
	m.projects.focus = focusTree
	if ws == "" || !m.projects.selectRow(ws) {
		m.projects.cursor = 0
	}
	mm, sync := m.syncPane()
	return mm, tea.Batch(cmd, sync)
}
