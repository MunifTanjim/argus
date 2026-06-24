package tui

import (
	"strings"
	"testing"
)

func TestJSONHighlightValid(t *testing.T) {
	h := newJSONHighlighter(true)
	out, ok := h.highlight(`{"command":"ls -la","count":3}`)
	if !ok {
		t.Fatal("expected valid JSON to highlight")
	}
	// The content survives highlighting (ANSI codes may wrap the tokens).
	for _, want := range []string{"command", "ls -la", "count"} {
		if !strings.Contains(out, want) {
			t.Errorf("highlighted output missing %q:\n%s", want, out)
		}
	}
	// Re-indented to multi-line.
	if !strings.Contains(out, "\n") {
		t.Errorf("expected pretty-printed multiline output, got:\n%s", out)
	}
}

func TestJSONHighlightInvalid(t *testing.T) {
	h := newJSONHighlighter(false)
	if _, ok := h.highlight("not json at all"); ok {
		t.Error("expected ok=false for non-JSON input")
	}
	if _, ok := h.highlight(""); ok {
		t.Error("expected ok=false for empty input")
	}
}
