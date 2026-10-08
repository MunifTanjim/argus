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
