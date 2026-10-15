package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// A line selection copies a card's source lines. The card's renderer splits
// its lines into spans: chrome it draws (headers, labels, rules, hints) copies
// as shown, a span standing for a whole source (a tool's input) copies it
// once, and a span of text copies the source lines its words line up with.

type spanKind uint8

const (
	spanText   spanKind = iota // the source lines its words line up with
	spanChrome                 // the lines as shown, not in the source
	spanWhole                  // the whole source, once
	spanTool                   // a tool's body, split by expand
)

// cardSpan is a run of a card's rendered lines and what they copy.
type cardSpan struct {
	kind   spanKind
	lines  int
	src    string
	expand func() []cardSpan // spanTool's spans, computed only for a copy
}

// cardSource is what a card copies: text for a character selection, spans for
// a line selection (nil, or nil spans: all its lines line up with text). spans
// renders the card, so only a line selection calls it.
type cardSource struct {
	text  string
	spans func() []cardSpan
}

func linesIn(block string) int { return strings.Count(block, "\n") + 1 }

func chromeSpans(n int) []cardSpan { return []cardSpan{{kind: spanChrome, lines: n}} }

func textSpan(block, src string) cardSpan {
	return cardSpan{kind: spanText, lines: linesIn(block), src: src}
}

func wholeSpan(n int, src string) cardSpan { return cardSpan{kind: spanWhole, lines: n, src: src} }

// clipSpans keeps the spans of the first n lines.
func clipSpans(spans []cardSpan, n int) []cardSpan {
	var out []cardSpan
	for _, sp := range spans {
		if n <= 0 {
			break
		}
		sp.lines = min(sp.lines, n)
		n -= sp.lines
		out = append(out, sp)
	}
	return out
}

// resolveSpans expands tool spans, fitting each expansion to its lines.
func resolveSpans(spans []cardSpan) []cardSpan {
	var out []cardSpan
	for _, sp := range spans {
		if sp.kind != spanTool {
			out = append(out, sp)
			continue
		}
		sub := clipSpans(sp.expand(), sp.lines)
		n := 0
		for _, s := range sub {
			n += s.lines
		}
		if n < sp.lines {
			sub = append(sub, cardSpan{kind: spanChrome, lines: sp.lines - n})
		}
		out = append(out, sub...)
	}
	return out
}

// resultProbes stand in for a tool's result to find where a renderer draws it.
var resultProbes = [2]string{"probe-a", "probe-b"}

// toolSpan is a tool body's span: block is the body as drawn at width (wrap
// when the body is hard-wrapped there).
func (m tview) toolSpan(e transcript.Entry, block string, width int, wrap bool) cardSpan {
	return cardSpan{kind: spanTool, lines: linesIn(block), expand: func() []cardSpan {
		return m.toolBodySpans(e, width, wrap)
	}}
}

// toolBodySpans splits a tool body into its input, the chrome above the
// result, and the result. The body is drawn twice more with two different
// stand-in results: the first line where the two differ starts the result, and
// the rules, blank lines and label just above it are chrome. A body that draws
// no result is all input.
func (m tview) toolBodySpans(e transcript.Entry, width int, wrap bool) []cardSpan {
	full, fetched := m.filledTool(e)
	if !fetched {
		return nil // a placeholder: chrome
	}
	all := []cardSpan{wholeSpan(1<<30, full.ToolInput)}
	if full.Result == "" {
		return all
	}
	var renders [2][]string
	for i, probe := range resultProbes {
		p := full
		p.Result = probe
		body := m.c.m.renderToolBody(m.agent(), p, width)
		if wrap {
			body = hardWrap(body, width)
		}
		renders[i] = strings.Split(body, "\n")
	}
	lines := renders[0]
	r := 0
	for r < len(lines) && r < len(renders[1]) && lines[r] == renders[1][r] {
		r++
	}
	if r == len(lines) && r == len(renders[1]) {
		return all
	}
	in := r
	for in > 0 && isSectionChrome(ansi.Strip(lines[in-1]), resultLabelText(full)) {
		in--
	}
	var out []cardSpan
	if in > 0 {
		out = append(out, wholeSpan(in, full.ToolInput))
	}
	if r > in {
		out = append(out, chromeSpans(r-in)...)
	}
	return append(out, cardSpan{kind: spanText, lines: 1 << 30, src: full.Result})
}

