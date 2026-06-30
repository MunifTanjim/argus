package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// logsAvail is the number of body lines the logs view shows: total height minus
// the title line, the two blank separators, and the footer (4 chrome lines).
func (m model) logsAvail() int { return max(1, m.height-4) }

// hasLogsTab reports whether the Logs tab is available: it exists only when the
// TUI spawned an embedded node and so holds its log buffer.
func (m model) hasLogsTab() bool { return m.logs != nil }

// logsBottom is the top-line offset that shows the newest page.
func (m model) logsBottom() int {
	if m.logs == nil {
		return 0
	}
	return max(0, m.logs.Len()-m.logsAvail())
}

// logsUnfollow pins the current bottom as an absolute offset before manual
// scrolling, so lines arriving while paused don't shift the viewport.
func (m *model) logsUnfollow() {
	if m.logsFollow {
		m.logsScroll = m.logsBottom()
		m.logsFollow = false
	}
}

func (m model) logsView() string {
	if !m.hasLogsTab() {
		return ""
	}
	title := Icon.Claude.Render() + " " + headerStyle.Render("argus") + "    " + m.homeTabs(modeLogs)
	// Logs is a home tab: gutter-pad the header and footer so they line up with
	// the Sessions/History tabs, but keep the log body flush-left at full width
	// (logs need the horizontal room and read better left-aligned).
	cardW := historyWidth(m)
	gutter := strings.Repeat(" ", max(0, (m.width-cardW)/2))
	var body string
	if m.logs.Len() == 0 {
		body = dimStyle.Render("no logs yet")
	} else {
		avail := m.logsAvail()
		bottom := m.logsBottom()
		off := m.logsScroll
		if m.logsFollow {
			off = bottom
		}
		off = max(0, min(off, bottom))
		// Copy only the visible window, not the whole ring, on the render path.
		body = strings.Join(m.logs.LinesRange(off, avail), "\n")
	}
	footer := m.footer(listKeys.TabPrev, logsKeys.Up, logsKeys.Bottom, logsKeys.Back)
	return gutter + title + "\n\n" + body + "\n\n" + gutter + footer
}

func (m model) handleLogsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if mm, cmd, ok := m.dispatch(msg, logsTable); ok {
		return mm, cmd
	}
	return m, nil
}

var logsTable = []keyTableEntry{
	{logsKeys.Up, model.actLogsUp},
	{logsKeys.Down, model.actLogsDown},
	{logsKeys.HalfUp, model.actLogsHalfUp},
	{logsKeys.HalfDown, model.actLogsHalfDown},
	{logsKeys.Top, model.actLogsTop},
	{logsKeys.Bottom, model.actLogsBottom},
	{listKeys.TabPrev, model.actLogsToHistory},  // left/h → History
	{listKeys.TabNext, model.actLogsToSessions}, // right/l → Sessions
	{logsKeys.Back, model.actLogsToSessions},
}

// logsScrollBy moves the viewport by delta lines (negative = up), clamped to the
// buffer, and re-engages follow when the move lands on the newest page. Callers
// pass a delta rather than an absolute offset so it applies after logsUnfollow
// has pinned the live bottom.
func (m *model) logsScrollBy(delta int) {
	m.logsUnfollow()
	bottom := m.logsBottom()
	m.logsScroll = max(0, min(m.logsScroll+delta, bottom))
	m.logsFollow = m.logsScroll >= bottom
}

func (m model) actLogsUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.logsScrollBy(-1)
	return m, nil
}

func (m model) actLogsDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.logsScrollBy(1)
	return m, nil
}

func (m model) actLogsHalfUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.logsScrollBy(-m.logsAvail() / 2)
	return m, nil
}

func (m model) actLogsHalfDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.logsScrollBy(m.logsAvail() / 2)
	return m, nil
}

// actOpenLogs switches to the Logs tab. Shared by the Sessions (left/h) and
// History (right/l) bindings. It is a no-op without a spawned node, keeping
// those keys inert when there is no buffer to show.
func (m model) actOpenLogs(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.hasLogsTab() {
		return m, nil
	}
	m.mode = modeLogs
	m.logsFollow = true
	return m, nil
}

func (m model) actLogsTop(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.logsFollow = false
	m.logsScroll = 0
	return m, nil
}

func (m model) actLogsBottom(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.logsFollow = true
	return m, nil
}

func (m model) actLogsToHistory(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.mode = modeHistoryProjects
	m.history.projects, m.history.err, m.history.projCursor = nil, nil, 0
	return m, m.fetchHistProjects()
}

func (m model) actLogsToSessions(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.mode = modeList
	return m, nil
}
