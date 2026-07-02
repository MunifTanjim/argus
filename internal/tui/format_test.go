package tui

import (
	"strings"
	"testing"

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
