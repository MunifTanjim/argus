package tui

import "testing"

func TestToolRegistryAttribution(t *testing.T) {
	// The registry holds Antigravity and Codex tools; Claude Code tools still live
	// in the switches. Guards against a mis-attributed entry.
	for name, meta := range toolRegistry {
		if meta.agent != agentAntigravity && meta.agent != agentCodex {
			t.Errorf("%q: agent = %q, want antigravity or codex", name, meta.agent)
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

func TestToolRegistryDetailCoverage(t *testing.T) {
	// These intentionally have no toolDetailBody renderer: invoke_subagent and
	// spawn_agent render via the subagent view (ItemSubagent); apply_patch and
	// view_image fall to the generic Input/Result body. Every other tool has one.
	noDetail := map[string]bool{
		"invoke_subagent": true,
		"spawn_agent":     true,
		"apply_patch":     true,
		"view_image":      true,
	}
	for name, meta := range toolRegistry {
		hasDetail := meta.detail != nil
		if noDetail[name] && hasDetail {
			t.Errorf("%q should not have a detail renderer", name)
		}
		if !noDetail[name] && !hasDetail {
			t.Errorf("%q should have a detail renderer", name)
		}
	}
}
