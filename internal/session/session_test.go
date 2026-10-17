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

func TestTakesPrompt(t *testing.T) {
	perm := &Interaction{Kind: InteractionPermission}
	idle := &Interaction{Kind: InteractionIdle}
	for _, tc := range []struct {
		name   string
		s      Session
		queues bool
		want   bool
	}{
		{"idle", Session{Status: StatusIdle}, false, true},
		{"idle prompt", Session{Status: StatusAwaitingInput, Interaction: idle}, false, true},
		{"working", Session{Status: StatusWorking}, false, false},
		{"working, queues", Session{Status: StatusWorking}, true, true},
		{"starting, queues", Session{Status: StatusStarting}, true, false},
		{"permission, queues", Session{Status: StatusAwaitingInput, Interaction: perm}, true, false},
		{"working with permission, queues", Session{Status: StatusWorking, Interaction: perm}, true, false},
		{"no input channel", Session{Status: StatusIdle, Input: InputNone}, false, false},
	} {
		if tc.name != "no input channel" {
			tc.s.Input = InputPane
		}
		if got := tc.s.TakesPrompt(tc.queues); got != tc.want {
			t.Errorf("%s: TakesPrompt(%v) = %v, want %v", tc.name, tc.queues, got, tc.want)
		}
	}
}
