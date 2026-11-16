package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestStatusWordPrefersServerLabel(t *testing.T) {
	s := session.Session{Status: session.StatusAwaitingInput, StatusLabel: "awaiting"}
	if got := statusWord(s); got != "awaiting" {
		t.Fatalf("statusWord = %q, want awaiting", got)
	}
	// Fallback to the raw status value when the server sent no label.
	s2 := session.Session{Status: session.StatusWorking}
	if got := statusWord(s2); got != "working" {
		t.Fatalf("statusWord fallback = %q, want working", got)
	}
}

func TestCardTitledBottomLabel(t *testing.T) {
	plain := ansi.Strip(cardTitled("L", "R", []string{"body"}, 24, ColorBorder, cardRounded, "", nil))
	if strings.Contains(lastLine(plain), "codex") {
		t.Errorf("empty bottom label should leave a plain rule: %q", lastLine(plain))
	}

	labeled := ansi.Strip(cardTitled("L", "R", []string{"body"}, 24, ColorBorder, cardRounded, "codex", ColorAgentCodex))
	last := lastLine(labeled)
	if !strings.Contains(last, "codex") {
		t.Errorf("bottom label should appear on the last row: %q", last)
	}
	if !strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
		t.Errorf("bottom row should keep its corners: %q", last)
	}
	// Right-aligned: dashes fill the left, the label sits just before the corner.
	if !strings.HasSuffix(last, "codex ─╯") {
		t.Errorf("bottom label should be right-aligned: %q", last)
	}
	if !strings.HasPrefix(last, "╰──") {
		t.Errorf("bottom row should lead with a dash run before the label: %q", last)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestSessionColor(t *testing.T) {
	initTheme(true)
	awaiting := func(ix *session.Interaction) session.Session {
		return session.Session{Status: session.StatusAwaitingInput, Interaction: ix}
	}
	for _, k := range []session.InteractionKind{session.InteractionPermission, session.InteractionQuestion, session.InteractionPlan} {
		if got := sessionColor(awaiting(&session.Interaction{Kind: k})); got != ColorBlocked {
			t.Errorf("%s: color = %v, want ColorBlocked", k, got)
		}
	}
	if got := sessionColor(awaiting(&session.Interaction{Kind: session.InteractionIdle})); got != ColorAccent {
		t.Errorf("idle: color = %v, want ColorAccent", got)
	}
	if got := sessionColor(awaiting(nil)); got != ColorAccent {
		t.Errorf("nil interaction: color = %v, want ColorAccent", got)
	}
	if got := sessionColor(session.Session{Status: session.StatusWorking}); got != ColorOngoing {
		t.Errorf("working: color = %v, want ColorOngoing", got)
	}
	if ColorBlocked == ColorAccent {
		t.Fatal("ColorBlocked must differ from ColorAccent")
	}
}

func TestInteractionHintColor(t *testing.T) {
	initTheme(true)
	blockedStyle := StyleAccentBold.Foreground(ColorBlocked)
	perm := interactionHint("claude", &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"})
	if want := blockedStyle.Render("needs permission · Bash"); perm != want {
		t.Errorf("permission hint = %q, want %q", perm, want)
	}
	idle := interactionHint("claude", &session.Interaction{Kind: session.InteractionIdle})
	if want := StyleAccentBold.Render("waiting"); idle != want {
		t.Errorf("idle hint = %q, want %q", idle, want)
	}
}
