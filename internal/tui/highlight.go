package tui

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/colorprofile"
)

// jsonHighlighter colorizes JSON via Chroma, picking a style for the terminal
// background and a formatter for its color depth.
// Ported from kylesnowschwartz/tail-claude json_highlight.go.
type jsonHighlighter struct {
	lexer     chroma.Lexer
	formatter chroma.Formatter
	style     *chroma.Style
}

func newJSONHighlighter(hasDark bool) *jsonHighlighter {
	styleName := "github"
	if hasDark {
		styleName = "dracula"
	}
	profile := colorprofile.Detect(os.Stderr, os.Environ())
	return &jsonHighlighter{
		lexer:     chroma.Coalesce(lexers.Get("json")),
		formatter: formatters.Get(chromaFormatter(profile)),
		style:     styles.Get(styleName),
	}
}

// highlight returns the syntax-highlighted, re-indented form of s, or ok=false
// when s is not valid JSON (callers fall back to plain/dim rendering).
func (h *jsonHighlighter) highlight(s string) (string, bool) {
	raw := []byte(s)
	if !json.Valid(raw) {
		return "", false
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return "", false
	}
	it, err := h.lexer.Tokenise(nil, buf.String())
	if err != nil {
		return "", false
	}
	var out bytes.Buffer
	if err := h.formatter.Format(&out, h.style, it); err != nil {
		return "", false
	}
	return out.String(), true
}

// chromaFormatter maps a detected color profile to a Chroma terminal formatter.
func chromaFormatter(p colorprofile.Profile) string {
	switch p {
	case colorprofile.TrueColor:
		return "terminal16m"
	case colorprofile.ANSI256:
		return "terminal256"
	case colorprofile.ANSI:
		return "terminal16"
	default:
		return "terminal"
	}
}
