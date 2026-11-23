package memory

import (
	"fmt"
	"strings"
	"time"
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

// ValidateOp rejects an op with a multi-line field or an Until that is not a
// YYYY-MM-DD date. Its errors never quote the op's values.
func ValidateOp(op Op) error {
	for _, f := range [][2]string{{"action", op.Action}, {"file", op.File}, {"match", op.Match}, {"text", op.Text}, {"until", op.Until}} {
		if strings.ContainsAny(f[1], "\r\n") {
			return fmt.Errorf("%s must be a single line", f[0])
		}
	}
	if op.Until != "" {
		if _, err := time.Parse(time.DateOnly, op.Until); err != nil {
			return fmt.Errorf("until must be a YYYY-MM-DD date")
		}
	}
	return nil
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

type row struct {
	raw string
	e   *Entry
}

func entriesOf(rows []row) []Entry {
	var es []Entry
	for _, r := range rows {
		if r.e != nil {
			es = append(es, *r.e)
		}
	}
	return es
}

// Apply applies ops in order to content. Failed ops are skipped and reported
// in the returned errors; the rest are returned as applied. Non-bullet lines
// are preserved in place; added entries go after the last bullet.
func Apply(content string, ops []Op, today string) (string, []Op, []error) {
	var rows []row
	if content != "" {
		for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
			r := row{raw: line}
			if m := entryRe.FindStringSubmatch(line); m != nil {
				r.e = &Entry{Text: m[1], Date: m[2], Until: m[3]}
			}
			rows = append(rows, r)
		}
	}
	var applied []Op
	var errs []error
	for _, op := range ops {
		if err := ValidateOp(op); err != nil {
			errs = append(errs, fmt.Errorf("invalid op: %w", err))
			continue
		}
		var err error
		es := entriesOf(rows)
		switch op.Action {
		case "add":
			var text string
			if text, err = cleanText(op.Text); err != nil {
				break
			}
			for _, e := range es {
				if strings.EqualFold(e.Text, text) {
					err = fmt.Errorf("duplicate entry %q", text)
					break
				}
			}
			if err == nil {
				at := len(rows)
				for i, r := range rows {
					if r.e != nil {
						at = i + 1
					}
				}
				nr := row{e: &Entry{Text: text, Date: today, Until: op.Until}}
				rows = append(rows[:at], append([]row{nr}, rows[at:]...)...)
			}
		case "update", "remove":
			var i int
			if i, err = findUnique(es, op.Match); err != nil {
				break
			}
			// map entry index to row index
			n, ri := 0, -1
			for j, r := range rows {
				if r.e == nil {
					continue
				}
				if n == i {
					ri = j
					break
				}
				n++
			}
			if op.Action == "remove" {
				rows = append(rows[:ri], rows[ri+1:]...)
				break
			}
			var text string
			if text, err = cleanText(op.Text); err != nil {
				break
			}
			for j, e := range es {
				if j != i && strings.EqualFold(e.Text, text) {
					err = fmt.Errorf("duplicate entry %q", text)
				}
			}
			if err == nil {
				until := op.Until
				if until == "" {
					until = rows[ri].e.Until
				}
				rows[ri] = row{e: &Entry{Text: text, Date: today, Until: until}}
			}
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
	var b strings.Builder
	for _, r := range rows {
		if r.e != nil {
			b.WriteString(r.e.render())
		} else {
			b.WriteString(r.raw)
		}
		b.WriteString("\n")
	}
	return b.String(), applied, errs
}
