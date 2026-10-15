package tui

import (
	"slices"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// surf builds a surface of plain lines, one row per blank-separated block.
func surf(lines ...string) textSurface {
	var first []int
	var ids []rowRef
	for i, l := range lines {
		if i == 0 || (lines[i-1] == "" && l != "") {
			first = append(first, i)
			ids = append(ids, rowRef{id: string(rune('a' + len(ids)))})
		}
	}
	return newSurface(lines, first, ids, 0, 40, 0, 10)
}

func TestSurfacePosRoundTrip(t *testing.T) {
	s := surf("one two", "three", "", "four")
	p := s.pos(absPos{line: 3, col: 2})
	if p.row.id != "b" || p.line != 0 || p.col != 2 {
		t.Fatalf("pos = %+v, want row b line 0 col 2", p)
	}
	a, ok := s.abs(p)
	if !ok || a != (absPos{3, 2}) {
		t.Fatalf("abs = %+v %v, want {3 2}", a, ok)
	}
	if p := s.pos(absPos{line: 2}); p.row.id != "a" || p.line != 2 {
		t.Errorf("the separator belongs to the row above, got %+v", p)
	}
	if _, ok := s.abs(textPos{row: rowRef{id: "gone"}}); ok {
		t.Error("a missing row must not resolve")
	}
}

func TestSurfaceExcludesGutter(t *testing.T) {
	s := newSurface([]string{"| hello"}, []int{0}, []rowRef{{id: "a"}}, 2, 10, 0, 5)
	if got := s.cells(0)[0]; got != "h" {
		t.Errorf("cell 0 = %q, want h (gutter excluded)", got)
	}
}

func TestMotionsHorizontal(t *testing.T) {
	s := surf("  foo.bar baz  ")
	p, _ := s.move(absPos{0, 0}, 0, mFirstNonBlank)
	if p.col != 2 {
		t.Errorf("^ = %d, want 2", p.col)
	}
	p, _ = s.move(p, 0, mLineEnd)
	if p.col != 12 {
		t.Errorf("$ = %d, want 12 (trailing spaces are not text)", p.col)
	}
	if q, _ := s.move(p, 0, mRight); q.col != 12 {
		t.Errorf("l past the end = %d, want 12", q.col)
	}
	if q, _ := s.move(absPos{0, 0}, 0, mLeft); q.col != 0 {
		t.Errorf("h at col 0 = %d, want 0", q.col)
	}
}

func TestMotionsWord(t *testing.T) {
	s := surf("foo.bar baz", "qux")
	steps := []struct {
		mv   motion
		want absPos
	}{
		{mWordNext, absPos{0, 3}}, // foo -> .
		{mWordNext, absPos{0, 4}}, // . -> bar
		{mWordNext, absPos{0, 8}}, // bar -> baz
		{mWordNext, absPos{1, 0}}, // baz -> qux on the next line
		{mWordPrev, absPos{0, 8}},
		{mWordEnd, absPos{0, 10}},
		{mWordEnd, absPos{1, 2}},
	}
	p := absPos{0, 0}
	for i, st := range steps {
		p, _ = s.move(p, p.col, st.mv)
		if p != st.want {
			t.Fatalf("step %d: got %+v, want %+v", i, p, st.want)
		}
	}
}

func TestMotionsVerticalKeepWant(t *testing.T) {
	s := surf("long line here", "ab", "another long one")
	p, want := s.move(absPos{0, 10}, 10, mDown)
	if p != (absPos{1, 1}) || want != 10 {
		t.Fatalf("j onto a short line = %+v want=%d, want {1 1} and 10", p, want)
	}
	p, _ = s.move(p, want, mDown)
	if p != (absPos{2, 10}) {
		t.Errorf("j keeps the wanted column, got %+v", p)
	}
}

func TestMotionSkipsWideContinuation(t *testing.T) {
	s := surf("a漢b")
	p, _ := s.move(absPos{0, 0}, 0, mRight)
	if p.col != 1 {
		t.Fatalf("l onto the wide char = %d, want 1", p.col)
	}
	p, _ = s.move(p, p.col, mRight)
	if p.col != 3 {
		t.Errorf("l past the wide char = %d, want 3 (col 2 is its continuation)", p.col)
	}
	p, _ = s.move(p, p.col, mLeft)
	if p.col != 1 {
		t.Errorf("h back = %d, want 1", p.col)
	}
	if c := s.clampCol(0, 2); c != 1 {
		t.Errorf("clampCol onto a continuation = %d, want 1", c)
	}
}

func sel(s textSurface, a, b absPos, linewise bool) *selection {
	return &selection{anchor: s.pos(a), cursor: s.pos(b), linewise: linewise}
}

func TestSelectedTextCharwise(t *testing.T) {
	s := surf("hello world", "second line")
	got := s.selectedText(sel(s, absPos{0, 6}, absPos{1, 5}, false), func(int) string { return "" })
	if got != "world\nsecond" {
		t.Errorf("got %q", got)
	}
	// Backwards selection normalizes.
	got = s.selectedText(sel(s, absPos{1, 5}, absPos{0, 6}, false), func(int) string { return "" })
	if got != "world\nsecond" {
		t.Errorf("backwards: got %q", got)
	}
}

func TestSelectedTextLinewiseAcrossRows(t *testing.T) {
	s := surf("first row", "", "second row")
	got := s.selectedText(sel(s, absPos{0, 3}, absPos{2, 1}, true), func(int) string { return "" })
	if got != "first row\n\nsecond row" {
		t.Errorf("got %q", got)
	}
}

func TestSelectedTextRejoinsWithSource(t *testing.T) {
	s := surf("the quick", "brown fox")
	got := s.selectedText(sel(s, absPos{0, 0}, absPos{1, 8}, false), func(int) string { return "the quick brown fox" })
	if got != "the quick brown fox" {
		t.Errorf("got %q", got)
	}
}

func TestSelectedTextWideChars(t *testing.T) {
	s := surf("a漢b")
	got := s.selectedText(sel(s, absPos{0, 0}, absPos{0, 3}, false), func(int) string { return "" })
	if got != "a漢b" {
		t.Errorf("got %q, want each character once", got)
	}
}

func TestPaintReversesSelectedCells(t *testing.T) {
	s := surf("hello world")
	out := s.paint(sel(s, absPos{0, 0}, absPos{0, 4}, false), s.lines)
	if out[0] == s.lines[0] {
		t.Fatal("paint left the line unchanged")
	}
	if ansi.Strip(out[0]) != "hello world" {
		t.Errorf("paint changed the text: %q", ansi.Strip(out[0]))
	}
	if s.lines[0] != "hello world" {
		t.Error("paint must not modify the surface's lines")
	}

	// Selected cells (0..4) are reversed; the rest of the text is not.
	for x, rev := range reversedAt(out[0], 0, len("hello world")) {
		if want := x <= 4; rev != want {
			t.Errorf("cell %d reversed = %v, want %v", x, rev, want)
		}
	}
	l := lineBuffer(out[0], 40).Line(0)
	// Cell 4 is the cursor; verify it has bold and underline
	if c := l.At(4); c != nil {
		if c.Style.Attrs&uv.AttrBold == 0 {
			t.Error("cursor cell should have bold")
		}
		if c.Style.Underline != uv.UnderlineSingle {
			t.Error("cursor cell should have single underline")
		}
	}

	// Verify visible width is not reduced
	if ansi.StringWidth(out[0]) < ansi.StringWidth(s.lines[0]) {
		t.Errorf("paint reduced visible width: %d < %d", ansi.StringWidth(out[0]), ansi.StringWidth(s.lines[0]))
	}
}

func TestPaintWithScroll(t *testing.T) {
	// Test paint with scroll > 0 ensures the correct line is painted
	// Create surface with 3 lines, but only display lines 1-2 (scroll=1, height=2)
	s := newSurface([]string{"line0", "line1", "line2"}, []int{0, 1, 2}, []rowRef{{id: "a"}, {id: "b"}, {id: "c"}}, 0, 40, 1, 2)
	// Select line 1, cells 0-3
	out := s.paint(sel(s, absPos{1, 0}, absPos{1, 3}, false), s.lines[1:])
	// out has 2 elements: lines[1:3] = ["line1", "line2"]
	// Selection spans line 1 (vis index 0), so cells 0..3 should have reverse
	for x, rev := range reversedAt(out[0], 0, 4) {
		if !rev {
			t.Errorf("vis[0] cell %d should have reverse (inside selection)", x)
		}
	}
	// out[1] (line2) is not in selection; should have no reverse
	if slices.Contains(reversedAt(out[1], 0, 40), true) {
		t.Error("vis[1] (line2) should not have reverse (outside selection)")
	}
}

func TestPaintBlankLine(t *testing.T) {
	// Test paint on a blank line inside selection
	s := surf("text", "", "more")
	sel := &selection{
		anchor:   s.pos(absPos{0, 0}),
		cursor:   s.pos(absPos{2, 2}),
		linewise: true,
	}
	out := s.paint(sel, s.lines)
	// Line 1 is blank and inside selection; it should have at least one highlighted cell
	if !slices.Contains(reversedAt(out[1], 0, 40), true) {
		t.Error("blank line inside selection should have at least one reversed cell")
	}
}

// reversedAt reports, for cells [x0, x0+n) of an ANSI line, whether each is
// drawn in reverse video.
func reversedAt(line string, x0, n int) []bool {
	l := lineBuffer(line, x0+n).Line(0)
	out := make([]bool, n)
	for x := range out {
		c := l.At(x0 + x)
		out[x] = c != nil && c.Style.Attrs&uv.AttrReverse != 0
	}
	return out
}