// isSectionChrome reports whether a drawn line is a blank, a rule, or the
// result label.
func isSectionChrome(line, label string) bool {
	line = strings.TrimSpace(line)
	return line == "" || line == label || strings.Trim(line, GlyphHRule) == ""
}

// lineText is one card's part of a line selection: lines [p0, p1] of the card
// at lines [l0, l1). Each selected span adds its copy in order, lines apart.
func (s textSurface) lineText(p0, p1, l0, l1 int, src cardSource) string {
	var spans []cardSpan
	if src.spans != nil {
		spans = src.spans()
	}
	total := 0
	for _, sp := range spans {
		total += sp.lines
	}
	if spans == nil || total > l1-l0 {
		spans = []cardSpan{{kind: spanText, lines: l1 - l0, src: src.text}}
	} else if total < l1-l0 {
		spans = append(spans[:len(spans):len(spans)], cardSpan{kind: spanChrome, lines: l1 - l0 - total})
	}
	var parts []string
	lastWhole := ""
	line := l0
	for _, sp := range spans {
		from, to := max(line, p0), min(line+sp.lines, p1+1)
		if from < to {
			switch sp.kind {
			case spanChrome:
				for l := from; l < to; l++ {
					if t := s.visible(l); !isRule(t) {
						parts = append(parts, t)
					}
				}
			case spanWhole:
				switch {
				case sp.src == "":
					parts = append(parts, s.plain(from, to))
				case sp.src != lastWhole:
					parts = append(parts, strings.TrimRight(sp.src, "\n"))
					lastWhole = sp.src
				}
			default:
				parts = append(parts, s.alignedText(line, line+sp.lines, from, to, sp.src))
			}
		}
		line += sp.lines
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// isRule reports a blank or separator line, which a copy leaves out.
func isRule(t string) bool {
	return strings.Trim(t, " "+GlyphHRule) == ""
}

// visible is a line's text as shown, trailing spaces trimmed.
func (s textSurface) visible(line int) string {
	return strings.TrimRight(strings.Join(s.cells(line), ""), " ")
}

// plain is lines [from, to) as shown, leading blank lines dropped.
func (s textSurface) plain(from, to int) string {
	var lines []string
	for l := from; l < to; l++ {
		t := s.visible(l)
		if len(lines) == 0 && strings.TrimSpace(t) == "" {
			continue // padding above a card's text
		}
		lines = append(lines, t)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n ")
}

// alignedText copies lines [from, to) of a text span at lines [l0, l1): the
// whole source lines from the first to the last word matched on them, with
// the selected lines above or below every match as shown. With no match it is
// the lines as shown.
func (s textSurface) alignedText(l0, l1, from, to int, src string) string {
	if src == "" {
		return s.plain(from, to)
	}
	matches := s.alignLines(l0, l1, src)
	first, last := -1, -1
	for l := from; l < to; l++ {
		if matches[l-l0].ok {
			if first < 0 {
				first = l
			}
			last = l
		}
	}
	if first < 0 {
		return s.plain(from, to)
	}
	start, end := lineBounds(src, matches[first-l0].s0, matches[last-l0].s1)
	parts := []string{s.plain(from, first), src[start:end], s.plain(last+1, to)}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// lineMatch is the source bytes [s0, s1) a rendered line's words matched.
type lineMatch struct {
	s0, s1 int
	ok     bool
}

// alignLines matches the words of lines [l0, l1) in order through src, a
// heuristic, not a general aligner. Words are runs of letters and digits, so
// markup and punctuation around them do not matter. A line is anchored on one
// word: any of its words, at its first match from the current position to the
// end of the next three non-blank source lines; the anchor matching the most
// words after it wins, then the earliest. The line's first word may match
// inside a source word (a hard wrap splits words); any other needs a word
// boundary before it. Words after the anchor match in order, each within
// maxWordSkip of the previous match.
func (s textSurface) alignLines(l0, l1 int, src string) []lineMatch {
	out := make([]lineMatch, l1-l0)
	cur := 0
	for l := l0; l < l1; l++ {
		words := lineRuns(s.cells(l))
		if len(words) == 0 {
			continue
		}
		limit := windowEnd(src, cur, 3)
		var best lineMatch
		bestN := 0
		for k, w := range words {
			at := findWord(src, cur, limit, w, k == 0)
			if at < 0 {
				continue
			}
			n, end := 1, at+len(w)
			for _, next := range words[k+1:] {
				j := findWord(src, end, min(len(src), end+maxWordSkip+len(next)), next, false)
				if j >= 0 {
					n, end = n+1, j+len(next)
				}
			}
			if n > bestN || n == bestN && at < best.s0 {
				best, bestN = lineMatch{at, end, true}, n
			}
			if k == 0 && n == len(words) {
				break
			}
		}
		if best.ok {
			out[l-l0] = best
			cur = best.s1
		}
	}
	return out
}

// findWord is the first match of w in src[from:limit], -1 if none; a match
// needs a word boundary before it unless inside is set.
func findWord(src string, from, limit int, w string, inside bool) int {
	for pos := from; pos < limit; {
		j := strings.Index(src[pos:limit], w)
		if j < 0 {
			return -1
		}
		at := pos + j
		if inside || alnumBoundary(src, at) {
			return at
		}
		pos = at + 1
	}
	return -1
}

// Unlike rejoin's isWordChar, '_' is not a word character here, so a word in
// _emphasis_ or snake_case still has a boundary before it.
func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// alnumBoundary reports whether no letter or digit comes right before pos.
func alnumBoundary(src string, pos int) bool {
	r, _ := utf8.DecodeLastRuneInString(src[:pos])
	return pos == 0 || !isAlnum(r)
}

// lineRuns is a rendered line's runs of letters and digits, but for a leading
// list number ("2." or "2)"): the renderer renumbers lists, so it need not be
// in the source.
func lineRuns(cells []string) []string {
	var out []string
	var b strings.Builder
	lead := true // nothing but blanks so far
	flush := func(next string) {
		if b.Len() > 0 {
			w := b.String()
			if !(lead && (next == "." || next == ")") && strings.Trim(w, "0123456789") == "") {
				out = append(out, w)
			}
			b.Reset()
			lead = false
		}
	}
	for _, c := range cells {
		if c == "" {
			continue // the continuation of a wide grapheme, or past the end
		}
		r, _ := utf8.DecodeRuneInString(c)
		switch {
		case isAlnum(r):
			b.WriteString(c)
		case b.Len() > 0:
			flush(c)
		case !isBlank(c):
			lead = false
		}
	}
	flush("")
	return out
}

// windowEnd is the end of the n-th non-blank source line from pos, counting
// the rest of pos's line.
func windowEnd(src string, pos, n int) int {
	for pos < len(src) {
		end := len(src)
		if i := strings.IndexByte(src[pos:], '\n'); i >= 0 {
			end = pos + i
		}
		if strings.TrimSpace(src[pos:end]) != "" {
			if n--; n == 0 {
				return end
			}
		}
		pos = end + 1
	}
	return len(src)
}

// lineBounds widens [start, end) to the whole source lines it touches.
func lineBounds(source string, start, end int) (int, int) {
	start = strings.LastIndexByte(source[:start], '\n') + 1
	if i := strings.IndexByte(source[end:], '\n'); i >= 0 {
		end += i
	} else {
		end = len(source)
	}
	return start, end
}
