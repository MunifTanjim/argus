package tui

import (
	"sort"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// textPos is a point in a transcript pane: a row by identity, a line within
// the row (the blank line after a row is its last line), and a cell column of
// the text area. It survives re-layout: new entries, folds, a resize.
type textPos struct {
	row  rowRef
	line int
	col  int
}

// absPos is a point in the pane's line buffer: a line, and a text-area column.
type absPos struct{ line, col int }

func (a absPos) less(b absPos) bool { return a.line < b.line || a.line == b.line && a.col < b.col }

// selection is a visual-mode selection. want is the column vertical motions
// aim for.
type selection struct {
	anchor, cursor textPos
	want           int
	linewise       bool
}

// textSurface is a pane's rendered lines as the selection sees them: rows
// start at first, identified by ids; the text area is cells [x0, x0+w) of
// each line; lines [scroll, scroll+height) show.
type textSurface struct {
	lines  []string
	first  []int
	ids    []rowRef
	x0, w  int
	scroll int
	height int
	cache  map[int][]string
}

func newSurface(lines []string, first []int, ids []rowRef, x0, w, scroll, height int) textSurface {
	return textSurface{lines: lines, first: first, ids: ids, x0: x0, w: max(1, w), scroll: scroll,
		height: max(1, height), cache: map[int][]string{}}
}

// cells is the text area of a line, one entry per cell: the grapheme that
// starts there, "" for the continuation of a wide one or past the line's end.
func (s textSurface) cells(line int) []string {
	if c, ok := s.cache[line]; ok {
		return c
	}
	if line < 0 || line >= len(s.lines) {
		out := make([]string, s.w)
		s.cache[line] = out
		return out
	}
	return s.cacheCells(line, lineBuffer(s.lines[line], s.x0+s.w))
}

// cacheCells records the text-area cells of line from buf, the line drawn.
func (s textSurface) cacheCells(line int, buf uv.ScreenBuffer) []string {
	out := make([]string, s.w)
	l := buf.Line(0)
	for x := range s.w {
		if c := l.At(s.x0 + x); c != nil {
			out[x] = c.Content
		}
	}
	s.cache[line] = out
	return out
}

// lineBuffer draws an ANSI line into a one-row cell buffer at least w wide.
func lineBuffer(line string, w int) uv.ScreenBuffer {
	w = max(w, ansi.StringWidth(line), 1)
	buf := uv.NewScreenBuffer(w, 1)
	uv.NewStyledString(line).Draw(&buf, uv.Rect(0, 0, w, 1))
	return buf
}

func isBlank(c string) bool { return c == "" || c == " " }

// lastText is the last cell holding text, -1 on a blank line. Renderers pad
// with real spaces, so trailing spaces do not count.
func lastText(cells []string) int {
	for x := len(cells) - 1; x >= 0; x-- {
		if !isBlank(cells[x]) {
			return x
		}
	}
	return -1
}

// firstCol is the first cell holding text, 0 on a blank line.
func (s textSurface) firstCol(line int) int {
	for x, c := range s.cells(line) {
		if !isBlank(c) {
			return x
		}
	}
	return 0
}

// rowOf is the row whose lines include line.
func (s textSurface) rowOf(line int) int { return rowAtLine(s.first, line) }

// rowAtLine is the last row starting at or before line, given rows' ascending
// first lines; 0 when none does.
func rowAtLine(first []int, line int) int {
	return max(0, sort.Search(len(first), func(i int) bool { return first[i] > line })-1)
}

func (s textSurface) rowEnd(i int) int {
	if i+1 < len(s.first) {
		return s.first[i+1]
	}
	return len(s.lines)
}

func (s textSurface) abs(p textPos) (absPos, bool) {
	for i, id := range s.ids {
		if id == p.row {
			line := min(s.first[i]+max(p.line, 0), s.rowEnd(i)-1)
			return absPos{line: line, col: s.clampCol(line, p.col)}, true
		}
	}
	return absPos{}, false
}

func (s textSurface) pos(a absPos) textPos {
	a.line = max(0, min(a.line, len(s.lines)-1))
	i := s.rowOf(a.line)
	return textPos{row: s.ids[i], line: a.line - s.first[i], col: a.col}
}

// clampCol keeps a column on the line's text and off a wide continuation.
func (s textSurface) clampCol(line, want int) int {
	cells := s.cells(line)
	col := max(0, min(want, max(lastText(cells), 0)))
	for col > 0 && cells[col] == "" {
		col--
	}
	return col
}

type motion int

const (
	mLeft motion = iota
	mRight
	mUp
	mDown
	mWordNext
	mWordPrev
	mWordEnd
	mLineStart
	mFirstNonBlank
	mLineEnd
)

// move applies a motion. Vertical motions aim for want; the others reset it
// to the new column.
func (s textSurface) move(p absPos, want int, mv motion) (absPos, int) {
	switch mv {
	case mUp, mDown:
		d := 1
		if mv == mUp {
			d = -1
		}
		p.line = max(0, min(len(s.lines)-1, p.line+d))
		p.col = s.clampCol(p.line, want)
		return p, want
	case mLeft:
		cells := s.cells(p.line)
		for c := p.col - 1; c >= 0; c-- {
			if cells[c] != "" {
				p.col = c
				break
			}
		}
	case mRight:
		cells := s.cells(p.line)
		for c := p.col + 1; c <= lastText(cells); c++ {
			if cells[c] != "" {
				p.col = c
				break
			}
		}
	case mLineStart:
		p.col = 0
	case mFirstNonBlank:
		p.col = s.firstCol(p.line)
	case mLineEnd:
		p.col = max(0, lastText(s.cells(p.line)))
	case mWordNext:
		p = s.wordNext(p)
	case mWordPrev:
		p = s.wordPrev(p)
	case mWordEnd:
		p = s.wordEnd(p)
	}
	return p, p.col
}

const (
	clsSpace = iota
	clsWord
	clsPunct
)

func (s textSurface) classAt(p absPos) int {
	cells := s.cells(p.line)
	if p.col > lastText(cells) || isBlank(cells[p.col]) {
		return clsSpace
	}
	if r, _ := utf8.DecodeRuneInString(cells[p.col]); isWordChar(r) {
		return clsWord
	}
	return clsPunct
}

// next steps to the following cell; crossed reports a line change, which ends
// a word. A blank line is one position.
func (s textSurface) next(p absPos) (q absPos, crossed, ok bool) {
	cells := s.cells(p.line)
	for c := p.col + 1; c <= lastText(cells); c++ {
		if cells[c] != "" {
			return absPos{p.line, c}, false, true
		}
	}
	if p.line+1 >= len(s.lines) {
		return p, false, false
	}
	return absPos{p.line + 1, 0}, true, true
}

func (s textSurface) prev(p absPos) (q absPos, crossed, ok bool) {
	cells := s.cells(p.line)
	for c := p.col - 1; c >= 0; c-- {
		if cells[c] != "" {
			return absPos{p.line, c}, false, true
		}
	}
	if p.line == 0 {
		return p, false, false
	}
	return absPos{p.line - 1, max(0, lastText(s.cells(p.line-1)))}, true, true
}

func (s textSurface) wordNext(p absPos) absPos {
	q := p
	if cls := s.classAt(q); cls != clsSpace {
		for {
			nq, crossed, ok := s.next(q)
			if !ok {
				return q
			}
			q = nq
			if crossed || s.classAt(q) != cls {
				break
			}
		}
	}
	for s.classAt(q) == clsSpace {
		nq, _, ok := s.next(q)
		if !ok {
			return q
		}
		q = nq
	}
	return q
}

func (s textSurface) wordEnd(p absPos) absPos {
	return s.wordEdge(p, s.next)
}

func (s textSurface) wordPrev(p absPos) absPos {
	return s.wordEdge(p, s.prev)
}

// wordEdge steps once, skips spaces, then runs to the far edge of the word.
func (s textSurface) wordEdge(p absPos, step func(absPos) (absPos, bool, bool)) absPos {
	q, _, ok := step(p)
	if !ok {
		return p
	}
	for s.classAt(q) == clsSpace {
		nq, _, ok := step(q)
		if !ok {
			return q
		}
		q = nq
	}
	cls := s.classAt(q)
	for {
		nq, crossed, ok := step(q)
		if !ok || crossed || s.classAt(nq) != cls {
			return q
		}
		q = nq
	}
}

// bounds is the selection's start and end, in order.
func (s textSurface) bounds(sel *selection) (start, end absPos, ok bool) {
	a, ok1 := s.abs(sel.anchor)
	b, ok2 := s.abs(sel.cursor)
	if !ok1 || !ok2 {
		return absPos{}, absPos{}, false
	}
	if b.less(a) {
		a, b = b, a
	}
	return a, b, true
}

// lineSpan is the selected text columns [c0, c1) of a line, for a selection
// whose bounds are a and b. A blank line in the selection spans one cell so it
// shows.
func (s textSurface) lineSpan(sel *selection, a, b absPos, line int) (c0, c1 int, ok bool) {
	if line < a.line || line > b.line {
		return 0, 0, false
	}
	c1 = max(lastText(s.cells(line))+1, 1)
	if !sel.linewise {
		if line == a.line {
			c0 = a.col
		}
		if line == b.line {
			c1 = min(c1, b.col+1)
		}
	}
	return c0, max(c0, c1), true
}

// paint reverses the selected cells of the visible lines vis, which are
// lines[scroll:scroll+len(vis)]. vis itself is left as it was. The terminal
// cursor marks the selection's cursor (see visualCursor).
func (s textSurface) paint(sel *selection, vis []string) []string {
	out := make([]string, len(vis))
	copy(out, vis)
	a, b, ok := s.bounds(sel)
	if !ok {
		return out
	}
	for i := range out {
		line := s.scroll + i
		if line < a.line || line > b.line {
			continue
		}
		buf := lineBuffer(out[i], s.x0+s.w)
		if _, ok := s.cache[line]; !ok {
			s.cacheCells(line, buf)
		}
		c0, c1, _ := s.lineSpan(sel, a, b, line)
		if c0 == c1 {
			continue
		}
		l := buf.Line(0)
		for x := c0; x < c1; x++ {
			if c := l.At(s.x0 + x); c != nil {
				c.Style.Attrs |= uv.AttrReverse
			}
		}
		rendered := buf.Render()
		originalWidth := ansi.StringWidth(vis[i])
		targetWidth := max(originalWidth, s.x0+c1)
		out[i] = ansi.Truncate(rendered, targetWidth, "")
	}
	return out
}

// selectedText is the selection's text, cards apart by a blank line. A line
// selection copies each card's source lines (see lineText); a character
// selection copies the selected cells, rejoined against the card's source.
func (s textSurface) selectedText(sel *selection, source func(row int) cardSource) string {
	a, b, ok := s.bounds(sel)
	if !ok {
		return ""
	}
	var cards []string
	if sel.linewise {
		for row := s.rowOf(a.line); row < len(s.first) && s.first[row] <= b.line; row++ {
			l0, l1 := s.first[row], s.rowEnd(row)
			if t := s.lineText(max(a.line, l0), min(b.line, l1-1), l0, l1, source(row)); t != "" {
				cards = append(cards, t)
			}
		}
		return strings.Join(cards, "\n\n")
	}
	var rows []string
	row := s.rowOf(a.line)
	flush := func() {
		if t := rejoin(rows, source(row).text); t != "" {
			cards = append(cards, t)
		}
		rows = rows[:0]
	}
	for line := a.line; line <= b.line; line++ {
		if r := s.rowOf(line); r != row {
			flush()
			row = r
		}
		c0, c1, _ := s.lineSpan(sel, a, b, line)
		var t strings.Builder
		for _, c := range s.cells(line)[c0:c1] {
			t.WriteString(c)
		}
		rows = append(rows, t.String())
	}
	flush()
	return strings.Join(cards, "\n\n")
}
