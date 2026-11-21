package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var (
	c20  = strings.Repeat("x", 20)
	c30  = strings.Repeat("x", 30)
	c200 = strings.Repeat("x", 200)

	wideTable = mdTable([]string{"h1", "h2", "h3", "h4"},
		[]string{"v1" + c20, "v2" + c20, "v3" + c20, "v4" + c20},
		[]string{"w1", "w2", "w3", "w4"})
)

func mdRow(cells ...string) string {
	return "| " + strings.Join(cells, " | ") + " |"
}

func mdTable(header []string, rows ...[]string) string {
	lines := []string{mdRow(header...), "|" + strings.Repeat("---|", len(header))}
	for _, r := range rows {
		lines = append(lines, mdRow(r...))
	}
	return strings.Join(lines, "\n")
}

// stacked wraps records written with "---" separators in the rule markers.
func stacked(records string) string {
	mid := "\n\n" + tableRuleMark + "mid\n\n"
	return "\n" + tableRuleMark + "top\n\n" + strings.ReplaceAll(records, "\n\n---\n\n", mid) +
		"\n\n" + tableRuleMark + "bottom\n"
}

func cells(n int, cell string) []string {
	return slices.Repeat([]string{cell}, n)
}

func TestStackWideTables(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string // "" means unchanged
	}{
		{"fits naturally", mdTable([]string{"a", "b"}, []string{"1", "2"}), 80, ""},
		{"exact fit", mdTable([]string{"aaaa", "bbbbbb", "cc"}, []string{"1", "2", "3"}), 20, ""},
		{"fits with floors", mdTable(cells(3, c30), cells(3, c30)), 44, ""},
		{"long second column", mdTable([]string{"id", "text"}, []string{"1", c200}), 40, ""},
		{"single column", mdTable([]string{"h"}, []string{c200}), 20, ""},
		{"header only", mdTable(cells(5, c20)), 20, ""},
		{"inside fence", "```\n" + mdTable(cells(4, c20), cells(4, c20)) + "\n```", 20, ""},
		{"inside indented fence", "  ```\n" + mdTable(cells(3, c20), cells(3, c20)) + "\n  ```", 20, ""},
		{"info string does not close fence", "```\n```go\n" + mdTable(cells(3, c20), cells(3, c20)) + "\n```", 20, ""},
		{
			"inline markup measured stripped",
			mdTable([]string{"a", "b"}, []string{"`x`", "[t](https://very.long/url/that/overflows)"}),
			10, "",
		},
		{
			"one cell over",
			mdTable([]string{"aaaa", "bbbbbb", "cc"}, []string{"1", "2", "3"}),
			19,
			"- **aaaa:** 1\n- **bbbbbb:** 2\n- **cc:** 3",
		},
		{
			"too wide",
			wideTable,
			40,
			"- **h1:** v1" + c20 + "\n- **h2:** v2" + c20 + "\n- **h3:** v3" + c20 + "\n- **h4:** v4" + c20 +
				"\n\n---\n\n- **h1:** w1\n- **h2:** w2\n- **h3:** w3\n- **h4:** w4",
		},
		{
			"escaped pipe",
			mdTable([]string{"h1", "h2"}, []string{`a \| b`, c30}),
			20,
			"- **h1:** a \\| b\n- **h2:** " + c30,
		},
		{
			"empty header",
			mdTable([]string{"h1", ""}, []string{c30, c30}),
			20,
			"- **h1:** " + c30 + "\n- **Column 2:** " + c30,
		},
		{
			"empty cell",
			mdTable([]string{"h1", "h2", "h3"}, []string{c30, "", c30}),
			20,
			"- **h1:** " + c30 + "\n- **h3:** " + c30,
		},
		{
			"ragged rows",
			mdTable([]string{"h1", "h2", "h3", "h4"}, []string{c20, "b"}, []string{"p", "q", "r", "s", "t", "u"}),
			20,
			"- **h1:** " + c20 + "\n- **h2:** b\n\n---\n\n- **h1:** p\n- **h2:** q\n- **h3:** r\n- **h4:** s",
		},
		{
			"empty row",
			mdTable([]string{"h1", "h2"}, []string{c20, "v"}, []string{"", ""}, []string{c20, "w"}),
			18,
			"- **h1:** " + c20 + "\n- **h2:** v\n\n---\n\n- **h1:** " + c20 + "\n- **h2:** w",
		},
		{
			"row without leading pipe",
			mdTable([]string{"h1", "h2"}, []string{c30, c30}) + "\np | q",
			20,
			"- **h1:** " + c30 + "\n- **h2:** " + c30 + "\n\n---\n\n- **h1:** p\n- **h2:** q",
		},
		{
			// 3 columns of 1 cell need 3 + 8 = 11; counting only the 2 header columns needs 7.
			"delimiter wider than header",
			"| a | b |\n|---|---|---|\n| 1 | 2 | 3 |",
			10,
			"- **a:** 1\n- **b:** 2\n- **Column 3:** 3",
		},
		{
			"label markup stripped",
			mdTable([]string{"**Name**", "`flag`"}, []string{c30, c30}),
			20,
			"- **Name:** " + c30 + "\n- **flag:** " + c30,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.in
			if tt.want != "" {
				want = stacked(tt.want)
			}
			if got := stackWideTables(tt.in, tt.width); got != want {
				t.Errorf("stackWideTables(width=%d)\n got: %q\nwant: %q", tt.width, got, want)
			}
		})
	}
}

