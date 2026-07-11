package tui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestPinFooterWrapsLongFooterWithinViewport(t *testing.T) {
	const width, height = 24, 8
	body := "transcript"
	footer := "⚠ 2 item(s) hold secrets that can't be removed — they WILL remain in the export. y save anyway · any cancel"

	out := pinFooter(body, footer, width, height)
	lines := strings.Split(out, "\n")

	// Nothing clips: the whole thing fits the viewport height exactly.
	if len(lines) != height {
		t.Fatalf("pinned output = %d lines, want %d (footer must not overflow the viewport)", len(lines), height)
	}
	// Every line stays within the terminal width (footer wrapped, not truncated).
	for i, l := range lines {
		if w := lipgloss.Width(l); w > width {
			t.Fatalf("line %d width %d exceeds terminal width %d: %q", i, w, width, l)
		}
	}
	// All of the footer's words survive across the wrapped lines.
	joined := lipgloss.NewStyle().Render(strings.Join(lines, " "))
	for _, word := range []string{"secrets", "remain", "cancel"} {
		if !strings.Contains(joined, word) {
			t.Fatalf("wrapped footer dropped %q:\n%s", word, out)
		}
	}
}

func TestPinFooterShortFooterUnchanged(t *testing.T) {
	const width, height = 40, 6
	out := pinFooter("body", "esc back", width, height)
	lines := strings.Split(out, "\n")
	if len(lines) != height {
		t.Fatalf("short footer: got %d lines, want %d", len(lines), height)
	}
	if !strings.Contains(lines[height-1], "esc back") {
		t.Fatalf("short footer should sit on the last row, got %q", lines[height-1])
	}
}
