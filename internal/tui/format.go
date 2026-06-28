package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/session"
)

// shortModel turns "claude-opus-4-6" into "opus4.6".
func shortModel(m string) string {
	m = strings.TrimPrefix(m, "claude-")
	parts := strings.SplitN(m, "-", 2)
	if len(parts) != 2 {
		return m
	}
	family := parts[0]
	// Keep major-minor only, dropping any dated/build suffix.
	v := strings.SplitN(parts[1], "-", 3)
	ver := v[0]
	if len(v) >= 2 {
		ver = v[0] + "-" + v[1]
	}
	if len(family) > 1 {
		family = strings.ToUpper(string(family[0])) + family[1:]
	}
	return family + " " + strings.ReplaceAll(ver, "-", ".")
}

// modelColor returns a color based on the Claude model family.
func modelColor(model string) color.Color {
	switch {
	case strings.Contains(model, "opus"):
		return ColorModelOpus
	case strings.Contains(model, "sonnet"):
		return ColorModelSonnet
	case strings.Contains(model, "haiku"):
		return ColorModelHaiku
	default:
		return ColorTextSecondary
	}
}

// formatTokens formats a token count: 1234 -> "1.2k", 1234567 -> "1.2M".
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// formatDuration formats milliseconds: 71000 -> "1m 11s", 3500 -> "3.5s".
func formatDuration(ms int64) string {
	secs := float64(ms) / 1000
	switch {
	case secs >= 60:
		return fmt.Sprintf("%dm %ds", int(secs)/60, int(secs)%60)
	case secs >= 10:
		return fmt.Sprintf("%.0fs", secs)
	default:
		return fmt.Sprintf("%.1fs", secs)
	}
}

// contextUsageColor maps a window-usage percentage to the theme's three
// context-pressure colors (thresholds 50/80).
func contextUsageColor(pct float64) color.Color {
	switch {
	case pct >= 80:
		return ColorContextCrit
	case pct >= 50:
		return ColorContextWarn
	default:
		return ColorContextOk
	}
}

