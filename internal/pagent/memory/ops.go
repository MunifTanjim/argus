package memory

import (
	"fmt"
	"strings"
)

// Op is one memory edit. Action is add, update or remove. Match is a
// substring of an existing entry's text for update and remove.
type Op struct {
	Action string
	File   string
	Match  string
	Text   string
	Until  string
}

func cleanText(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty text")
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("text must be a single line")
	}
	return s, nil
}

func findUnique(entries []Entry, match string) (int, error) {
	match = strings.ToLower(strings.TrimSpace(match))
	if match == "" {
		return -1, fmt.Errorf("empty match")
	}
	idx := -1
	for i, e := range entries {
		if !strings.Contains(strings.ToLower(e.Text), match) {
			continue
		}
		if idx >= 0 {
			return -1, fmt.Errorf("match %q is ambiguous", match)
		}
		idx = i
	}
	if idx < 0 {
		return -1, fmt.Errorf("no entry matches %q", match)
	}
	return idx, nil
}

// Apply applies ops in order to content. Failed ops are skipped and reported
// in the returned errors; the rest are returned as applied.
func Apply(content string, ops []Op, today string) (string, []Op, []error) {
	entries := Parse(content)
	var applied []Op
	var errs []error
	for _, op := range ops {
		var err error
		switch op.Action {
		case "add":
			var text string
			if text, err = cleanText(op.Text); err != nil {
				break
			}
			for _, e := range entries {
				if strings.EqualFold(e.Text, text) {
					err = fmt.Errorf("duplicate entry %q", text)
					break
				}
			}
			if err == nil {
				entries = append(entries, Entry{Text: text, Date: today, Until: op.Until})
			}
		case "update":
			var text string
			if text, err = cleanText(op.Text); err != nil {
				break
			}
			var i int
			if i, err = findUnique(entries, op.Match); err != nil {
				break
			}
			entries[i] = Entry{Text: text, Date: today, Until: op.Until}
		case "remove":
			var i int
			if i, err = findUnique(entries, op.Match); err != nil {
				break
			}
			entries = append(entries[:i], entries[i+1:]...)
		default:
			err = fmt.Errorf("unknown action %q", op.Action)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", op.Action, err))
			continue
		}
		applied = append(applied, op)
	}
	if len(applied) == 0 {
		return content, nil, errs
	}
	return Render(entries, Header(content)), applied, errs
}
