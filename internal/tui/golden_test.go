package tui

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// assertGolden compares the whole drawn frame, without escape sequences, to
// testdata/<name>.golden.
func assertGolden(t *testing.T, name string, m model) {
	t.Helper()
	got := ansi.Strip(m.View().Content)
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s: the drawn frame changed\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// killableHome is the Home pane with sessions that have a pane to kill.
func killableHome() model {
	m := homeTestModel()
	for id, s := range m.sessions {
		s.Tmux = session.TmuxLocation{PaneID: "%1"}
		m.sessions[id] = s
	}
	return m
}

func TestHomeAndWorkspaceFramesUnchanged(t *testing.T) {
	onTree := func(m model) model { return pressKeys(m, cw('h')...) }
	cases := []struct {
		name  string
		build func() model
	}{
		{"home", func() model { return typeKeys(homeTestModel(), "j") }},
		{"home-kill", func() model { return typeKeys(killableHome(), "jdd") }},
		{"home-terminals", func() model {
			m := homeTestModel()
			m, _ = upd(m, terminalsMsg{list: twoTerminals, nodes: capable("n1")})
			return typeKeys(m, "gtgt")
		}},
		{"home-splash", func() model {
			m := homeTestModel()
			m.sessions, m.order = map[string]session.Session{}, nil
			return m
		}},
		{"tree-over-home", func() model { return onTree(typeKeys(homeTestModel(), "j")) }},
		{"tree-over-empty-home", func() model {
			m := homeTestModel()
			m.sessions, m.order = map[string]session.Session{}, nil
			return onTree(m)
		}},
		{"project-row", func() model { return pressKeys(typeKeys(onTree(homeTestModel()), "j"), keyMsg("enter")) }},
		{"workspace-row", func() model { return pressKeys(typeKeys(onTree(homeTestModel()), "jj"), keyMsg("enter")) }},
		{"workspace-pane", func() model {
			return typeKeys(pressKeys(typeKeys(onTree(homeTestModel()), "jj"), keyMsg("enter")), "j")
		}},
		{"workspace-kill", func() model {
			return typeKeys(pressKeys(typeKeys(onTree(killableHome()), "jj"), keyMsg("enter")), "jdd")
		}},
		{"workspace-files", func() model {
			m := homeTestModel()
			m.width, m.height = 160, 30
			m.right.hidden = false
			return pressKeys(typeKeys(onTree(m), "jj"), keyMsg("enter"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertGolden(t, tc.name, tc.build()) })
	}
}
