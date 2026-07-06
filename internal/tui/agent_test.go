package tui

import "testing"

func TestAgentLabel(t *testing.T) {
	cases := []struct {
		agent     string
		wantLabel string
	}{
		{"claude", "Claude"},
		{"codex", "Codex"},
		{"antigravity", "Antigravity"},
		{"future-agent", "future-agent"}, // unknown: raw id
		{"", ""},                         // empty: no label
	}
	for _, tc := range cases {
		label, c := agentLabel(tc.agent)
		if label != tc.wantLabel {
			t.Errorf("agentLabel(%q) label = %q, want %q", tc.agent, label, tc.wantLabel)
		}
		if tc.agent == "" && c != nil {
			t.Errorf("agentLabel(\"\") color = %v, want nil", c)
		}
		if tc.agent != "" && c == nil {
			t.Errorf("agentLabel(%q) color = nil, want non-nil", tc.agent)
		}
	}
}

func TestAgentLabelColorDimUntilFocused(t *testing.T) {
	if got := agentLabelColor(ColorAgentCodex, false); got != ColorTextDim {
		t.Errorf("unfocused label should be dimmed, got %v", got)
	}
	if got := agentLabelColor(ColorAgentCodex, true); got != ColorAgentCodex {
		t.Errorf("focused label should keep the agent accent, got %v", got)
	}
}
