package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// logsAvail is the body line count: total height minus 4 chrome lines (title, two
// blanks, footer).
func (m model) logsAvail() int { return max(1, m.height-4) }

// hasLogsTab reports whether the Logs tab exists (only when the TUI spawned an
// embedded node and holds its log buffer).
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
	// Gutter-pad the header to line up with the other home tabs, but keep the log
	// body flush-left at full width (logs read better left-aligned).
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
		// Each ring line wraps to >=1 display line; window of `avail` ring lines
		// yields >=avail display lines, so clamping to avail fills the screen.
		var disp []string
		for _, ln := range m.logs.LinesRange(off, avail) {
			disp = append(disp, strings.Split(ansi.Hardwrap(ln, m.width, false), "\n")...)
		}
		if len(disp) > avail {
			if m.logsFollow {
				disp = disp[len(disp)-avail:] // keep newest
			} else {
				disp = disp[:avail] // anchor top at scrolled line
			}
		}
		body = strings.Join(disp, "\n")
	}
	footer := m.footer(listKeys.TabNext, logsKeys.Up, logsKeys.Bottom, logsKeys.Back)
	return pinFooter(gutter+title+"\n\n"+body, footer, m.width, m.height)
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

// logsScrollBy moves the viewport by delta lines (negative = up), clamped, and
// re-engages follow when landing on the newest page. Delta (not absolute offset)
// so it applies after logsUnfollow pins the live bottom.
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

// actOpenLogs switches to the Logs tab. No-op without a spawned node, keeping the
// Sessions/History bindings inert when there's no buffer.
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
