package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestListViewEmptyStateIsFriendly(t *testing.T) {
	m := testModel() // no sessions: m.order is empty
	// Footers render via the help bubble (per-token styled); strip ANSI so the
	// assertions see the plain text.
	out := ansi.Strip(m.listView())

	for _, want := range []string{
		"your AI coding sessions, one place", // tagline
		"welcome",                            // greeting
		"n new · r refresh · q quit",         // trimmed footer
	} {
		if !strings.Contains(out, want) {
			t.Errorf("empty welcome should contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "No sessions discovered yet.") {
		t.Errorf("old spartan copy should be gone:\n%s", out)
	}
}

// Both home tabs render their labels on the Sessions list and the History view.
func TestHomeTabsRendered(t *testing.T) {
	m := testModel()
	out := ansi.Strip(m.listView())
	for _, want := range []string{"Sessions", "History"} {
		if !strings.Contains(out, want) {
			t.Errorf("Sessions list should show the %q tab:\n%s", want, out)
		}
	}
	m.mode = modeHistoryProjects
	hout := ansi.Strip(m.historyProjectsView())
	for _, want := range []string{"Sessions", "History"} {
		if !strings.Contains(hout, want) {
			t.Errorf("History view should show the %q tab:\n%s", want, hout)
		}
	}
}

func TestListViewPopulatedHidesTagline(t *testing.T) {
	m := testModel()
	m.sessions = map[string]session.Session{
		"s1": {ID: "s1", Status: session.StatusWorking, Tmux: session.TmuxLocation{
			SessionName: "work", PaneID: "%1", CurrentPath: "/tmp",
		}},
	}
	m.order = []string{"s1"}
	out := m.listView()

	if !strings.Contains(out, "work") {
		t.Errorf("populated list should show the session row:\n%s", out)
	}
	if strings.Contains(out, "your AI coding sessions, one place") {
		t.Errorf("tagline is empty-state only, should not show when populated:\n%s", out)
	}
}

func TestSessionCardShowsRichMetadata(t *testing.T) {
	m := testModel()
	s := session.Session{
		ID: "s1", Status: session.StatusIdle, Repo: "argus",
		Tmux: session.TmuxLocation{SessionName: "api-refactor", PaneID: "%3", Server: session.TmuxServerDefault},
		Summary: &session.Summary{
			Model: "claude-opus-4-8", HasContext: true, ContextPct: 42, Tokens: 128000,
			Task: "Revamp the session list view", LastActivity: time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
		},
	}
	out := m.sessionCard(s, false, 78)

	for _, want := range []string{
		"╭", "╰", // card border
		"api-refactor",
		"argus",    // repo
		"Opus 4.8", // short model
		"ctx 42%",
		"Revamp the session list view", // task
		"2h",                           // relative last-activity
		"%3",                           // pane
	} {
		if !strings.Contains(out, want) {
			t.Errorf("card should contain %q:\n%s", want, out)
		}
	}
	// The tmux fields render as one contiguous group on the right (single styled
	// span, so no ANSI breaks the run).
	if !strings.Contains(out, "api-refactor · %3 · default") {
		t.Errorf("tmux fields should be grouped (name · pane · server):\n%s", out)
	}
}

// The focused card uses a heavy border; an unfocused one uses the rounded border.
// This color-independent cue keeps focus visible even on an awaiting-input card
// whose border color is already the accent.
func TestSessionCardFocusUsesHeavyBorder(t *testing.T) {
	m := testModel()
	s := session.Session{
		ID:   "s1",
		Tmux: session.TmuxLocation{SessionName: "api-refactor", PaneID: "%3"},
	}
	focused := m.sessionCard(s, true, 78)
	for _, want := range []string{"┏", "┗"} {
		if !strings.Contains(focused, want) {
			t.Errorf("focused card should use a heavy border (%q):\n%s", want, focused)
		}
	}
	if strings.Contains(focused, "╭") || strings.Contains(focused, "╰") {
		t.Errorf("focused card should not use rounded corners:\n%s", focused)
	}
	unfocused := m.sessionCard(s, false, 78)
	if !strings.Contains(unfocused, "╭") || strings.Contains(unfocused, "┏") {
		t.Errorf("unfocused card should keep the rounded border:\n%s", unfocused)
	}
}

func TestSessionCardAwaitingShowsHint(t *testing.T) {
	m := testModel()
	s := session.Session{
		ID: "s1", Status: session.StatusAwaitingInput,
		Tmux:        session.TmuxLocation{SessionName: "bugfix", PaneID: "%5"},
		Interaction: &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"},
	}
	out := m.sessionCard(s, true, 78)
	if !strings.Contains(out, "needs permission") || !strings.Contains(out, "Bash") {
		t.Errorf("awaiting card should show the interaction hint:\n%s", out)
	}
}

func TestRelTime(t *testing.T) {
	now := time.Now()
	cases := map[string]string{
		now.Add(-30 * time.Second).Format(time.RFC3339): "now",
		now.Add(-90 * time.Second).Format(time.RFC3339): "1m",
		now.Add(-2 * time.Hour).Format(time.RFC3339):    "2h",
		now.Add(-49 * time.Hour).Format(time.RFC3339):   "2d",
		"":           "",
		"not-a-time": "",
	}
	for in, want := range cases {
		if got := relTime(in); got != want {
			t.Errorf("relTime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusColorDistinguishesStates(t *testing.T) {
	// Working and awaiting must differ from each other and from idle/dead so the
	// card borders read at a glance.
	working := statusColor(session.StatusWorking)
	awaiting := statusColor(session.StatusAwaitingInput)
	idle := statusColor(session.StatusIdle)
	if working == awaiting || working == idle || awaiting == idle {
		t.Errorf("statusColor should differ across working/awaiting/idle: %v/%v/%v", working, awaiting, idle)
	}
}

// The card prefers Claude's own session name (from discovery), falling back to the
// tmux session name when absent.
func TestSessionCardPrefersClaudeName(t *testing.T) {
	m := testModel()
	base := session.Session{
		ID:   "s1",
		Tmux: session.TmuxLocation{SessionName: "tmux-name", PaneID: "%3", Server: session.TmuxServerDefault},
	}
	withName := base
	withName.Name = "claude-name"
	if out := m.sessionCard(withName, false, 78); !strings.Contains(out, "claude-name") {
		t.Errorf("card should show Claude's name:\n%s", out)
	}
	if out := m.sessionCard(base, false, 78); !strings.Contains(out, "tmux-name") {
		t.Errorf("card should fall back to the tmux name:\n%s", out)
	}
}
