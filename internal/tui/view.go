package tui

import (
	_ "embed"
	"fmt"
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

type homeTab int

const (
	tabSessions homeTab = iota
	tabHistory
	tabLogs
)

// homeTabs renders the Sessions / History (/ Logs) tab bar, highlighting active.
// The Logs tab shows only with an embedded node (see hasLogsTab).
func (m model) homeTabs(active homeTab) string {
	sess, hist, logs := StyleDim, StyleDim, StyleDim
	switch active {
	case tabSessions:
		sess = StyleAccentBold
	case tabHistory:
		hist = StyleAccentBold
	case tabLogs:
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

// treeKey is the History and Logs hint for focus left, shown only when the
// tree is.
func (m model) treeKey() binding {
	b := paneKeys.Left
	b.SetEnabled(m.sidebarVisible())
	return b
}

func (m model) listBackKey() binding {
	if m.sidebarVisible() {
		return listKeys.Back
	}
	return listKeys.Quit
}
