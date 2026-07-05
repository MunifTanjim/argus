package tui

import "testing"

func TestToolRegistryAttribution(t *testing.T) {
	// Every entry belongs to a known agent.
	for name, meta := range toolRegistry {
		switch meta.agent {
		case agentClaude, agentCodex, agentAntigravity:
		default:
			t.Errorf("%q: unknown agent %q", name, meta.agent)
		}
	}
}

func TestToolRegistryCoversKnownTools(t *testing.T) {
	// Tool names observed in real agy/codex transcripts. New tools should be
	// registered so they get proper attribution, icon, color, and name.
	known := []string{
		// antigravity
		"run_command", "grep_search", "list_dir", "view_file", "write_to_file",
		"replace_file_content", "multi_replace_file_content", "search_web",
		"generate_image", "invoke_subagent", "define_subagent", "manage_subagents",
		"manage_task", "ask_question", "ask_permission", "list_permissions",
		"send_message", "schedule",
		// codex
		"exec_command", "apply_patch", "update_plan", "view_image", "web_search",
		"wait_agent", "close_agent", "spawn_agent",
		// claude code
		"Read", "Edit", "MultiEdit", "Write", "Bash", "Grep", "Glob", "LS",
		"WebFetch", "WebSearch", "AskUserQuestion", "ExitPlanMode", "TodoWrite",
		"TaskCreate", "TaskUpdate", "TaskList", "ToolSearch", "LSP",
		"Task", "Agent", "Skill",
	}
	for _, name := range known {
		if _, ok := toolRegistry[name]; !ok {
			t.Errorf("tool %q missing from registry", name)
		}
	}
}

func TestToolRegistryDrivesIconColorName(t *testing.T) {
	if got := toolDisplayName("run_command"); got != "Run Command" {
		t.Errorf("display = %q, want Run Command", got)
	}
	if toolColor("run_command") != categoryColor(catBash) {
		t.Error("run_command color should resolve via catBash")
	}
	if toolIcon("run_command", false) != categoryIcon(catBash) {
		t.Error("run_command icon should resolve via catBash")
	}
	if toolColor("grep_search") != categoryColor(catGrep) {
		t.Error("grep_search color should resolve via catGrep")
	}
}

func TestToolRegistryDetailRenderers(t *testing.T) {
	// Tools with a dedicated detail body must keep their renderer wired.
	withDetail := []string{
		"run_command", "grep_search", "view_file", "write_to_file",
		"exec_command", "update_plan", "web_search", "wait_agent", "close_agent",
		"Bash", "Read", "Edit", "Grep", "Glob", "AskUserQuestion",
		"TodoWrite", "TaskCreate", "TaskUpdate",
	}
	for _, name := range withDetail {
		if toolRegistry[name].detail == nil {
			t.Errorf("%q should have a detail renderer", name)
		}
	}
	// Subagent-view tools never carry a toolDetailBody renderer.
	for _, name := range []string{"invoke_subagent", "spawn_agent", "Task", "Agent"} {
		if toolRegistry[name].detail != nil {
			t.Errorf("%q should render via the subagent view, not a detail body", name)
		}
	}
}
