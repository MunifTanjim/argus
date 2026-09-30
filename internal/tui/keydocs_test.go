package tui

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

func commandTable() string {
	type row struct{ sections, keys []string }
	rows := map[string]*row{}
	for section, bs := range screenBindings {
		for _, b := range bs {
			r := rows[b.name]
			if r == nil {
				r = &row{}
				rows[b.name] = r
			}
			label := section
			if o := sectionOffers[section]; o.where != "" && offersKey(o.keys, b) {
				label += " (" + o.where + ")"
			}
			if !slices.Contains(r.sections, label) {
				r.sections = append(r.sections, label)
			}
			for _, tok := range strings.Fields(b.defaults + " " + b.quiet) {
				if v := "`" + tok + "`"; !slices.Contains(r.keys, v) {
					r.keys = append(r.keys, v)
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString("| Command | Sections | Default keys |\n|---|---|---|\n")
	for _, name := range slices.Sorted(maps.Keys(rows)) {
		r := rows[name]
		slices.Sort(r.sections)
		slices.Sort(r.keys)
		b.WriteString("| `" + name + "` | " + strings.Join(r.sections, ", ") + " | " + strings.Join(r.keys, " ") + " |\n")
	}
	return b.String()
}

func TestDocsListEveryCommand(t *testing.T) {
	data, err := os.ReadFile("../../docs/guide/tui.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start := strings.Index(doc, "<!-- keymap-commands:start -->\n")
	end := strings.Index(doc, "<!-- keymap-commands:end -->")
	if start < 0 || end < 0 {
		t.Fatal("tui.md has no keymap-commands markers")
	}
	got := doc[start+len("<!-- keymap-commands:start -->\n") : end]
	if want := commandTable(); got != want {
		t.Errorf("the command table in tui.md is out of date; replace it with:\n%s", want)
	}
}
