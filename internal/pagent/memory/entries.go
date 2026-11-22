// Package memory holds the pure logic for agent memory files: entry
// parsing, edit operations, tidy guards and secret scanning.
package memory

import (
	"regexp"
	"strings"
)

// Entry is one bullet line: "- <text> (YYYY-MM-DD)" with an optional
// " · until YYYY-MM-DD" suffix. Line is the 1-based line number.
type Entry struct {
	Line  int
	Text  string
	Date  string
	Until string
}

var entryRe = regexp.MustCompile(`^- (.*?)(?: \((\d{4}-\d{2}-\d{2})\))?(?: · until (\d{4}-\d{2}-\d{2}))?\s*$`)

// Parse returns the bullet entries of content, ignoring other lines.
func Parse(content string) []Entry {
	var entries []Entry
	for i, line := range strings.Split(content, "\n") {
		m := entryRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, Entry{Line: i + 1, Text: m[1], Date: m[2], Until: m[3]})
	}
	return entries
}

// Header returns the leading "# " line of content, or "".
func Header(content string) string {
	first, _, _ := strings.Cut(content, "\n")
	if strings.HasPrefix(first, "# ") {
		return first
	}
	return ""
}

func (e Entry) render() string {
	s := "- " + e.Text
	if e.Date != "" {
		s += " (" + e.Date + ")"
	}
	if e.Until != "" {
		s += " · until " + e.Until
	}
	return s
}

// Render writes the header (when non-empty) followed by one line per entry.
func Render(entries []Entry, header string) string {
	var b strings.Builder
	if header != "" {
		b.WriteString(header + "\n")
	}
	for _, e := range entries {
		b.WriteString(e.render() + "\n")
	}
	return b.String()
}
