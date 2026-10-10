package tui

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

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
	h := Icon.Node.Render() + " " + StyleSecondaryBold.Render(name)
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
func interactionHint(agent string, ix *session.Interaction) string {
	if ix == nil {
		return StyleAccentBold.Render("needs input")
	}
	switch ix.Kind {
	case session.InteractionPermission:
		s := "needs permission"
		if ix.ToolName != "" {
			s += " · " + toolDisplayName(agent, ix.ToolName)
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

type homeTab int

const (
	tabSessions homeTab = iota
	tabHistory
	tabTerminals
	tabLogs
)

var homeTabLabel = [...]string{tabSessions: "Sessions", tabHistory: "History", tabTerminals: "Terminals", tabLogs: "Logs"}

// homeTabList is the Home tabs in order. Terminals hides once server.info shows
// no node with tmux; Logs shows only with an embedded node (see hasLogsTab).
func (m model) homeTabList() []homeTab {
	tabs := []homeTab{tabSessions, tabHistory}
	if m.hasTerminalsTab() {
		tabs = append(tabs, tabTerminals)
	}
	if m.hasLogsTab() {
		tabs = append(tabs, tabLogs)
	}
	return tabs
}

func (m model) hasTerminalsTab() bool {
	return len(m.nodeInfo) == 0 || len(m.terminalNodes()) > 0
}

func (m model) homeTabLabels() []string {
	tabs := m.homeTabList()
	labels := make([]string, len(tabs))
	for i, t := range tabs {
		labels[i] = homeTabLabel[t]
	}
	return labels
}

// homeTabAt is the Home tab at index i of the tab bar.
func (m model) homeTabAt(i int) homeTab {
	tabs := m.homeTabList()
	if i < 0 || i >= len(tabs) {
		return tabSessions
	}
	return tabs[i]
}

func (m model) homeTabs(active homeTab) string {
	return m.tabLine(m.homeTabLabels(), slices.Index(m.homeTabList(), active))
}

func (m model) hitHomeTabs(c *ctx, x int) { c.hitTabs(x, 0, 3, m.homeTabLabels()...) }

func (m model) quarantined() bool {
	return m.client != nil && m.client.Quarantined()
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// listBackKey is the splash hint for leaving Home: the splash hides the tree.
func (m model) listBackKey() binding {
	if m.sidebarVisible() {
		return listKeys.Back
	}
	return listKeys.Quit
}

// quitKey is the Home hint for quit, shown only when no tree offers a way out.
func (m model) quitKey() binding {
	b := listKeys.Quit
	b.SetEnabled(!m.sidebarVisible())
	return b
}

// clearFilterKey is the hint for back, shown only while a filter is on.
func clearFilterKey(back binding, on bool) binding {
	b := helpAs(back, "clear filter")
	b.SetEnabled(on)
	return b
}
