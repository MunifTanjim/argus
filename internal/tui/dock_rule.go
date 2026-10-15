package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
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
