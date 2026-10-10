package tui

import "testing"

func TestToolRegistryCoversKnownTools(t *testing.T) {
	// Tool names observed in real transcripts, per agent. New tools should be
	// registered so they get proper icon, color, and name.
	known := map[string][]string{
		agentAntigravity: {"run_command", "grep_search", "list_dir", "view_file", "write_to_file",
			"replace_file_content", "multi_replace_file_content", "search_web",
			"generate_image", "invoke_subagent", "define_subagent", "manage_subagents",
			"manage_task", "ask_question", "ask_permission", "list_permissions",
			"send_message", "schedule"},
		agentCodex: {"exec_command", "apply_patch", "update_plan", "view_image", "web_search",
			"wait_agent", "close_agent", "spawn_agent",
			"exec", "request_user_input", "request_user_input_async", "Skill"},
		agentClaude: {"Read", "Edit", "MultiEdit", "Write", "Bash", "Grep", "Glob", "LS",
			"WebFetch", "WebSearch", "AskUserQuestion", "ExitPlanMode", "TodoWrite",
			"TaskCreate", "TaskUpdate", "TaskList", "ToolSearch", "LSP",
			"Task", "Agent", "Skill"},
	}
	for agent, names := range known {
		for _, name := range names {
			if _, ok := toolRegistry[agent][name]; !ok {
				t.Errorf("%s tool %q missing from registry", agent, name)
			}
		}
	}
}

func TestToolRegistryDrivesIconColorName(t *testing.T) {
	if got := toolDisplayName(agentAntigravity, "run_command"); got != "Run Command" {
		t.Errorf("display = %q, want Run Command", got)
	}
	if toolIcon(agentAntigravity, "run_command", false) != categoryIcon(catBash) {
		t.Error("run_command icon should resolve via catBash")
	}
}

func TestToolRegistryDetailRenderers(t *testing.T) {
	// Tools with a dedicated detail body must keep their renderer wired.
	withDetail := map[string][]string{
		agentAntigravity: {"run_command", "grep_search", "view_file", "write_to_file"},
		agentCodex: {"exec_command", "update_plan", "web_search", "wait_agent", "close_agent",
			"apply_patch", "view_image", "exec", "request_user_input", "request_user_input_async"},
		agentClaude: {"Bash", "Read", "Edit", "Grep", "Glob", "AskUserQuestion",
			"TodoWrite", "TaskCreate", "TaskUpdate"},
	}
	for agent, names := range withDetail {
		for _, name := range names {
			if toolRegistry[agent][name].detail == nil {
				t.Errorf("%s %q should have a detail renderer", agent, name)
			}
		}
	}
	// Subagent-view tools never carry a toolDetailBody renderer.
	for agent, name := range map[string]string{agentAntigravity: "invoke_subagent", agentCodex: "spawn_agent", agentClaude: "Agent"} {
		if toolRegistry[agent][name].detail != nil {
			t.Errorf("%s %q should render via the subagent view, not a detail body", agent, name)
		}
	}
}

// One name may mean different tools for different agents: lookup takes the
// session's agent's entry, and only an unknown agent falls back to any entry.
func TestLookupToolIsAgentAware(t *testing.T) {
	saved := toolRegistry[agentCodex]["send_message"]
	toolRegistry[agentCodex]["send_message"] = toolMeta{display: "Codex Send", category: catTask}
	t.Cleanup(func() {
		if saved.display == "" && saved.detail == nil {
			delete(toolRegistry[agentCodex], "send_message")
		} else {
			toolRegistry[agentCodex]["send_message"] = saved
		}
	})
	if got := toolDisplayName(agentCodex, "send_message"); got != "Codex Send" {
		t.Errorf("codex send_message = %q, want codex's entry", got)
	}
	if got := toolDisplayName(agentAntigravity, "send_message"); got != "Send Message" {
		t.Errorf("antigravity send_message = %q, want antigravity's entry", got)
	}
	// A known agent does not borrow another agent's tool.
	if _, ok := lookupTool(agentOpenCode, "run_command"); ok {
		t.Error("opencode must not resolve antigravity's run_command")
	}
	// An unknown agent falls back to any agent's entry.
	if got := toolDisplayName("", "run_command"); got != "Run Command" {
		t.Errorf("unknown-agent run_command = %q, want the fallback entry", got)
	}
	// MCP tools resolve for every agent.
	if _, ok := lookupTool(agentClaude, "mcp__github__create_issue"); !ok {
		t.Error("MCP tools should resolve for any agent")
	}
}
