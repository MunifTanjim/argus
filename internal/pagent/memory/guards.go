package memory

import (
	"fmt"
	"regexp"
	"strings"
)

// tidyFraction is the share of entries one tidy pass may change.
const tidyFraction = 0.2

var wordRe = regexp.MustCompile(`[a-z0-9]+`)
var dateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

func words(s string) []string {
	return wordRe.FindAllString(strings.ToLower(dateRe.ReplaceAllString(s, "")), -1)
}

// CheckTidy enforces the tidy guards: at most a fifth of entries changed
// (at least one allowed), user-sourced lines kept, and no words in new
// entries that are absent from the entries they replace. userLines holds
// 1-based line numbers of before.
func CheckTidy(before, after []Entry, userLines map[int]bool) error {
	remaining := map[string]int{}
	for _, e := range after {
		remaining[e.Text]++
	}
	var removed []Entry
	for _, e := range before {
		if remaining[e.Text] > 0 {
			remaining[e.Text]--
			continue
		}
		removed = append(removed, e)
	}

	limit := int(tidyFraction * float64(len(before)))
	if limit < 1 && len(before) >= 1 {
		limit = 1
	}
	if len(removed) > limit {
		return fmt.Errorf("tidy changes %d of %d entries, limit is %d", len(removed), len(before), limit)
	}
	for _, e := range removed {
		if userLines[e.Line] {
			return fmt.Errorf("tidy would change user line %d: %q", e.Line, e.Text)
		}
	}

	known := map[string]bool{}
	for _, e := range removed {
		for _, w := range words(e.Text) {
			known[w] = true
		}
	}
	beforeTexts := map[string]int{}
	for _, e := range before {
		beforeTexts[e.Text]++
	}
	for _, e := range after {
		if beforeTexts[e.Text] > 0 {
			beforeTexts[e.Text]--
			continue
		}
		for _, w := range words(e.Text) {
			if !known[w] {
				return fmt.Errorf("tidy introduced new word %q in %q", w, e.Text)
			}
		}
	}
	return nil
}

// OverBudget reports whether content is larger than budget bytes.
func OverBudget(content string, budget int64) bool {
	return int64(len(content)) > budget
}
