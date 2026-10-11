package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadKeymapsObjectForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`tui:
  keymap:
    global:
      "<C-q>": ":quit<CR>"
      "<Leader>g":
        action: ":open palette<CR>"
        desc: go to
      "a":
        desc: nothing
      "b":
        action: j
        color: red
`), 0o600); err != nil {
		t.Fatal(err)
	}
	km, err := ReadKeymaps(path)
	if err != nil {
		t.Fatal(err)
	}
	g := km["global"]
	want := map[string]Mapping{
		"<C-q>":     {Action: ":quit<CR>"},
		"<Leader>g": {Action: ":open palette<CR>", Desc: "go to"},
		"a":         {Desc: "nothing", Problem: "needs an action"},
		"b":         {Action: "j", Problem: `unknown field "color"`},
	}
	for k, w := range want {
		if g[k] != w {
			t.Errorf("%s = %+v, want %+v", k, g[k], w)
		}
	}
}
