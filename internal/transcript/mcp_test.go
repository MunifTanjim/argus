package transcript

import "testing"

func TestMCPDisplayName(t *testing.T) {
	for _, c := range []struct {
		name, display string
		ok            bool
	}{
		{"mcp__github__create_issue", "github › create_issue", true},
		{"mcp__codex_apps__pets__create", "codex_apps › pets__create", true},
		{"mcp__server", "", false},
		{"mcp____tool", "", false},
		{"exec_command", "", false},
	} {
		d, ok := MCPDisplayName(c.name)
		if d != c.display || ok != c.ok {
			t.Errorf("MCPDisplayName(%q) = (%q, %v), want (%q, %v)", c.name, d, ok, c.display, c.ok)
		}
	}
}
