package session

import "testing"

func TestControllable(t *testing.T) {
	withPane := Session{Tmux: TmuxLocation{PaneID: "%3"}}
	if !withPane.Controllable() {
		t.Fatal("session with a pane should be controllable")
	}
	paneless := Session{Frontend: FrontendVSCode}
	if paneless.Controllable() {
		t.Fatal("paneless session should not be controllable")
	}
}

func TestFrontendConstants(t *testing.T) {
	if FrontendTmux != "tmux" || FrontendVSCode != "vscode" || FrontendExternal != "external" {
		t.Fatalf("unexpected frontend values: %s %s %s", FrontendTmux, FrontendVSCode, FrontendExternal)
	}
}

func TestStatusLabel(t *testing.T) {
	cases := map[Status]string{
		StatusWorking:       "working",
		StatusAwaitingInput: "awaiting",
		StatusIdle:          "idle",
		StatusDiscovered:    "discovered",
		StatusDead:          "dead",
	}
	for s, want := range cases {
		if got := s.Label(); got != want {
			t.Errorf("%s.Label() = %q, want %q", s, got, want)
		}
	}
}
