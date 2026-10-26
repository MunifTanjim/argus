package main

import "testing"

func TestDecideAttach(t *testing.T) {
	cases := []struct {
		name          string
		st            *launchState
		node, gateway bool
		want          attachAction
		stale         bool
	}{
		{"no state, nothing", nil, false, false, attachNone, false},
		{"no state, node", nil, true, false, attachNone, false},
		{"no state, standalone gateway", nil, false, true, attachNone, false},
		{"isolated, node", &launchState{Mode: launchModeIsolated}, true, false, attachState, false},
		{"isolated, gone", &launchState{Mode: launchModeIsolated}, false, false, attachNone, true},
		{"connected, node", &launchState{Mode: launchModeConnected}, true, false, attachState, false},
		{"gateway, both", &launchState{Mode: launchModeGateway}, true, true, attachState, false},
		{"gateway, only gateway", &launchState{Mode: launchModeGateway}, false, true, attachPartial, false},
		{"gateway, only node", &launchState{Mode: launchModeGateway}, true, false, attachPartial, false},
		{"gateway, gone", &launchState{Mode: launchModeGateway}, false, false, attachNone, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, stale := decideAttach(c.st, c.node, c.gateway)
			if got != c.want || stale != c.stale {
				t.Errorf("decideAttach = (%v, %v), want (%v, %v)", got, stale, c.want, c.stale)
			}
		})
	}
}
