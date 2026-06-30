package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// grouped reports whether any session carries a node label (i.e. the client is
// connected to a gateway), which turns on per-host grouping in the list view.
func (m model) grouped() bool {
	for _, s := range m.sessions {
		if s.NodeLabel != "" {
			return true
		}
	}
	return false
}

// groupOffline reports whether every session from the given node is offline
// (its uplink to the gateway is currently down).
func (m model) groupOffline(label string) bool {
	seen := false
	for _, s := range m.sessions {
		if s.NodeLabel == label {
			seen = true
			if !s.Offline {
				return false
			}
		}
	}
	return seen
}

// needsYouHeader labels the cross-host group of awaiting-input sessions shown at
// the top of the list (mirrors the mobile "Needs you" section).
func (m model) needsYouHeader() string {
	return StyleAccentBold.Render("Needs you")
}

// sectionKey assigns a session to a list section: all awaiting-input sessions
// share one cross-host "Needs you" section; every other session belongs to its
// host's section. A header is drawn whenever this key changes between rows.
func sectionKey(s session.Session) string {
	if s.Status == session.StatusAwaitingInput {
		return "\x00needs-you"
	}
	return "host:" + s.NodeLabel
}

// groupHeader renders the per-host section header shown above a node's cards,
// flagged when that node is currently disconnected from the gateway.
func (m model) groupHeader(label string) string {
	name := label
	if name == "" {
		name = "local"
	}
	h := StyleSecondary.Render("▌ " + name)
	if m.groupOffline(label) {
		h += dimStyle.Render("  (offline)")
	}
	return h
}

// Shared view styles, bound to resolved theme colors by initStyles(). They are
// zero-valued (plain rendering) until Run() initializes the theme, which is fine
// for tests that render without a theme.
var (
	headerStyle lipgloss.Style
	dimStyle    lipgloss.Style
	cursorStyle lipgloss.Style
	userStyle   lipgloss.Style
	asstStyle   lipgloss.Style
)

// initStyles binds the shared view styles to theme colors. Called from Run()
// after initTheme().
func initStyles() {
	headerStyle = StyleAccentBold
	dimStyle = StyleDim
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorOngoing)
	userStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorInfo)
	asstStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorModelOpus)
}

// statusGlyph is the list marker for a status. Working sessions are drawn with an
// animated spinner by the card renderer instead of this static dot.
func statusGlyph(s session.Status) string {
	switch s {
	case session.StatusWorking:
		return "●"
	case session.StatusAwaitingInput:
		return "◆"
	case session.StatusIdle:
		return "○"
	case session.StatusDead:
		return "✗"
	default:
		return "·"
	}
}

// interactionHint renders a short, attention-colored summary of what a waiting
// session needs, for the session list.
func interactionHint(ix *session.Interaction) string {
	if ix == nil {
		return StyleAccentBold.Render("needs input")
	}
	switch ix.Kind {
	case session.InteractionPermission:
		s := "needs permission"
		if ix.ToolName != "" {
			s += " · " + ix.ToolName
		}
		return StyleAccentBold.Render(s)
	case session.InteractionQuestion:
		if len(ix.Questions) > 1 {
			return StyleAccentBold.Render(fmt.Sprintf("questions · %d", len(ix.Questions)))
		}
		if len(ix.Questions) == 1 && ix.Questions[0].Question != "" {
			return StyleAccentBold.Render("question · " + truncate(ix.Questions[0].Question, 40))
		}
		return StyleAccentBold.Render("question")
	case session.InteractionPlan:
		return StyleAccentBold.Render("plan approval")
	default: // idle / generic notification
		if ix.Message != "" {
			return StyleAccentBold.Render(truncate(ix.Message, 50))
		}
		return StyleAccentBold.Render("waiting")
	}
}

