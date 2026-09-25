package tui

import (
	_ "embed"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// grouped reports whether any session carries a node label (gateway-connected),
// turning on per-host grouping in the list view.
func (m model) grouped() bool {
	for _, s := range m.sessions {
		if s.NodeLabel != "" {
			return true
		}
	}
	return false
}

// groupOffline reports whether every session from the node is offline.
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

// needsYouHeader labels the cross-host group of awaiting-input sessions at the top
// of the list (mirrors the mobile "Needs you" section).
func (m model) needsYouHeader() string {
	return StyleAccentBold.Render("Needs you")
}

// sectionKey assigns a session to a list section: awaiting-input sessions share
// one cross-host "Needs you" section, others belong to their host. A header is
// drawn whenever this key changes between rows.
func sectionKey(s session.Session) string {
	if s.Status == session.StatusAwaitingInput {
		return "\x00needs-you"
	}
	return "host:" + s.NodeLabel
}

// groupHeader renders the per-host section header, flagged when the node is
// disconnected from the gateway.
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

// Shared view styles, bound to theme colors by initStyles(). Zero-valued (plain
// rendering) until Run() initializes the theme, which is fine for theme-less tests.
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
	asstStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorAssistant)
}

const maxCardWidth = 78

// statusGlyph is the list marker for a status. Working sessions use an animated
// spinner from the card renderer instead of this static dot.
func statusGlyph(s session.Status) string {
	switch s {
	case session.StatusWorking:
		return "●"
	case session.StatusAwaitingInput:
		return "◆"
	case session.StatusIdle:
		return "○"
	case session.StatusStarting:
		return "◌"
	case session.StatusDead:
		return "✗"
	default:
		return "·"
	}
}

