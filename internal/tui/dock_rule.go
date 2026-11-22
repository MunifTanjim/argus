package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// dockRuleEdge is the run of dashes before the left label and after the right.
const dockRuleEdge = 2

// dockRule draws a w-wide "── left ─── right ──". The right label is dropped
// when it cannot fit; the left one is dropped, never truncated, when it cannot
// fit whole.
func dockRule(left, right string, w int, c color.Color) string {
	dash := func(n int) string {
		return lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("─", max(0, n)))
	}
	if w < 2*dockRuleEdge {
		return dash(w)
	}
	if !dockRuleFits("", right, w) {
		right = ""
	}
	if !dockRuleFits(left, right, w) {
		left = ""
	}
	l, r := padLabel(left), padLabel(right)
	fill := w - 2*dockRuleEdge - lipgloss.Width(l) - lipgloss.Width(r)
	return dash(dockRuleEdge) + l + dash(fill) + r + dash(dockRuleEdge)
}

// dockRuleFits reports whether dockRule draws both labels whole in w cells.
func dockRuleFits(left, right string, w int) bool {
	return 2*dockRuleEdge+lipgloss.Width(padLabel(left))+lipgloss.Width(padLabel(right)) <= w
}

// padLabel sets a rule label off from its dashes by a space each side.
func padLabel(s string) string {
	if s == "" {
		return ""
	}
	return " " + s + " "
}

// formatElapsed: "12s", "3m 04s", "1h 02m".
func formatElapsed(d time.Duration) string {
	s := max(0, int(d/time.Second))
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh %02dm", s/3600, s/60%60)
	}
}

// clockNow is the TUI's clock for running timers. A var so tests can fix it.
var clockNow = time.Now

// dockStatusLabel is the top rule's label: the pending prompt's kind, or what
// the session is doing; "" while it waits for a prompt.
// dockTopRule is the dock's top rule, labeled with the session status.
func (m model) dockTopRule(w int, c color.Color) string {
	return dockRule("", m.dockStatusLabel(m.sessions[m.liveSessionID()]), w, c)
}

func (m model) dockStatusLabel(s session.Session) string {
	accent := lipgloss.NewStyle().Foreground(ColorAccent)
	if ix := s.Interaction; ix != nil && ix.Kind != session.InteractionIdle {
		return StyleBlockedBold.Render(interactionKindLabel(ix.Kind))
	}
	switch s.Status {
	case session.StatusWorking:
		label := spinnerFrame(m) + " Working…"
		if t, err := time.Parse(time.RFC3339, s.StatusSince); err == nil {
			label += " " + formatElapsed(clockNow().Sub(t))
		}
		return accent.Render(label)
	case session.StatusStarting:
		return dimStyle.Render("Starting…")
	}
	return ""
}

// interactionKindLabel names a pending prompt on the top rule.
func interactionKindLabel(k session.InteractionKind) string {
	switch k {
	case session.InteractionPermission:
		return "Permission"
	case session.InteractionQuestion:
		return "Question"
	case session.InteractionPlan:
		return "Plan"
	case session.InteractionSecret:
		return "Secret"
	}
	return "Input"
}

// sessionInfo is the bottom rule's right label as parts: model, effort, usage,
// cost, each left out when unknown. The first base parts are the model and
// effort.
func sessionInfo(sum *session.Summary) (parts []string, base int) {
	if sum == nil {
		return nil, 0
	}
	if sum.ModelName != "" {
		st := dimStyle
		if sum.ModelColor != "" {
			st = lipgloss.NewStyle().Foreground(modelColorOf(sum.ModelColor))
		}
		parts = append(parts, st.Render(sum.ModelName))
	}
	if sum.Effort != "" {
		parts = append(parts, dimStyle.Render(sum.Effort))
	}
	base = len(parts)
	if u := usageLabel(sum); u != "" {
		parts = append(parts, u)
	}
	if sum.HasCost {
		parts = append(parts, dimStyle.Render(fmt.Sprintf("$%.2f", sum.CostUSD)))
	}
	return parts, base
}

// usageLabel is the context window's fill as "84.0k (42%)", in the context's
// color.
func usageLabel(sum *session.Summary) string {
	switch {
	case !sum.HasContext && sum.Tokens == 0:
		return ""
	case !sum.HasContext:
		return dimStyle.Render(formatTokens(sum.Tokens))
	}
	label := formatPct(sum.ContextPct)
	if sum.Tokens > 0 {
		label = formatTokens(sum.Tokens) + " (" + label + ")"
	}
	return lipgloss.NewStyle().Foreground(contextUsageColor(sum.ContextPct)).Render(label)
}
