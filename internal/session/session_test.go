package session

import "testing"

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
