package tui

import (
	"strings"
	"testing"
)

func TestRejoin(t *testing.T) {
	cases := []struct {
		name   string
		rows   []string
		source string
		want   string
	}{
		{"prose", []string{"the quick", "brown fox", "jumps"}, "the quick brown fox jumps", "the quick brown fox jumps"},
		{"hard-wrapped code", []string{"abcdef", "ghij", "xyz"}, "abcdefghij\nxyz", "abcdefghij\nxyz"},
		{"markdown list", []string{"• one", "  two", "• three"}, "- one two\n- three", "• one two\n• three"},
		{"markup in the gap", []string{"foo", "bar baz"}, "foo **bar** baz", "foo bar baz"},
		{"paragraphs", []string{"para one", "", "para two"}, "para one\n\npara two", "para one\n\npara two"},
		{"no source", []string{"a", "  b"}, "", "a\n  b"},
		{"padding above", []string{"   ", "", "the quick", "brown"}, "the quick brown", "the quick brown"},
		{"trailing spaces", []string{"a   ", "b"}, "a b", "a b"},
		{"word not found in source", []string{"foo", "unknown"}, "foo bar", "foo\nunknown"},
		{"gap longer than maxWrapGap", []string{"foo", "bar"}, "foo" + strings.Repeat("x", 20) + "bar", "foo\nbar"},
		{"tab gap", []string{"foo", "bar"}, "foo\tbar", "foo bar"},
		{"all words unmatched then a row", []string{"unknown", "foo"}, "foo bar", "unknown\nfoo"},
		{"short-word boundary: para/a", []string{"para one", "a b"}, "para one a b", "para one a b"},
		{"boundary rejects mid-word match", []string{"x a", "b c", "d"}, "x a bcd", "x a b c\nd"},
	}
	for _, c := range cases {
		if got := rejoin(c.rows, c.source); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
