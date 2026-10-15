package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDockRuleIsAlwaysItsWidth(t *testing.T) {
	for _, w := range []int{0, 1, 3, 4, 10, 20, 37, 120} {
		for _, labels := range [][2]string{{"", ""}, {"✻ Working… 12s", ""}, {"", "Opus 5.5 · high"}, {"✻ Working… 12s", "Opus 5.5 · high"}} {
			got := dockRule(labels[0], labels[1], w, ColorBorder)
			if lipgloss.Width(got) != w || strings.Contains(got, "\n") {
				t.Errorf("w=%d labels=%q: width %d, %q", w, labels, lipgloss.Width(got), ansi.Strip(got))
			}
		}
	}
}

func TestDockRuleLabels(t *testing.T) {
	got := ansi.Strip(dockRule("Working… 12s", "Opus 5.5 · high", 60, ColorBorder))
	if !strings.HasPrefix(got, "── Working… 12s ─") || !strings.HasSuffix(got, "─ Opus 5.5 · high ──") {
		t.Fatalf("labels not set into the rule: %q", got)
	}
}

func TestDockRuleTruncatesLeftBeforeDroppingRight(t *testing.T) {
	got := ansi.Strip(dockRule("Working on a very long label", "Opus 5.5", 24, ColorBorder))
	if !strings.Contains(got, "Opus 5.5") || strings.Contains(got, "very long label") {
		t.Fatalf("left should truncate first: %q", got)
	}
	got = ansi.Strip(dockRule("Working", "Opus 5.5 · high", 12, ColorBorder))
	if strings.Contains(got, "Opus") {
		t.Fatalf("a right label that cannot fit should be dropped: %q", got)
	}
}

func TestFormatElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:                              "0s",
		12 * time.Second:                          "12s",
		3*time.Minute + 4*time.Second:             "3m 04s",
		time.Hour + 2*time.Minute + 9*time.Second: "1h 02m",
	} {
		if got := formatElapsed(d); got != want {
			t.Errorf("formatElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}
