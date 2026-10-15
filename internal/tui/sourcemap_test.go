package tui

import (
	"math"
	"strings"
	"testing"
)

// srcOf gives each row of surf the source at its index.
func srcOf(srcs ...string) func(int) cardSource {
	return func(i int) cardSource {
		if i < len(srcs) {
			return cardSource{text: srcs[i]}
		}
		return cardSource{}
	}
}

func TestSourceSliceLinewise(t *testing.T) {
	cases := []struct {
		name   string
		lines  []string
		source string
		l0, l1 int
		want   string
	}{
		{"list items", []string{"Intro:", "• one", "• two", "• three"}, "Intro:\n\n- one\n- two\n- three", 1, 2, "- one\n- two"},
		{"heading", []string{"Title", "body text"}, "# Title\n\nbody text", 0, 0, "# Title"},
		{"code keeps indentation, no fences", []string{"func f() {", "    return 1", "}"}, "```go\nfunc f() {\n    return 1\n}\n```", 0, 2, "func f() {\n    return 1\n}"},
		{"whole markdown line", []string{"use bold and code here"}, "use **bold** and `code` here", 0, 0, "use **bold** and `code` here"},
	}
	for _, c := range cases {
		s := surf(c.lines...)
		got := s.selectedText(sel(s, absPos{c.l0, 2}, absPos{c.l1, 0}, true), srcOf(c.source))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSourceSliceSpans(t *testing.T) {
	s := surf("   ", "Bash ls", "$ echo", "────", "Result", "hello", "world")
	input, result := `{"command":"echo"}`, "hello\nworld"
	src := func(int) cardSource {
		return cardSource{text: input + "\n\n" + result, spans: func() []cardSpan {
			return []cardSpan{{kind: spanChrome, lines: 2}, wholeSpan(1, input), {kind: spanChrome, lines: 2}, {kind: spanText, lines: 2, src: result}}
		}}
	}
	cases := []struct {
		name   string
		l0, l1 int
		want   string
	}{
		{"header only", 1, 1, "Bash ls"},
		{"input line", 2, 2, input},
		{"result lines", 5, 6, result},
		{"one result line", 6, 6, "world"},
		{"whole card", 0, 6, "Bash ls\n" + input + "\nResult\n" + result},
	}
	for _, c := range cases {
		if got := s.selectedText(sel(s, absPos{c.l0, 0}, absPos{c.l1, 0}, true), src); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSourceSliceWholeOnce(t *testing.T) {
	s := surf("a", "b", "c")
	src := func(int) cardSource {
		return cardSource{text: "raw", spans: func() []cardSpan { return []cardSpan{wholeSpan(1, "raw"), wholeSpan(2, "raw")} }}
	}
	if got := s.selectedText(sel(s, absPos{0, 0}, absPos{2, 0}, true), src); got != "raw" {
		t.Errorf("got %q, want the whole source once", got)
	}
}

func TestSourceSliceRenumberedList(t *testing.T) {
	md := "1. a\n1. b\n1. c\n\nSee section 2. later and 3. too"
	s := surf("1. a", "2. b", "3. c", "", "See section 2. later and 3. too")
	src := func(int) cardSource { return cardSource{text: md} }
	for l, want := range []string{"1. a", "1. b", "1. c"} {
		if got := s.selectedText(sel(s, absPos{l, 0}, absPos{l, 0}, true), src); got != want {
			t.Errorf("line %d: got %q, want %q", l, got, want)
		}
	}
}

func TestSourceSliceRenumberedProse(t *testing.T) {
	md := "1. Install the tool\n1. Run the tool\n\nStep 2 Run the tool again"
	s := surf("1. Install the tool", "2. Run the tool", "", "Step 2 Run the tool again")
	if got := s.selectedText(sel(s, absPos{1, 0}, absPos{1, 0}, true), srcOf(md)); got != "1. Run the tool" {
		t.Errorf("got %q, want %q", got, "1. Run the tool")
	}
	md = "1. a\n1. b\n1. c\n\nSee section 2. b too"
	s = surf("1. a", "2. b", "3. c", "", "See section 2. b too")
	for l, want := range []string{"1. a", "1. b", "1. c"} {
		if got := s.selectedText(sel(s, absPos{l, 0}, absPos{l, 0}, true), srcOf(md)); got != want {
			t.Errorf("line %d: got %q, want %q", l, got, want)
		}
	}
}

func TestSourceSlicePunctuation(t *testing.T) {
	md := "use **bold**, then `code`. and _em_: done"
	s := surf("use bold, then code. and em: done")
	if got := s.selectedText(sel(s, absPos{0, 0}, absPos{0, 0}, true), srcOf(md)); got != md {
		t.Errorf("got %q, want %q", got, md)
	}
}

func TestSourceSliceBoundedSearch(t *testing.T) {
	md := "intro\nalpha\nbeta\ngamma\ndelta\nzeta words"
	s := surf("intro", "zeta words", "alpha")
	// "zeta" lies past the next three non-blank lines, so it does not match
	// and does not move the position past "alpha".
	if got := s.selectedText(sel(s, absPos{2, 0}, absPos{2, 0}, true), srcOf(md)); got != "alpha" {
		t.Errorf("got %q, want %q", got, "alpha")
	}
}

func TestSourceSliceHardWrappedRaw(t *testing.T) {
	long := strings.Repeat("x", 30) + "yyyyyyyyyy" + strings.Repeat("z", 25)
	src := "short\n" + long + "\nend"
	s := surf("short", long[:30], long[30:60], long[60:], "end")
	got := s.selectedText(sel(s, absPos{1, 0}, absPos{3, 0}, true), srcOf(src))
	if got != long {
		t.Errorf("got %q, want the original line", got)
	}
}

func TestSourceSliceAcrossCards(t *testing.T) {
	s := surf("first bold", "", "second item")
	got := s.selectedText(sel(s, absPos{0, 0}, absPos{2, 0}, true), srcOf("first **bold**", "- second item"))
	if want := "first **bold**\n\n- second item"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSourceSliceNoSource(t *testing.T) {
	s := surf("   ", "a  ", "  b")
	got := s.selectedText(sel(s, absPos{0, 0}, absPos{2, math.MaxInt}, true), srcOf(""))
	if got != "a\n  b" {
		t.Errorf("got %q, want the visible lines", got)
	}
	got = s.selectedText(sel(s, absPos{1, 0}, absPos{2, math.MaxInt}, true), srcOf("zzz"))
	if got != "a\n  b" {
		t.Errorf("unmatched: got %q, want the visible lines", got)
	}
}
