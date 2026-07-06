package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
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

func TestAccentBlockPrefixesEveryLine(t *testing.T) {
	out := accentBlock("alpha\nbeta\ngamma", ColorToolBash, GlyphAccentBar)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, l := range lines {
		if !strings.Contains(l, "┃") {
			t.Errorf("line %d missing accent bar: %q", i, l)
		}
	}
	if !strings.Contains(out, "beta") {
		t.Errorf("content lost: %q", out)
	}
}

func TestItemAccentColor(t *testing.T) {
	tool := transcript.Item{Kind: transcript.ItemTool, ToolName: "Bash"}
	if itemAccentColor(tool) != ColorToolBash {
		t.Error("tool item should use its tool color")
	}
	think := transcript.Item{Kind: transcript.ItemThinking}
	if itemAccentColor(think) != ColorTextDim {
		t.Error("thinking item should use dim")
	}
	out := transcript.Item{Kind: transcript.ItemText}
	if itemAccentColor(out) != ColorTextSecondary {
		t.Error("output item should use secondary (accent is reserved for focus)")
	}
	if itemAccentColor(out) == ColorAccent {
		t.Error("no item color should equal the focus accent")
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