func (m model) View() tea.View {
	var content string
	switch m.mode {
	case modeSession:
		content = m.sessionView()
	case modeScreen:
		content = m.screenView()
	case modeHistoryProjects:
		content = m.historyProjectsView()
	case modeHistorySessions:
		content = m.historySessionsView()
	case modeHistoryTranscript:
		content = m.historyTranscriptView()
	case modeLogs:
		content = m.logsView()
	default:
		content = m.listView()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// homeTabs renders the home screen's Sessions / History (/ Logs) tab bar,
// highlighting the active tab. The Logs tab shows only when the TUI spawned an
// embedded node (see hasLogsTab). The active tab is passed in by the caller.
func (m model) homeTabs(active viewMode) string {
	sess, hist, logs := StyleDim, StyleDim, StyleDim
	switch active {
	case modeList:
		sess = StyleAccentBold
	case modeHistoryProjects:
		hist = StyleAccentBold
	case modeLogs:
		logs = StyleAccentBold
	}
	out := sess.Render("Sessions") + StyleDim.Render("   ") + hist.Render("History")
	if m.hasLogsTab() {
		out += StyleDim.Render("   ") + logs.Render("Logs")
	}
	return out
}

func (m model) listView() string {
	if m.spawn.active() {
		return m.spawnView()
	}
	title := Icon.Claude.Render() + " " + headerStyle.Render("argus")
	if m.reconnecting {
		title += dimStyle.Render("  (reconnecting…)")
	}
	title += "    " + m.homeTabs(modeList)

	// Empty state: a warm welcome with the next steps that actually work here.
	if len(m.order) == 0 {
		var b strings.Builder
		b.WriteString(title + dimStyle.Render("  ·  your AI coding sessions, one place") + "\n\n")
		b.WriteString(StyleAccentBold.Render("Nothing here yet — welcome!") + "\n\n")
		b.WriteString(StyleSecondary.Render("Start Claude Code in a tmux pane, or press ") +
			StyleAccentBold.Render("n") +
			StyleSecondary.Render(" to spawn one right here.") + "\n\n")
		b.WriteString(m.footer(listKeys.TabNext, listKeys.New, listKeys.Refresh, listKeys.Quit))
		return b.String()
	}

	// Populated: centered, scrollable session cards.
	cardW := min(m.containerWidth(), 78)
	if cardW < 30 {
		cardW = 30
	}

	// Render every card to lines, tracking the cursor card's line range so the
	// window can keep it on screen. On a gateway, a host header precedes each group.
	grouped := m.grouped()
	var lines []string
	curStart, curEnd := 0, 0
	for i, id := range m.order {
		s := m.sessions[id]
		if i > 0 {
			lines = append(lines, "") // blank separator between cards / before headers
		}
		newSection := i == 0 || sectionKey(s) != sectionKey(m.sessions[m.order[i-1]])
		if newSection {
			switch {
			case s.Status == session.StatusAwaitingInput:
				lines = append(lines, m.needsYouHeader())
			case grouped:
				lines = append(lines, m.groupHeader(s.NodeLabel))
			}
		}
		start := len(lines)
		lines = append(lines, strings.Split(m.sessionCard(s, i == m.cursor, cardW), "\n")...)
		if i == m.cursor {
			curStart, curEnd = start, len(lines)
		}
	}

	// Window to the available height (chrome: title + blank + blank + footer = 4),
	// keeping the cursor card fully visible.
	lines = windowSpan(lines, curStart, curEnd, max(1, m.height-4))

	footer := m.footer(listKeys.Up, listKeys.Open, listKeys.Screen, listKeys.Jump,
		listKeys.TabNext, listKeys.New, listKeys.Kill, listKeys.Refresh, listKeys.Quit)
	switch {
	case m.pendingKill && m.cursor < len(m.order):
		footer = asstStyle.Render("kill this session? y/n")
	case m.flash != "":
		footer = asstStyle.Render(m.flash)
	}

	inner := title + "\n\n" + strings.Join(lines, "\n") + "\n\n" + footer
	return centerBlock(inner, cardW, m.width)
}

// spawnView renders the active "new session" flow as a dedicated, vertically
// scrolling screen. List steps (node, dir) render one choice per line, windowed
// to the terminal height with the cursor kept visible; text steps (custom path,
// name, command) render a labeled input line. Rows are width-clamped so a long
// project list or long paths never overflow the screen.
func (m model) spawnView() string {
	cardW := historyWidth(m)
	title := Icon.Claude.Render() + " " + headerStyle.Render("argus") +
		dimStyle.Render("  ·  new session")
	avail := max(1, m.height-4)
	navFooter := dimStyle.Render("↑/↓ move · enter select · esc cancel")

	var body, footer string
	switch m.spawn.step {
	case spawnStepNode:
		cards := make([]string, len(m.spawn.nodes))
		for i, n := range m.spawn.nodes {
			sub := ""
			if !n.Capabilities.SpawnSession {
				sub = "no tmux" // disabled: can't spawn here
			}
			cards[i] = spawnChoiceRow(nodeName(n.Label, n.ID), sub, i == m.spawn.cursor, cardW)
		}
		body = StyleSecondaryBold.Render("Spawn on which node?") + "\n\n" +
			renderCardList(cards, m.spawn.cursor, max(1, avail-2))
		footer = navFooter
	case spawnStepDir:
		if m.spawn.custom {
			body = StyleSecondaryBold.Render("Working directory") + "\n\n" +
				asstStyle.Render(truncate(m.spawn.cwd, cardW-1)+"▏")
			footer = dimStyle.Render("type a path · enter confirm · esc cancel")
			break
		}
		cards := make([]string, 0, m.spawn.dirCursorMax())
		for i, p := range m.spawn.dirs {
			cards = append(cards, spawnChoiceRow(p.Label, p.Cwd, i == m.spawn.cursor, cardW))
		}
		cards = append(cards, spawnChoiceRow("Custom path…", "", m.spawn.cursor == len(m.spawn.dirs), cardW))
		body = StyleSecondaryBold.Render("Choose a directory") + "\n\n" +
			renderCardList(cards, m.spawn.cursor, max(1, avail-2))
		footer = navFooter
	case spawnStepPrompt:
		lines := strings.Split(m.spawn.prompt, "\n")
		for i, ln := range lines {
			lines[i] = truncateLine(ln, cardW)
		}
		lines[len(lines)-1] += "▏" // cursor at the end; Split always yields ≥1 line
		// Window to the height left after the label + blank, keeping the tail
		// (where typing happens) visible so a long prompt never overflows.
		if budget := max(1, avail-2); len(lines) > budget {
			lines = lines[len(lines)-budget:]
		}
		body = StyleSecondaryBold.Render("Initial prompt") + " " + dimStyle.Render("(required)") + "\n\n" +
			asstStyle.Render(strings.Join(lines, "\n"))
		footer = dimStyle.Render("enter launch · shift+enter/ctrl+j newline · esc cancel")
	}

	return centerBlock(title+"\n\n"+body+"\n\n"+footer, cardW, m.width)
}

// spawnChoiceRow renders one selectable row in the node/dir steps: a cursor
// marker, the label, and an optional dimmed sub-line (e.g. the project path),
// the whole row clamped to w so it never overflows the screen width.
func spawnChoiceRow(label, sub string, sel bool, w int) string {
	marker, name := "  ", dimStyle.Render(label)
	if sel {
		marker, name = asstStyle.Render("❯ "), headlineStyle(sel).Render(label)
	}
	line := marker + name
	if sub != "" {
		rest := w - lipgloss.Width(line) - 2
		if rest >= 8 {
			line += "  " + dimStyle.Render(truncate(sub, rest))
		}
	}
	return truncateLine(line, w)
}

func (m model) screenView() string {
	s := m.sessions[m.selectedID]
	var b strings.Builder
	b.WriteString(headerStyle.Render("argus · "+s.Tmux.SessionName) +
		dimStyle.Render(fmt.Sprintf("  [%s] %s", paneTag(s), statusWord(s))) + "\n\n")

	body := m.screen
	if m.screenErr != nil {
		body = dimStyle.Render("screen unavailable: " + m.screenErr.Error())
	}
	innerW := max(10, m.width-2)  // the rounded border eats 2 columns
	visible := max(1, m.height-6) // header(1)+blank(1)+border(2)+blank(1)+footer(1)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) > visible { // show the most recent screen content
		lines = lines[len(lines)-visible:]
	}
	// The captured lines carry tmux's SGR color escapes (-e); clip each to the box
	// width (no reflow — keep it a 1:1 mirror) and reset so colors don't bleed.
	for i, line := range lines {
		line = truncateLine(line, innerW)
		if m.screenErr == nil {
			line += "\x1b[0m"
		}
		lines[i] = line
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Width(innerW).
		Render(strings.Join(lines, "\n"))
	b.WriteString(box + "\n")

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("keys go to the session · ") + m.footer(screenLeave))
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
