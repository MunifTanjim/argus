package tui

import "testing"

func TestToolRegistryAntigravityAttribution(t *testing.T) {
	// Every registered tool is currently Antigravity's; Claude/Codex tools still
	// live in the switches. Guards against a mis-attributed entry.
	for name, meta := range toolRegistry {
		if meta.agent != agentAntigravity {
			t.Errorf("%q: agent = %q, want %q", name, meta.agent, agentAntigravity)
		}
	}
}

func TestToolRegistryCoversKnownAntigravityTools(t *testing.T) {
	// The tool names observed in real agy transcripts. New agy tools should be
	// registered so they get proper attribution, icon, color, and name.
	known := []string{
		"run_command", "grep_search", "list_dir", "view_file", "write_to_file",
		"replace_file_content", "multi_replace_file_content", "search_web",
		"generate_image", "invoke_subagent", "define_subagent", "manage_subagents",
		"manage_task", "ask_question", "ask_permission", "list_permissions",
		"send_message", "schedule",
	}
	for _, name := range known {
		if _, ok := toolRegistry[name]; !ok {
			t.Errorf("antigravity tool %q missing from registry", name)
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
	// invoke_subagent renders via the subagent view (ItemSubagent), so it
	// intentionally has no toolDetailBody renderer; every other registered tool
	// has a custom one.
	noDetail := map[string]bool{"invoke_subagent": true}
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
