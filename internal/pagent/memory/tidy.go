package memory

import (
	"fmt"
	"slices"
	"strings"
)

const tidyPromptHead = `You tidy the long-term memory of a personal assistant. Reduce clutter without losing information.

Reply with only JSON, no prose:
{"ops":[{"action":"update|remove|add","file":"MEMORY.md|topics/<name>.md","match":"","text":"","until":""}],"log":""}

Rules:
- Merge duplicate or overlapping entries: "update" one entry with the merged text and "remove" the others.
- Move detail that is not needed in every conversation to a topic file: "add" it to topics/<name>.md (lowercase letters, digits, - and _) and "remove" it from MEMORY.md.
- "update" and "remove" need match, a substring of exactly one existing entry. Each text is a single line.
- Use only words already present in the entries you merge or move. Never add new facts.
- Change as few entries as possible, and leave entries that are fine alone.
- Entries that are expired are removed separately; ignore them.
- Leave "log" empty.
- If nothing needs tidying, reply {"ops":[],"log":""}.
`

// BuildTidyPrompt asks for tidy operations given the current MEMORY.md and the
// topic file names.
func BuildTidyPrompt(mem string, topics []string) string {
	var b strings.Builder
	b.WriteString(tidyPromptHead)
	section := func(title, body string) {
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		if strings.TrimSpace(body) == "" {
			body = "(empty)"
		}
		b.WriteString(strings.TrimSpace(body))
		b.WriteString("\n")
	}
	section("Current MEMORY.md", mem)
	section("Topic files", strings.Join(topics, "\n"))
	return b.String()
}

// ExpiredEntries returns the entries whose Until date is before today.
func ExpiredEntries(content, today string) []Entry {
	var out []Entry
	for _, e := range Parse(content) {
		if e.Until != "" && e.Until < today {
			out = append(out, e)
		}
	}
	return out
}

// DropLines returns content without the entry lines in expired, keeping every
// other line as it is.
func DropLines(content string, expired []Entry) string {
	if len(expired) == 0 {
		return content
	}
	drop := map[int]bool{}
	for _, e := range expired {
		drop[e.Line] = true
	}
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for i, ln := range lines {
		if drop[i+1] {
			continue
		}
		b.WriteString(ln)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// TidyPlan is the checked result of a tidy pass.
type TidyPlan struct {
	Memory  string // new MEMORY.md
	Expired int    // entries dropped for being expired
	Applied []Op   // model ops applied to MEMORY.md
	Topics  []Op   // adds to topic files
	Errs    []error
}

// PlanTidy drops expired entries from content (user-written ones too: the
// user wrote the until date, so expiry is their instruction), applies the model's ops and
// checks the guards against the whole file. Expired entries are exempt from
// the change limit. Ops name MEMORY.md (or ""), or add to a topic file.
// userLines holds the 1-based lines of content written by the user.
func PlanTidy(content string, ops []Op, today string, userLines map[int]bool) (TidyPlan, error) {
	expired := ExpiredEntries(content, today)
	var before []Entry
	for _, e := range Parse(content) {
		if !slices.ContainsFunc(expired, func(x Entry) bool { return x.Line == e.Line }) {
			before = append(before, e)
		}
	}
	plan := TidyPlan{Memory: DropLines(content, expired), Expired: len(expired)}

	var memOps []Op
	for _, op := range ops {
		switch {
		case op.File == "" || op.File == "MEMORY.md":
			memOps = append(memOps, op)
		case strings.HasPrefix(op.File, "topics/") && op.Action == "add":
			plan.Topics = append(plan.Topics, op)
		default:
			return TidyPlan{}, fmt.Errorf("tidy may not %s in %s", op.Action, op.File)
		}
	}
	out, applied, errs := Apply(plan.Memory, memOps, today)
	plan.Memory, plan.Applied, plan.Errs = out, applied, errs

	after := Parse(plan.Memory)
	if err := CheckTidy(before, after, userLines); err != nil {
		return TidyPlan{}, err
	}
	// Text moved to a topic file may only use words of entries that left MEMORY.md.
	known := map[string]bool{}
	remaining := map[string]int{}
	for _, e := range after {
		remaining[e.Text]++
	}
	for _, e := range before {
		if remaining[e.Text] > 0 {
			remaining[e.Text]--
			continue
		}
		for _, w := range words(e.Text) {
			known[w] = true
		}
	}
	for _, op := range plan.Topics {
		for _, w := range words(op.Text) {
			if !known[w] {
				return TidyPlan{}, fmt.Errorf("tidy introduced new word %q in %q", w, op.Text)
			}
		}
	}
	return plan, nil
}
