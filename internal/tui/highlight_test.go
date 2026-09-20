package tui

import (
	"strings"
	"testing"
)

func TestJSONHighlightValid(t *testing.T) {
	h := newCodeHighlighter(true, "json")
	out, ok := h.highlightJSON(`{"command":"ls -la","count":3}`)
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
	h := newCodeHighlighter(false, "json")
	if _, ok := h.highlightJSON("not json at all"); ok {
		t.Error("expected ok=false for non-JSON input")
	}
	if _, ok := h.highlightJSON(""); ok {
		t.Error("expected ok=false for empty input")
	}
}

func TestJavaScriptHighlight(t *testing.T) {
	h := newCodeHighlighter(true, "javascript")
	out, ok := h.highlight("const x = 2 + 2;")
	if !ok {
		t.Fatal("expected javascript to highlight")
	}
	for _, want := range []string{"const", "x"} {
		if !strings.Contains(out, want) {
			t.Errorf("highlighted output missing %q:\n%s", want, out)
		}
	}
}
