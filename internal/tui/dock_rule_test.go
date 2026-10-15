package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
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

func withSession(m model, f func(*session.Session)) model {
	s := m.sessions["s1"]
	f(&s)
	m.sessions["s1"] = s
	return m
}

// lineWith is the first line of the stripped view holding s, trailing spaces
// trimmed.
func lineWith(view, s string) string {
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(l, s) {
			return strings.TrimRight(l, " ")
		}
	}
	return ""
}

func TestTopRuleShowsActivity(t *testing.T) {
	old := clockNow
	clockNow = func() time.Time { return time.Date(2026, 10, 15, 9, 0, 12, 0, time.UTC) }
	t.Cleanup(func() { clockNow = old })

	m := withSession(sessionModel(nil), func(s *session.Session) {
		s.Status = session.StatusWorking
		s.StatusSince = "2026-10-15T09:00:00Z"
	})
	if l := lineWith(paneView(m), "Working…"); !strings.HasSuffix(l, " Working… 12s ──") || strings.HasPrefix(strings.TrimLeft(l, " │"), "── "+spinnerFrame(m)) {
		t.Fatalf("top rule should end with the working time: %q", l)
	}
	m = withSession(m, func(s *session.Session) { s.StatusSince = "" })
	if out := ansi.Strip(paneView(m)); !strings.Contains(out, "Working…") || strings.Contains(out, "Working… 1") {
		t.Fatalf("no status_since: Working… without a time:\n%s", out)
	}
	m = withSession(m, func(s *session.Session) { s.Status = session.StatusStarting })
	if out := ansi.Strip(paneView(m)); !strings.Contains(out, "Starting…") {
		t.Fatalf("starting label missing:\n%s", out)
	}
	m = withInteraction(withSession(m, func(s *session.Session) { s.Status = session.StatusAwaitingInput }), dockPermission)
	if l := lineWith(paneView(m), "Permission"); !strings.HasSuffix(l, "─ Permission ──") {
		t.Fatalf("top rule should end with the prompt kind: %q", l)
	}
}

func TestBottomRuleShowsModelAndEffort(t *testing.T) {
	m := withSession(sessionModel(nil), func(s *session.Session) {
		s.Summary = &session.Summary{ModelName: "Opus 5.5", Effort: "high"}
	})
	m.width = 120
	if out := ansi.Strip(paneView(m)); !strings.Contains(out, " Opus 5.5 · high ──") {
		t.Fatalf("bottom rule should show model · effort:\n%s", out)
	}
	m = withSession(m, func(s *session.Session) { s.Summary.Effort = "" })
	out := ansi.Strip(paneView(m))
	var rule string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " Opus 5.5 ──") {
			rule = line
		}
	}
	if rule == "" || strings.Contains(rule, "·") {
		t.Fatalf("no effort: bottom rule shows the model alone:\n%s", out)
	}
}

func TestRegionSpinsOnlyWhileWorking(t *testing.T) {
	m := sessionModel(nil)
	c := &ctx{m: &m}
	if m.dock.spins(c) {
		t.Fatal("an idle session should not spin")
	}
	m = withSession(m, func(s *session.Session) { s.Status = session.StatusWorking })
	c = &ctx{m: &m}
	if !m.dock.spins(c) {
		t.Fatal("a working session should spin")
	}
}