// interactionHint renders a short, attention-colored summary of what a waiting
// session needs.
func interactionHint(ix *session.Interaction) string {
	if ix == nil {
		return StyleAccentBold.Render("needs input")
	}
	switch ix.Kind {
	case session.InteractionPermission:
		s := "needs permission"
		if ix.ToolName != "" {
			s += " · " + toolDisplayName(ix.ToolName)
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
	if m.projects.showHelp && m.mode != modeProjects {
		v := tea.NewView(m.helpScreen())
		v.AltScreen = true
		return v
	}
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
	case modeProjects:
		content = m.projectsView()
	default:
		content = m.listView()
	}
	if m.embedded() {
		content = m.embedInProjects(content, m.currentFooter())
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// homeTabs renders the Sessions / History (/ Logs) tab bar, highlighting active.
// The Logs tab shows only with an embedded node (see hasLogsTab).
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

func (m model) quarantined() bool {
	return m.client != nil && m.client.Quarantined()
}

func (m model) listView() string { return m.renderList() }

// renderList draws the Home session list.
func (m model) renderList() string {
	if m.spawn.active() {
		return m.spawnView()
	}
	// The status bar shows connection and quarantine state when framed; the bare
	// splash has no bar, so it keeps them in its own header.
	bare := !m.embedded()
	title := m.homeBrand() + m.homeTabs(modeList)
	if bare && m.reconnecting {
		title += dimStyle.Render("  (reconnecting…)")
	}

	// chrome counts non-content rows: title + blank-after-title + footer + blank-before-footer.
	// +1 when the quarantine banner is present (it adds a second title row).
	chrome := 4
	if bare && m.quarantined() {
		title += "\n" + StyleErrorBold.Render("⚠ QUARANTINED") +
			dimStyle.Render("  pin this device: argus lock pin")
		chrome++
	}

	// Empty state.
	if len(m.order) == 0 {
		return m.emptyListView(title, chrome)
	}

	// Populated.
	cardW := min(m.containerWidth(), maxCardWidth)
	if cardW < 30 {
		cardW = 30
	}

	// Render cards to lines, tracking the cursor card's range so the window keeps
	// it visible. On a gateway, a host header precedes each group.
	grouped := m.grouped()
	showAgent := m.multiAgent()
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
		lines = append(lines, strings.Split(m.sessionCard(s, i == m.cursor, cardW, showAgent), "\n")...)
		if i == m.cursor {
			curStart, curEnd = start, len(lines)
		}
	}

	lines = windowSpan(lines, curStart, curEnd, max(1, m.bodyHeight()-chrome))

	block := m.center(title+"\n\n"+strings.Join(lines, "\n"), cardW)
	return m.pin(block, m.listFooter())
}

func (m model) listFooter() string {
	switch {
	case len(m.order) == 0:
		return m.footer(listKeys.TabNext, listKeys.New, listKeys.Refresh, m.listBackKey())
	case m.pendingKill && m.cursor >= 0 && m.cursor < len(m.order):
		return asstStyle.Render(killPrompt(m.sessions[m.order[m.cursor]]))
	case m.flash != "":
		return asstStyle.Render(firstLine(m.flash))
	}
	return m.footer(listKeys.Up, listKeys.Open, listKeys.Jump,
		listKeys.TabNext, listKeys.New, listKeys.Kill, listKeys.Refresh, m.listBackKey(), projectsKeys.Help)
}

// argusMark is the pre-rendered truecolor logo (gold "A" in a white ring).
//
//go:embed logo.txt
var argusMark string

// argusMonogram is the "A" mark, ringed by a rounded border, for terminals too small
// for the full logo.
const argusMonogram = ` █████╗
██╔══██╗
███████║
██╔══██║
██║  ██║
╚═╝  ╚═╝`

// argusLogo renders the logo scaled to the viewport: the full mark when it fits, the
// ringed "A" monogram on smaller screens, or a plain wordmark when tiny.
func argusLogo(width, height int) string {
	mark := strings.TrimRight(argusMark, "\n")
	if width >= lipgloss.Width(mark)+2 && height >= lipgloss.Height(mark)+12 {
		return mark
	}
	mono := lipgloss.NewStyle().Bold(true).Foreground(ColorTeamYellow).Render(argusMonogram)
	ringed := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(0, 3).
		Render(mono)
	if width >= lipgloss.Width(ringed)+2 && height >= lipgloss.Height(ringed)+10 {
		return ringed
	}
	return lipgloss.NewStyle().Bold(true).Foreground(ColorTeamYellow).Render("argus")
}

// emptyListView renders the welcome screen: the argus logo, wordmark, tagline, and a
// spawn hint, centered in the space between the tab bar and the footer.
func (m model) emptyListView(title string, chrome int) string {
	textW := max(16, min(m.bodyWidth()-2, 52))
	center := lipgloss.NewStyle().Width(textW).Align(lipgloss.Center)
	hint := dimStyle.Render("No sessions yet. Start an AI agent in a tmux pane, or press ") +
		StyleAccentBold.Render("s") + dimStyle.Render(" to spawn one right here.")
	welcome := lipgloss.JoinVertical(lipgloss.Center,
		argusLogo(m.bodyWidth(), m.bodyHeight()),
		"",
		headerStyle.Render("Argus"),
		center.Render(StyleSecondary.Render("Watch and control all your AI agents.")),
		"",
		center.Render(hint),
	)

	avail := max(1, m.bodyHeight()-chrome)
	top := max(0, (avail-lipgloss.Height(welcome))/2)
	block := strings.Repeat("\n", top) + m.center(welcome, lipgloss.Width(welcome))

	cardW := min(m.containerWidth(), maxCardWidth)
	if cardW < 30 {
		cardW = 30
	}
	return m.pin(m.center(title, cardW)+"\n\n"+block, m.listFooter())
}

// spawnView renders the "new session" flow. List steps (node, dir) render one
// choice per line, windowed with the cursor visible; text steps render a labeled
// input line. Rows are width-clamped so long lists/paths never overflow.
func (m model) spawnView() string {
	cardW := historyWidth(m)
	title := headerStyle.Render(m.withBrand("new session"))
	avail := max(1, m.bodyHeight()-4)
	var body string
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
	case spawnStepAgent:
		if m.spawn.agents == nil {
			body = StyleSecondaryBold.Render("Which agent?") + "\n\n" +
				dimStyle.Render("Detecting agents…")
			break
		}
		cards := make([]string, len(m.spawn.agents))
		for i, a := range m.spawn.agents {
			cards[i] = spawnChoiceRow(a.Name, "", i == m.spawn.cursor, cardW)
		}
		body = StyleSecondaryBold.Render("Which agent?") + "\n\n" +
			renderCardList(cards, m.spawn.cursor, max(1, avail-2))
	case spawnStepDir:
		if m.spawn.custom {
			ci := m.spawn.cwd
			ci.SetWidth(cardW - 1)
			body = StyleSecondaryBold.Render("Working directory") + "\n\n" +
				asstStyle.Render(ci.View())
			break
		}
		cards := make([]string, 0, m.spawn.dirCursorMax())
		for i, p := range m.spawn.dirs {
			cards = append(cards, spawnChoiceRow(p.Label, p.Cwd, i == m.spawn.cursor, cardW))
		}
		cards = append(cards, spawnChoiceRow("Custom path…", "", m.spawn.cursor == len(m.spawn.dirs), cardW))
		body = StyleSecondaryBold.Render("Choose a directory") + "\n\n" +
			renderCardList(cards, m.spawn.cursor, max(1, avail-2))
	case spawnStepPrompt:
		head := StyleSecondaryBold.Render("Initial prompt") + " " + dimStyle.Render("(required)")
		rows := avail - 2
		if m.spawn.fixedCwd { // the dir step was skipped, so say where it runs
			head += "\n" + dimStyle.Render("in "+truncateLeft(m.spawn.cwd.Value(), max(1, cardW-3)))
			rows--
		}
		ta := m.spawn.prompt
		ta.SetWidth(cardW)
		ta.SetHeight(max(1, rows))
		body = head + "\n\n" + ta.View()
	}
	return m.pin(m.center(title+"\n\n"+body, cardW), m.spawnFooter())
}

func (m model) spawnFooter() string {
	switch {
	case m.spawn.step == spawnStepAgent && m.spawn.agents == nil:
		return dimStyle.Render("esc cancel")
	case m.spawn.step == spawnStepDir && m.spawn.custom:
		return dimStyle.Render("type a path · enter confirm · esc cancel")
	case m.spawn.step == spawnStepPrompt:
		return dimStyle.Render("enter launch · shift+enter/^j newline · esc cancel")
	}
	return dimStyle.Render("↑/↓ move · enter select · esc cancel")
}

// spawnChoiceRow renders one selectable node/dir row: cursor marker, label, and
// an optional dimmed sub-line (e.g. project path), clamped to w.
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
	b.WriteString(headerStyle.Render(m.withBrand(s.Tmux.SessionName)) +
		dimStyle.Render(fmt.Sprintf("  [%s] %s", paneTag(s), statusWord(s))) + "\n\n")

	var body string
	switch {
	case m.termErr != nil:
		body = dimStyle.Render("terminal unavailable: " + m.termErr.Error())
	case m.term != nil:
		body = m.term.Render()
	}
	cols, visible := m.termDims()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) > visible { // keep the most recent content
		lines = lines[len(lines)-visible:]
	}
	// Lines carry SGR escapes; clip to the interior width and reset so colors
	// don't bleed.
	for i, line := range lines {
		line = truncateLine(line, cols)
		if m.termErr == nil {
			line += "\x1b[0m"
		}
		lines[i] = line
	}
	// lipgloss Width counts the border, so pass cols+2 to keep the interior at cols
	// (Width(cols) would give a cols-2 interior and wrap every row).
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Width(cols + 2).
		Render(strings.Join(lines, "\n"))
	b.WriteString(box)

	return m.pin(b.String(), m.screenFooter())
}

func (m model) screenFooter() string {
	return dimStyle.Render("keys go to the session · ") + m.footer(screenLeave)
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

// listBackKey is esc → tree, or q → quit when no tree is visible.
// homeTreeKey is the History and Logs hint for tab, shown only when the tree is.
func (m model) homeTreeKey() key.Binding {
	b := homeTree
	b.SetEnabled(m.sidebarVisible())
	return b
}

func (m model) listBackKey() key.Binding {
	if m.sidebarVisible() {
		return listKeys.Back
	}
	return listKeys.Quit
}
