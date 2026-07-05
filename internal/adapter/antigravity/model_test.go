package antigravity

import "testing"

func TestModelNameColor(t *testing.T) {
	tests := []struct {
		id        string
		wantName  string
		wantColor string
	}{
		{"gpt-5-codex", "gpt-5-codex", "#83a598"},
		{"GPT-4o", "GPT-4o", "#83a598"},
		{"gemini-2.5-pro", "gemini-2.5-pro", "#8ec07c"},
		{"claude-opus-4", "claude-opus-4", "#d3869b"},
		{"llama-3", "llama-3", ""},
		{"", "", ""},
		{"  ", "", ""},
	}
	for _, tt := range tests {
		name, color := modelNameColor(tt.id)
		if name != tt.wantName || color != tt.wantColor {
			t.Errorf("modelNameColor(%q) = (%q, %q), want (%q, %q)", tt.id, name, color, tt.wantName, tt.wantColor)
		}
	}
}
