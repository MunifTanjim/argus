package claudecode

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

func TestFoldItem_SkillToolBecomesItemSkill(t *testing.T) {
	pit := parser.DisplayItem{
		Type:        parser.ItemToolCall,
		ToolName:    "Skill",
		ToolID:      "call_1",
		ToolSummary: "superpowers:systematic-debugging",
		ToolResult:  "# Systematic Debugging\n\nFind root cause first.",
	}

	it, ok := foldItem(pit, nil, nil, 0)
	if !ok {
		t.Fatal("foldItem dropped the Skill item")
	}
	if it.Kind != ItemSkill {
		t.Errorf("Kind = %q, want ItemSkill (not a subagent or plain tool)", it.Kind)
	}
	if it.ToolName != "Skill" {
		t.Errorf("ToolName = %q, want Skill", it.ToolName)
	}
	if it.InputPreview != "superpowers:systematic-debugging" {
		t.Errorf("InputPreview = %q, want the skill identifier", it.InputPreview)
	}
	if it.Result != "# Systematic Debugging\n\nFind root cause first." {
		t.Errorf("Result = %q, want the loaded skill body (for drill)", it.Result)
	}
	if len(it.Subagents) != 0 {
		t.Errorf("Subagents = %v, want none (Skill is not a spawn)", it.Subagents)
	}
}

func TestFoldItem_TaskToolStaysSubagent(t *testing.T) {
	pit := parser.DisplayItem{
		Type:         parser.ItemSubagent,
		ToolName:     "Task",
		ToolID:       "call_1",
		SubagentType: "Explore",
	}

	it, ok := foldItem(pit, nil, nil, 0)
	if !ok {
		t.Fatal("foldItem dropped the Task item")
	}
	if it.Kind != ItemSubagent {
		t.Errorf("Kind = %q, want ItemSubagent", it.Kind)
	}
}