// formatContext renders the per-turn context-window indicator, colored by the
// last cycle's pressure. Shows growth across the turn's inference cycles when it
// occurred ("ctx 31% → 67% (+220k)"), else a single "ctx 67%". Returns "" when
// the chunk carries no context data.
func formatContext(c claudecode.Chunk) string {
	if !c.HasContext {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(contextUsageColor(c.ContextPct))
	if c.ContextDeltaTokens == 0 && c.ContextFirstPct == c.ContextPct {
		return st.Render(fmt.Sprintf("ctx %.0f%%", c.ContextPct))
	}
	return st.Render(fmt.Sprintf("ctx %.0f%% → %.0f%% (+%s)",
		c.ContextFirstPct, c.ContextPct, formatTokens(c.ContextDeltaTokens)))
}

// paneTag is the bracket label shown for a session: its tmux pane id when it has
// one, else the frontend word (e.g. "vscode") so paneless sessions read clearly.
func paneTag(s session.Session) string {
	if s.Tmux.PaneID != "" {
		return s.Tmux.PaneID
	}
	if s.Frontend != "" {
		return string(s.Frontend)
	}
	return "?"
}

// statusWord is the display word for a session's status: the server-rendered
// StatusLabel when present, else the raw status value as a fallback.
func statusWord(s session.Session) string {
	if s.StatusLabel != "" {
		return s.StatusLabel
	}
	return string(s.Status)
}

// statusColor maps a session status to its accent color for the list cards.
func statusColor(s session.Status) color.Color {
	switch s {
	case session.StatusWorking:
		return ColorOngoing
	case session.StatusAwaitingInput:
		return ColorAccent
	case session.StatusIdle:
		return ColorTextDim
	default: // dead / discovered
		return ColorTextMuted
	}
}

// relTime formats an RFC3339 timestamp as a compact age relative to now ("now",
// "5m", "2h", "3d"). Returns "" for an empty or unparseable timestamp.
func relTime(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

// clockTime extracts the HH:MM:SS portion of an ISO timestamp, if present.
func clockTime(ts string) string {
	if i := strings.IndexByte(ts, 'T'); i >= 0 && len(ts) >= i+9 {
		return ts[i+1 : i+9]
	}
	return ts
}

// toolColor returns the category color for a Claude Code tool by name, paralleling
// toolIcon. Unknown tools get the dim "other" color.
func toolColor(name string) color.Color {
	switch name {
	case "Read", "NotebookRead":
		return ColorToolRead
	case "Edit", "MultiEdit", "NotebookEdit":
		return ColorToolEdit
	case "Write":
		return ColorToolWrite
	case "Bash", "BashOutput", "KillShell":
		return ColorToolBash
	case "Grep":
		return ColorToolGrep
	case "Glob", "LS":
		return ColorToolGlob
	case "Task", "Agent":
		return ColorToolTask
	case "Skill":
		return ColorToolSkill
	case "WebFetch", "WebSearch":
		return ColorToolWeb
	default:
		return ColorToolOther
	}
}

// sessionCard renders one session as a bordered card for the list: a titled top
// edge with the repo as the left headline and the tmux coordinates
// (name · pane · server) grouped on the right, a metadata line
// (model · ctx% · tokens … relTime), and a task line (the awaiting-input hint or
// the current-task summary). A working session's glyph is the animated spinner.
// The unfocused card is muted to a calm baseline (neutral border, dimmed repo /
// tmux / summary) so the focused card -- a bright-cyan heavy border with
// full-strength text -- stands out. The status glyph and the awaiting-input hint
// stay colored on every card as triage signals.
func (m model) sessionCard(s session.Session, selected bool, cardW int) string {
	border, chrome := ColorBorder, cardRounded
	if selected {
		border, chrome = ColorFocus, cardHeavy
	}
	innerW := max(cardW-4, 10)

	// Status glyph keeps its color on every card (working keeps the spinner).
	glyph, gcolor := statusGlyph(s.Status), statusColor(s.Status)
	if s.Status == session.StatusWorking {
		glyph, gcolor = SpinnerFrames[m.spin%len(SpinnerFrames)], ColorOngoing
	}
	// A session whose node dropped off the gateway reads as inactive: muted glyph,
	// no spinner, regardless of its last-known status.
	if s.Offline {
		glyph, gcolor = "○", ColorBorder
	}

	// Left headline: status glyph + repo (just the glyph when there's no repo).
	titleLeft := lipgloss.NewStyle().Foreground(gcolor).Render(glyph)
	if s.Repo != "" {
		repoStyle := StyleDim
		if selected {
			repoStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorGitBranch)
		}
		titleLeft += " " + repoStyle.Render(truncate(s.Repo, 28))
	}

	// Right: tmux coordinates grouped as one contiguous span. Prefer Claude's own
	// session name (known from discovery); fall back to the tmux session name. The
	// name is omitted when both are empty (it would otherwise duplicate the pane id).
	var tmux []string
	name := s.Name
	if name == "" {
		name = s.Tmux.SessionName
	}
	if name != "" {
		tmux = append(tmux, truncate(name, 24))
	}
	if tag := paneTag(s); tag != "?" {
		tmux = append(tmux, tag)
	}
	tmux = append(tmux, string(s.Tmux.Server))
	tmuxStyle := StyleDim
	if selected {
		tmuxStyle = StyleSecondary
	}
	titleRight := tmuxStyle.Render(strings.Join(tmux, " · "))

	var task string
	switch {
	case s.Offline:
		task = StyleDim.Render("(node offline)")
	case s.Status == session.StatusAwaitingInput:
		task = interactionHint(s.Interaction) // key signal: kept colored
	case s.Summary != nil && s.Summary.Task != "":
		taskStyle := StyleDim
		if selected {
			taskStyle = StyleSecondary
		}
		task = taskStyle.Render(truncate(s.Summary.Task, innerW))
	default:
		task = StyleDim.Render("(idle)")
	}

	// In the cross-host "Needs you" section the per-node header is replaced, so the
	// node would otherwise be unknowable; surface it on the card itself.
	nodeLabel := ""
	if s.Status == session.StatusAwaitingInput && m.grouped() {
		nodeLabel = s.NodeLabel
		if nodeLabel == "" {
			nodeLabel = "local"
		}
	}

	return cardTitled(titleLeft, titleRight, []string{m.cardMeta(s, innerW, selected, nodeLabel), task}, cardW, border, chrome)
}

// cardMeta builds a session card's metadata line: model · ctx% · tokens on the left
// (each omitted when unknown) and the relative last-activity on the right. On an
// unfocused card the model badge is dimmed and a healthy ctx% is dimmed too; an
// elevated ctx% (>=50%) keeps its warning color so it triages at a glance.
func (m model) cardMeta(s session.Session, width int, selected bool, nodeLabel string) string {
	var parts []string
	if sum := s.Summary; sum != nil {
		if sum.Model != "" {
			st := StyleDim
			if selected {
				st = lipgloss.NewStyle().Foreground(modelColor(sum.Model))
			}
			parts = append(parts, st.Render(shortModel(sum.Model)))
		}
		if sum.HasContext {
			st := StyleDim
			if selected || sum.ContextPct >= 50 {
				st = lipgloss.NewStyle().Foreground(contextUsageColor(sum.ContextPct))
			}
			parts = append(parts, st.Render(fmt.Sprintf("ctx %.0f%%", sum.ContextPct)))
		}
		if sum.Tokens > 0 {
			parts = append(parts, StyleDim.Render(formatTokens(sum.Tokens)))
		}
	}
	left := strings.Join(parts, StyleDim.Render(" · "))
	if left == "" {
		left = StyleDim.Render(statusWord(s)) // no summary yet: at least show status
	}
	// Node on the right, before the time, so the cross-host "Needs you" cards
	// stay attributable to their host.
	var rights []string
	if nodeLabel != "" {
		rights = append(rights, StyleSecondary.Render(Icon.Node.Glyph+" "+nodeLabel))
	}
	if s.Summary != nil {
		rights = append(rights, StyleDim.Render(relTime(s.Summary.LastActivity)))
	}
	right := strings.Join(rights, StyleDim.Render(" · "))
	return spaceBetween(left, right, width)
}

// cardChrome is a card's box-drawing glyph set: corners, horizontal, vertical.
type cardChrome struct{ tl, tr, bl, br, h, v string }

var (
	cardRounded = cardChrome{"╭", "╮", "╰", "╯", "─", "│"} // unfocused
	cardHeavy   = cardChrome{"┏", "┓", "┗", "┛", "━", "┃"} // focused
)

// cardTitled composes a card with a titled top edge using the given chrome (the
// focused card uses cardHeavy, otherwise cardRounded). titleLeft sits after the
// lead corner; titleRight before the tail corner; a border-colored rule fills the
// gap. Body lines are framed by the vertical glyph, truncated and padded to cardW-4.
func cardTitled(titleLeft, titleRight string, body []string, cardW int, border color.Color, ch cardChrome) string {
	bs := lipgloss.NewStyle().Foreground(border)
	innerW := max(cardW-4, 10)
	lead, tail := ch.tl+ch.h+" ", " "+ch.h+ch.tr

	// Cap the title so the top edge keeps at least one rule dash and fits cardW.
	maxLeft := cardW - lipgloss.Width(lead) - lipgloss.Width(tail) - lipgloss.Width(titleRight) - 3
	if maxLeft < 1 {
		maxLeft = 1
	}
	titleLeft = truncateLine(titleLeft, maxLeft)

	dashN := cardW - lipgloss.Width(lead) - lipgloss.Width(titleLeft) - lipgloss.Width(titleRight) - lipgloss.Width(tail) - 2
	if dashN < 1 {
		dashN = 1
	}
	top := bs.Render(lead) + titleLeft + " " + bs.Render(strings.Repeat(ch.h, dashN)) + " " +
		titleRight + bs.Render(tail)

	rows := []string{top}
	for _, line := range body {
		content := truncateLine(line, innerW)
		pad := max(0, innerW-lipgloss.Width(content))
		rows = append(rows, bs.Render(ch.v+" ")+content+strings.Repeat(" ", pad)+bs.Render(" "+ch.v))
	}
	rows = append(rows, bs.Render(ch.bl+strings.Repeat(ch.h, cardW-2)+ch.br))
	return strings.Join(rows, "\n")
}

// accentBlock prefixes every line of content with a category-colored gutter,
// giving each detail item a left accent rule. The bar glyph is the caller's
// choice (a thin GlyphAccentBar normally, a heavy GlyphAccentBarFocused when the
// item is focused).
func accentBlock(content string, c color.Color, bar string) string {
	pre := lipgloss.NewStyle().Foreground(c).Render(bar) + " "
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = pre + l
	}
	return strings.Join(lines, "\n")
}

// itemAccentColor returns the accent-rule color for a detail item: tools use their
// category color; thinking and output are neutral grays, subagents use the task
// color. No item uses ColorAccent — that is reserved for the focus highlight, so a
// focused item's accent bar always differs from its own color.
func itemAccentColor(it claudecode.Item) color.Color {
	switch it.Kind {
	case claudecode.ItemThinking:
		return ColorTextDim
	case claudecode.ItemText:
		return ColorTextSecondary
	case claudecode.ItemSubagent:
		return ColorToolTask
	default:
		return toolColor(it.ToolName)
	}
}