func TestStackWideTablesWideRunes(t *testing.T) {
	cjk := "日本語テキスト"
	narrow := mdTable(cells(2, cjk), cells(2, cjk))
	if got := stackWideTables(narrow, 40); got != narrow {
		t.Errorf("2 CJK columns at width 40 changed:\n%q", got)
	}
	wide := mdTable([]string{"a", "b", "c", "d"}, cells(4, cjk))
	if got := stackWideTables(wide, 40); !strings.Contains(got, "- **a:** "+cjk) {
		t.Errorf("4 CJK columns at width 40 not stacked:\n%q", got)
	}
}

func TestStackWideTablesNoTrailingPipe(t *testing.T) {
	with := mdTable([]string{"h1", "h2"}, []string{c20, "v"})
	without := "| h1 | h2\n|---|---\n| " + c20 + " | v"
	if a := stackWideTables(with, 18); a == with {
		t.Fatalf("table not stacked: %q", a)
	}
	if a, b := stackWideTables(with, 18), stackWideTables(without, 18); a != b {
		t.Errorf("trailing pipe changes output:\n with: %q\n without: %q", a, b)
	}
}

func TestStackWideTablesTwoTables(t *testing.T) {
	narrow := mdTable([]string{"a", "b"}, []string{"1", "2"})
	wide := mdTable([]string{"h1", "h2"}, []string{c30, c30})
	got := stackWideTables(narrow+"\n\ntext\n\n"+wide, 20)
	want := narrow + "\n\ntext\n\n" + stacked("- **h1:** "+c30+"\n- **h2:** "+c30)
	if got != want {
		t.Errorf("got: %q\nwant: %q", got, want)
	}
}

func TestRenderMDStacksWideTable(t *testing.T) {
	out := ansi.Strip(testModel().renderMD(wideTable, 40))
	lines := strings.Split(out, "\n")
	idx := func(s string) int {
		return slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, s) })
	}
	a, b := idx("h1: v1"), idx("h2: v2")
	if a < 0 || b < 0 || a == b {
		t.Errorf("labels not on separate lines:\n%s", out)
	}
	if strings.Contains(out, `\`) || strings.Contains(out, "│") || strings.Contains(out, tableRuleMark) {
		t.Errorf("output keeps a backslash, a grid separator, or a rule marker:\n%s", out)
	}
	rule := strings.Repeat("─", 40)
	var got []string
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		switch {
		case l == rule:
			got = append(got, "rule")
		case l == "":
			got = append(got, "blank")
		case strings.HasPrefix(l, "• h1:"):
			got = append(got, "first")
		}
	}
	want := []string{"rule", "first", "rule", "first", "rule"}
	if !slices.Equal(got, want) {
		t.Errorf("layout = %v, want %v:\n%s", got, want, out)
	}
}

func TestRenderMDKeepsBlankLinesAroundStackedTable(t *testing.T) {
	out := ansi.Strip(testModel().renderMD("before\n\n"+wideTable+"\n\nafter", 40))
	lines := strings.Split(out, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	rule := strings.Repeat("─", 40)
	top, bottom := slices.Index(lines, rule), -1
	for i, l := range lines {
		if l == rule {
			bottom = i
		}
	}
	if top < 2 || lines[top-1] != "" || lines[top-2] != "before" {
		t.Errorf("no blank line between text and top rule:\n%s", out)
	}
	if bottom+2 >= len(lines) || lines[bottom+1] != "" || lines[bottom+2] != "after" {
		t.Errorf("no blank line between bottom rule and text:\n%s", out)
	}
}

func TestStripInline(t *testing.T) {
	for in, want := range map[string]string{
		"`x`":                "x",
		"**b** and __b__":    "b and b",
		"*i* _i_ ~~s~~":      "i i s",
		"[t](https://a.b/c)": "t",
		`a \| b`:             "a | b",
		"snake_case_name":    "snake_case_name",
	} {
		if got := stripInline(in); got != want {
			t.Errorf("stripInline(%q) = %q, want %q", in, got, want)
		}
	}
}
