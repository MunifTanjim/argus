package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/logbuf"
)

func assertView(t *testing.T, step string, m model, want shownView) {
	t.Helper()
	if got := viewOf(m); got != want {
		t.Fatalf("%s: view = %v, want %v", step, got, want)
	}
}

func TestBackStackHomeSessionScreen(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewHome)
	s := m.sessions["n1:s1"]
	s.CanOpenTerminal = true
	m.sessions["n1:s1"] = s
	assertView(t, "start", m, viewHome)

	m = pressKeys(m, keyMsg("enter"))
	assertView(t, "<CR> on a session", m, viewSession)

	m = pressKeys(m, ctrlKey('t'))
	assertView(t, "<C-t>", m, viewScreen)

	m = pressKeys(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	assertView(t, "^]", m, viewSession)

	m = pressKeys(m, keyMsg("esc"))
	assertView(t, "<Esc>", m, viewHome)
}

func TestBackStackWorkspaceSession(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	assertView(t, "start", m, viewTree)

	m = pressKeys(m, keyMsg("enter"))
	assertView(t, "<CR> on a session", m, viewSession)

	m = pressKeys(m, keyMsg("esc"))
	assertView(t, "<Esc>", m, viewTree)
}

func TestBackStackResumeKeepsFirstReturn(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	m = pressKeys(m, keyMsg("enter"))
	assertView(t, "<CR> on a session", m, viewSession)

	m, _ = upd(m, resumeResultMsg{sessionID: "n1:s3"})
	assertView(t, "resume", m, viewSession)
	if m.liveSessionID() != "n1:s3" {
		t.Fatalf("resume: liveSessionID = %q, want n1:s3", m.liveSessionID())
	}

	m = pressKeys(m, keyMsg("esc"))
	assertView(t, "<Esc>", m, viewTree)
}

func TestBackStackTreeKeepsTheWayBackUntilEnterOpensAnotherRow(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	m = pressKeys(m, keyMsg("enter"))
	assertView(t, "<CR> on a session", m, viewSession)
	m = pressKeys(m, cw('h')...)
	assertView(t, "<C-w>h", m, viewSession)
	m = pressKeys(pressKeys(m, cw('l')...), keyMsg("esc"))
	assertView(t, "<Esc> after the tree", m, viewTree)
	m = pressKeys(m, keyMsg("enter"))
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("j"), keyMsg("enter"))
	assertView(t, "<CR> on n1:w2", m, viewTree)
	if len(m.main) != 1 {
		t.Errorf("stack = %v, want one entry", m.main)
	}
}

func TestBackStackHomeSessionTreeKeepsTheStack(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewHome)
	m = pressKeys(m, keyMsg("enter"))
	assertView(t, "<CR> on a session", m, viewSession)
	m = pressKeys(m, cw('h')...)
	if _, onHome := m.main[0].(homeComp); len(m.main) != 2 || !onHome || trOf(m).sessionID != "n1:s1" {
		t.Errorf("stack = %T..., len %d, want [Home, n1:s1]", m.main[0], len(m.main))
	}
}

// A resume from History opens the session over the History tab, and back
// returns to the tab, from its session list or from a past session's
// transcript.
func TestBackStackResumeFromHistory(t *testing.T) {
	p := histProj
	p.Cwd = "/p"
	m := withHistorySessions(homeTestModel(), p, historyPage())
	m.client = &recordingClient{}
	m, cmd := typeKeysCmd(m, "R")
	if cmd == nil {
		t.Fatal("R on a resumable session should send the resume")
	}
	m, _ = upd(m, resumeResultMsg{sessionID: "n1:s1"})
	assertView(t, "resume from the session list", m, viewSession)
	m = pressKeys(m, keyMsg("esc"))
	assertView(t, "<Esc>", m, viewHistorySessions)

	m = pressKeys(m, keyMsg("enter"))
	assertView(t, "<CR> on a past session", m, viewHistoryTranscript)
	m, _ = upd(m, resumeResultMsg{sessionID: "n1:s1"})
	assertView(t, "resume from the transcript", m, viewSession)
	m = pressKeys(m, keyMsg("esc"))
	assertView(t, "<Esc>", m, viewHistoryTranscript)
	m = pressKeys(m, keyMsg("esc"))
	assertView(t, "second <Esc>", m, viewHistorySessions)
}

// A route away from a pane and back brings the pane back with its cursor.
func TestPanesKeepTheirCursorOnTheWayBack(t *testing.T) {
	onTree := func(m model) model { return pressKeys(m, cw('h')...) }
	homeCursor := func(m model) int { return homeOf(m).cursor }
	paneCursor := func(m model) int { return paneOf(m).cursor }
	home := func() model {
		m := homeTestModel()
		m.logs = logbuf.New(10)
		return typeKeys(m, "j")
	}
	pane := func() model {
		return typeKeys(pressKeys(typeKeys(onTree(homeTestModel()), "jjj"), keyMsg("enter")), "j")
	}
	cases := []struct {
		name       string
		start      func() model
		away, back func(model) model
		cursor     func(model) int
	}{
		{"Home to History and back", home, func(m model) model { return typeKeys(m, "gt") }, func(m model) model { return typeKeys(m, "gT") }, homeCursor},
		{"Home to Logs and back", home, func(m model) model { return typeKeys(m, "gT") }, func(m model) model { return typeKeys(m, "gt") }, homeCursor},
		{"Home to a session and back", home, func(m model) model { return pressKeys(m, keyMsg("enter")) }, func(m model) model { return pressKeys(m, keyMsg("esc")) }, homeCursor},
		{"workspace pane to a session and back", pane, func(m model) model { return pressKeys(m, keyMsg("enter")) }, func(m model) model { return pressKeys(m, keyMsg("esc")) }, paneCursor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.start()
			if tc.cursor(m) != 1 {
				t.Fatalf("setup: cursor = %d, want 1", tc.cursor(m))
			}
			m = tc.away(m)
			if tc.cursor(m) != 1 {
				t.Fatalf("away: cursor = %d, want the pane kept on 1", tc.cursor(m))
			}
			m = tc.back(m)
			if tc.cursor(m) != 1 || !isPane(m.rootComp()) {
				t.Errorf("back: root = %T cursor = %d, want the pane back on 1", m.rootComp(), tc.cursor(m))
			}
		})
	}
}
