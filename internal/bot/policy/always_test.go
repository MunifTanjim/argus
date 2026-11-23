package policy

import (
	"encoding/json"
	"testing"
)

func TestAlwaysRule(t *testing.T) {
	cmd := func(c string) json.RawMessage { b, _ := json.Marshal(map[string]string{"command": c}); return b }
	cases := []struct {
		tool   string
		input  json.RawMessage
		target string
		ok     bool
	}{
		{"Bash", cmd("git status"), "git status", true},
		{"Bash", cmd("rm foo"), "rm foo", true},
		{"Bash", cmd("make"), "", false},
		{"Bash", cmd("cd x && npm test"), "", false},
		{"Bash", cmd("ls *.go"), "", false},
		{"bash", nil, "", false},
		{"apply_patch", nil, "", false},
		{"Write", json.RawMessage(`{"file_path":"notes/a.md","content":"x"}`), "notes/a.md", true},
		{"apply_patch", json.RawMessage(`{"input":"*** Begin Patch\n*** Update File: a.go\n*** Update File: b.go\n*** End Patch"}`), "", false},
		{"WebFetch", json.RawMessage(`{"url":"https://example.com/x"}`), "example.com", true},
		{"mcp__slack__post_message", json.RawMessage(`{"text":"hi"}`), "post_message", true},
		{"WebSearch", json.RawMessage(`{"query":"x"}`), "", false},
		{"", json.RawMessage(`{"x":1}`), "", false},
	}
	for _, c := range cases {
		r, ok := AlwaysRule(Describe("claude", c.tool, c.input))
		if ok != c.ok || ok && (r.Tool != c.tool || r.Target != c.target || r.Decision != OutcomeAllow) {
			t.Errorf("%s %s = %+v %v", c.tool, c.input, r, ok)
		}
	}
}

func TestOpaque(t *testing.T) {
	for _, c := range []struct {
		tool, input string
		want        bool
	}{
		{"bash", "", true},
		{"bash", "{}", true},
		{"apply_patch", "", true},
		{"", `{"x":1}`, true},
		{"Bash", `{"description":"x"}`, true},
		{"Bash", `{"command":"ls"}`, false},
		{"permissions", `{"network":true}`, false},
	} {
		if got := Opaque(Describe("opencode", c.tool, json.RawMessage(c.input))); got != c.want {
			t.Errorf("%s %s = %v", c.tool, c.input, got)
		}
	}
}
