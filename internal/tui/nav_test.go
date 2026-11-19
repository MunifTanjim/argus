package tui

import (
	"slices"
	"testing"
)

func headedList() itemLines {
	var l itemLines
	l.text("header A")
	l.add(0, "a0")
	l.text("")
	l.add(1, "a1")
	l.text("")
	l.text("header B")
	l.add(2, "b0")
	return l
}

func TestWindowShowsHeaderAboveItemOnScrollUp(t *testing.T) {
	l := headedList()
	c := &ctx{m: &model{listScroll: map[string]int{}}}
	l.window(c, 2, 2)
	if got := l.window(c, 0, 2); !slices.Equal(got, []string{"header A", "a0"}) {
		t.Fatalf("got %q, want the header above a0", got)
	}
}

func TestWindowShowsSectionHeaderOnScrollUp(t *testing.T) {
	var l itemLines
	l.add(0, "a0")
	l.text("")
	l.text("header B")
	l.add(1, "b0")
	l.add(2, "b1")
	c := &ctx{m: &model{listScroll: map[string]int{}}}
	l.window(c, 2, 1)
	if got := l.window(c, 1, 2); !slices.Equal(got, []string{"header B", "b0"}) {
		t.Fatalf("got %q, want the header above b0", got)
	}
}

func TestWindowKeepsItemWhenLeadDoesNotFit(t *testing.T) {
	l := headedList()
	c := &ctx{m: &model{listScroll: map[string]int{}}}
	l.window(c, 2, 1)
	if got := l.window(c, 0, 1); !slices.Equal(got, []string{"a0"}) {
		t.Fatalf("got %q, want a0 alone", got)
	}
}
