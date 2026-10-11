package tui

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

func commandTable() string {
	type row struct{ sections, builtin, mapped []string }
	rows := map[string]*row{}
	add := func(list *[]string, toks string) {
		for _, tok := range strings.Fields(toks) {
			if v := "`" + tok + "`"; !slices.Contains(*list, v) {
				*list = append(*list, v)
			}
		}
	}
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
			add(&r.builtin, b.defaults+" "+b.quiet)
			add(&r.mapped, b.maps)
		}
	}
	var b strings.Builder
	b.WriteString("| Command | Sections | Built-in keys | Default mappings |\n|---|---|---|---|\n")
	for _, name := range slices.Sorted(maps.Keys(rows)) {
		r := rows[name]
		slices.Sort(r.sections)
		slices.Sort(r.builtin)
		slices.Sort(r.mapped)
		b.WriteString("| `" + name + "` | " + strings.Join(r.sections, ", ") + " | " + strings.Join(r.builtin, " ") + " | " + strings.Join(r.mapped, " ") + " |\n")
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
