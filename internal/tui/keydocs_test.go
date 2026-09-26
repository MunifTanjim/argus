package tui

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

func commandTable() string {
	type row struct{ screens, keys []string }
	rows := map[string]*row{}
	for screen, bs := range screenBindings {
		for _, b := range bs {
			r := rows[b.name]
			if r == nil {
				r = &row{}
				rows[b.name] = r
			}
			if !slices.Contains(r.screens, screen) {
				r.screens = append(r.screens, screen)
			}
			for _, tok := range strings.Fields(b.defaults) {
				if v := "`" + tok + "`"; !slices.Contains(r.keys, v) {
					r.keys = append(r.keys, v)
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString("| Command | Screens | Default keys |\n|---|---|---|\n")
	for _, name := range slices.Sorted(maps.Keys(rows)) {
		r := rows[name]
		slices.Sort(r.screens)
		slices.Sort(r.keys)
		b.WriteString("| `" + name + "` | " + strings.Join(r.screens, ", ") + " | " + strings.Join(r.keys, " ") + " |\n")
	}
	return b.String()
}

func TestDocsListEveryCommand(t *testing.T) {
	data, err := os.ReadFile("../../docs/getting-started/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start := strings.Index(doc, "<!-- keymap-commands:start -->\n")
	end := strings.Index(doc, "<!-- keymap-commands:end -->")
	if start < 0 || end < 0 {
		t.Fatal("configuration.md has no keymap-commands markers")
	}
	got := doc[start+len("<!-- keymap-commands:start -->\n") : end]
	if want := commandTable(); got != want {
		t.Errorf("the command table in configuration.md is out of date; replace it with:\n%s", want)
	}
}
