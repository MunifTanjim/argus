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

// codeHighlighter colorizes source of a fixed language via Chroma.
// Ported from kylesnowschwartz/tail-claude json_highlight.go.
type codeHighlighter struct {
	lexer     chroma.Lexer
	formatter chroma.Formatter
	style     *chroma.Style
}

func newCodeHighlighter(hasDark bool, language string) *codeHighlighter {
	styleName := "gruvbox-light"
	if hasDark {
		styleName = "gruvbox"
	}
	profile := colorprofile.Detect(os.Stderr, os.Environ())
	return &codeHighlighter{
		lexer:     chroma.Coalesce(lexers.Get(language)),
		formatter: formatters.Get(chromaFormatter(profile)),
		style:     styles.Get(styleName),
	}
}

// highlight colorizes s as the highlighter's language. ok is false on a tokenise
// or format error (callers fall back to plain rendering).
func (h *codeHighlighter) highlight(s string) (string, bool) {
	it, err := h.lexer.Tokenise(nil, s)
	if err != nil {
		return "", false
	}
	var out bytes.Buffer
	if err := h.formatter.Format(&out, h.style, it); err != nil {
		return "", false
	}
	return out.String(), true
}

// highlightJSON re-indents s before colorizing, or ok=false when s is not valid
// JSON (callers fall back to plain/dim rendering).
func (h *codeHighlighter) highlightJSON(s string) (string, bool) {
	raw := []byte(s)
	if !json.Valid(raw) {
		return "", false
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return "", false
	}
	return h.highlight(buf.String())
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
