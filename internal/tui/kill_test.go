package tui

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestKillRefusal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		s       session.Session
		refused bool
	}{
		{"pane", session.Session{Agent: "claude", Tmux: session.TmuxLocation{PaneID: "%1"}, Input: session.InputPane}, false},
		{"paneless opencode", session.Session{Agent: "opencode", Input: session.InputAPI}, false},
		{"paneless claude with mod", session.Session{Agent: "claude", Input: session.InputAPI}, false},
		{"paneless claude without mod", session.Session{Agent: "claude", Frontend: session.FrontendExternal, Input: session.InputNone}, true},
	} {
		if got := killRefusal(tc.s) != ""; got != tc.refused {
			t.Errorf("%s: refused = %v, want %v", tc.name, got, tc.refused)
		}
	}
}
