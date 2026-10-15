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

// dockRule draws a w-wide "── left ─── right ──". The left label truncates
// first; the right one is dropped only when it cannot fit.
func dockRule(left, right string, w int, c color.Color) string {
	dash := func(n int) string {
		return lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("─", max(0, n)))
	}
	if w < 2*dockRuleEdge {
		return dash(w)
	}
	pad := func(s string) string {
		if s == "" {
			return ""
		}
		return " " + s + " "
	}
	r := pad(right)
	if 2*dockRuleEdge+lipgloss.Width(r) > w {
		r = ""
	}
	room := w - 2*dockRuleEdge - lipgloss.Width(r)
	l := pad(left)
	if lipgloss.Width(l) > room {
		l = ""
		if room > 3 {
			l = " " + truncateLine(left, room-3) + "… "
		}
	}
	fill := w - 2*dockRuleEdge - lipgloss.Width(l) - lipgloss.Width(r)
	return dash(dockRuleEdge) + l + dash(fill) + r + dash(dockRuleEdge)
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
func (m model) dockStatusLabel(s session.Session) string {
	accent := lipgloss.NewStyle().Foreground(ColorAccent)
	if ix := s.Interaction; ix != nil && ix.Kind != session.InteractionIdle {
		return StyleAccentBold.Render(interactionKindLabel(ix.Kind))
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
	}
	return "Input"
}

// modelEffortLabel is "<model> · <effort>", each part left out when unknown.
func modelEffortLabel(sum *session.Summary) string {
	if sum == nil {
		return ""
	}
	var parts []string
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
	return strings.Join(parts, dimStyle.Render(" · "))
}
