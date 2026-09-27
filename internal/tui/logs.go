package tui

// logsAvail is the body line count: total height minus 4 chrome lines (title, two
// blanks, footer).
func (m model) logsAvail() int { return max(1, m.bodyHeight()-4) }

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
