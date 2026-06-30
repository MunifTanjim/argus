package parser_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

// writeFixtureSessionWithSubagent writes a parent session (Task tool-1, result
// linking agentId "aaa") plus its subagents/agent-aaa.jsonl. Returns the parent path.
func writeFixtureSessionWithSubagent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "parent.jsonl")

	parentLines := `{"uuid":"a1","type":"assistant","timestamp":"2025-06-15T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"Task","input":{"subagent_type":"general-purpose","description":"Do something"}}],"model":"claude-sonnet-4-20250514","stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"uuid":"r1","type":"user","timestamp":"2025-06-15T10:00:05Z","isMeta":true,"sourceToolUseID":"tool-1","toolUseResult":{"agentId":"aaa","status":"completed"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"Done."}]}}
`
	if err := os.WriteFile(sessionPath, []byte(parentLines), 0644); err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(dir, "parent", "subagents")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	agentLines := `{"uuid":"u1","type":"user","isSidechain":true,"timestamp":"2025-06-15T10:00:01Z","message":{"role":"user","content":"Do something"}}
{"uuid":"a1","type":"assistant","isSidechain":true,"timestamp":"2025-06-15T10:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`
	if err := os.WriteFile(filepath.Join(subDir, "agent-aaa.jsonl"), []byte(agentLines), 0644); err != nil {
		t.Fatal(err)
	}

	return sessionPath
}

func TestAgentIDsByToolID(t *testing.T) {
	sessionPath := writeFixtureSessionWithSubagent(t)
	refs := parser.AgentIDsByToolID(sessionPath)
	if refs["tool-1"] != "aaa" {
		t.Fatalf("refs[tool-1] = %q, want aaa", refs["tool-1"])
	}
}

func TestSubagentFilePath(t *testing.T) {
	sessionPath := writeFixtureSessionWithSubagent(t)
	p, ok := parser.SubagentFilePath(sessionPath, "aaa")
	if !ok {
		t.Fatal("expected subagent file to exist")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("resolved path not statable: %v", err)
	}
	if _, ok := parser.SubagentFilePath(sessionPath, "nope"); ok {
		t.Fatal("expected missing agent to report not-exist")
	}
}
