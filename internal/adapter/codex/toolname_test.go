package codex

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestCodexToolName(t *testing.T) {
	for _, c := range []struct{ ns, name, want string }{
		{"", "exec_command", "exec_command"},
		{"functions", "exec_command", "exec_command"},
		{"collaboration", "send_message", "collaboration.send_message"},
		{"multi_agent_v1", "spawn_agent", "spawn_agent"},
		{"clock", "sleep", "clock.sleep"},
		{"mcp__github__", "create_issue", "mcp__github__create_issue"},
		{"mcp__github", "create_issue", "mcp__github__create_issue"},
	} {
		if got := codexToolName(c.ns, c.name); got != c.want {
			t.Errorf("codexToolName(%q, %q) = %q, want %q", c.ns, c.name, got, c.want)
		}
	}
}

// v2's wait_agent names no targets, so it is a plain tool row, not a subagent op.
func TestV2ToolsAreNamespacedTools(t *testing.T) {
	lines := []rolloutLine{
		{Type: "response_item", Payload: rolloutPayload{Type: "function_call", Namespace: "collaboration", Name: "wait_agent", CallID: "w", Arguments: []byte(`"{\"timeout_ms\":30000}"`)}},
		{Type: "response_item", Payload: rolloutPayload{Type: "function_call", Namespace: "collaboration", Name: "send_message", CallID: "s", Arguments: []byte(`"{\"target\":\"/root/a\",\"message\":\"hi\"}"`)}},
		{Type: "response_item", Payload: rolloutPayload{Type: "function_call", Namespace: "clock", Name: "sleep", CallID: "c", Arguments: []byte(`"{\"duration_ms\":10000}"`)}},
	}
	got := map[string]transcript.Entry{}
	for _, e := range foldRollout(lines, nil, true) {
		if e.ToolName != "" {
			got[e.ToolName] = e
		}
	}
	for name, preview := range map[string]string{
		"collaboration.wait_agent":   "up to 30s",
		"collaboration.send_message": "/root/a",
		"clock.sleep":                "10s",
	} {
		e, ok := got[name]
		if !ok || e.Kind != transcript.EntryTool || e.InputPreview != preview {
			t.Errorf("%s: entry %+v; want a tool row previewing %q", name, e, preview)
		}
	}
}
