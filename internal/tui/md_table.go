package tui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// tableRuleMark starts a placeholder paragraph that drawTableRules turns into
// a full-width rule. Glamour's own horizontal rule brings blank lines around it.
const tableRuleMark = "\uE000rule-"

// tableMinColumn is the narrowest a column may shrink to before a table stacks.
// The app uses the same value; keep them in sync.
const tableMinColumn = 12

var (
	tableDelimRe = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	inlineCodeRe = regexp.MustCompile("`([^`]*)`")
	inlineLinkRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	inlineEmphRe = []*regexp.Regexp{
		regexp.MustCompile(`\*\*(.+?)\*\*`),
		regexp.MustCompile(`__(.+?)__`),
		regexp.MustCompile(`~~(.+?)~~`),
		regexp.MustCompile(`\*(.+?)\*`),
		regexp.MustCompile(`\b_(.+?)_\b`),
	}
)

// stackWideTables rewrites top-level pipe tables that cannot fit width as
// records of "- **header:** cell" items, so glamour does not squeeze columns
// into fragments. A list is used because glamour paragraphs drop hard breaks.
func stackWideTables(text string, width int) string {
	if !strings.Contains(text, "|") {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	fence := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if f, rest := fenceMarker(line); f != "" {
			switch {
			case fence == "":
				fence = f
			case strings.HasPrefix(f, fence) && strings.TrimSpace(rest) == "":
				fence = ""
			}
		}
		if fence != "" || !strings.HasPrefix(line, "|") || i+1 >= len(lines) || !tableDelimRe.MatchString(lines[i+1]) {
			out = append(out, line)
			continue
		}
		end := i + 2
		for end < len(lines) && isTableRow(lines[end]) {
			end++
		}
		header := splitRow(line)
		if n := len(splitRow(lines[i+1])); len(header) < n {
			header = append(header, make([]string, n-len(header))...)
		}
		var rows [][]string
		for _, l := range lines[i+2 : end] {
			rows = append(rows, splitRow(l))
		}
		if len(header) < 2 || len(rows) == 0 || fitsGrid(header, rows, width) {
			out = append(out, lines[i:end]...)
		} else {
			out = append(out, "", stackRows(header, rows), "")
		}
		i = end - 1
	}
	return strings.Join(out, "\n")
}

// fenceMarker returns the fence run of a fence line (up to 3 spaces of indent)
// and the text after it.
func fenceMarker(line string) (string, string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return "", ""
	}
	for _, m := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, m) {
			rest := strings.TrimLeft(trimmed, m[:1])
			return trimmed[:len(trimmed)-len(rest)], rest
		}
	}
	return "", ""
}

// isTableRow follows goldmark, which continues a table with rows that lack a
// leading pipe.
func isTableRow(line string) bool {
	if f, _ := fenceMarker(line); f != "" {
		return false
	}
	return strings.TrimSpace(line) != "" && strings.Contains(line, "|")
}

// splitRow splits on unescaped pipes; escaped pipes stay escaped in the cell.
func splitRow(line string) []string {
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteString(`\|`)
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	if cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

func stripInline(s string) string {
	s = inlineCodeRe.ReplaceAllString(s, "$1")
	s = inlineLinkRe.ReplaceAllString(s, "$1")
	for _, re := range inlineEmphRe {
		s = re.ReplaceAllString(s, "$1")
	}
	return strings.ReplaceAll(s, `\|`, "|")
}

// fitsGrid mirrors glamour's table layout: 2 cells of padding per column and a
// separator between columns.
func fitsGrid(header []string, rows [][]string, width int) bool {
	total := 3*len(header) - 1
	for c := range header {
		natural := ansi.StringWidth(stripInline(header[c]))
		for _, r := range rows {
			if c < len(r) {
				natural = max(natural, ansi.StringWidth(stripInline(r[c])))
			}
		}
		total += min(natural, tableMinColumn)
	}
	return total <= width
}

func stackRows(header []string, rows [][]string) string {
	labels := make([]string, len(header))
	for c, h := range header {
		labels[c] = stripInline(h)
		if labels[c] == "" {
			labels[c] = "Column " + strconv.Itoa(c+1)
		}
	}
	var records []string
	for _, r := range rows {
		var fields []string
		for c, label := range labels {
			if c < len(r) && r[c] != "" {
				fields = append(fields, "- **"+label+":** "+r[c])
			}
		}
		if len(fields) > 0 {
			records = append(records, strings.Join(fields, "\n"))
		}
	}
	if len(records) == 0 {
		return ""
	}
	return tableRuleMark + "top\n\n" +
		strings.Join(records, "\n\n"+tableRuleMark+"mid\n\n") +
		"\n\n" + tableRuleMark + "bottom"
}

// drawTableRules replaces rule placeholders in rendered output with width-wide
// rules and drops the blank lines between the rules and the records.
func drawTableRules(out string, width int) string {
	if !strings.Contains(out, tableRuleMark) {
		return out
	}
	isBlank := func(l string) bool { return strings.TrimSpace(ansi.Strip(l)) == "" }
	rule := StyleDim.Render(strings.Repeat("─", width))
	var res []string
	trimAfter := false
	for _, l := range strings.Split(out, "\n") {
		kind, ok := strings.CutPrefix(strings.TrimSpace(ansi.Strip(l)), tableRuleMark)
		if !ok {
			if !trimAfter || !isBlank(l) {
				res = append(res, l)
				trimAfter = false
			}
			continue
		}
		if kind != "top" {
			for len(res) > 0 && isBlank(res[len(res)-1]) {
				res = res[:len(res)-1]
			}
		}
		res = append(res, rule)
		trimAfter = kind != "bottom"
	}
	return strings.Join(res, "\n")
}
