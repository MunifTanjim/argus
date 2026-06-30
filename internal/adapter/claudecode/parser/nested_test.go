package parser_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

// writeNestedFixture builds a session where subagent A (depth 1) spawns
// subagent B (depth 2). Returns the session root path.
func writeNestedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "sess.jsonl")
	mustWrite(t, root, []string{
		`{"uuid":"u1","type":"user","timestamp":"2025-06-15T10:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"uuid":"a1","type":"assistant","timestamp":"2025-06-15T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-gp","name":"Task","input":{"subagent_type":"general-purpose","description":"do nested"}}]}}`,
		`{"uuid":"r1","type":"user","timestamp":"2025-06-15T10:00:30Z","isMeta":true,"sourceToolUseID":"tool-gp","toolUseResult":{"agentId":"A","status":"completed"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-gp","content":"done"}]}}`,
	})
	sd := filepath.Join(dir, "sess", "subagents")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(sd, "agent-A.jsonl"), []string{
		`{"uuid":"au1","type":"user","timestamp":"2025-06-15T10:00:02Z","isSidechain":true,"message":{"role":"user","content":"do nested"}}`,
		`{"uuid":"aa1","type":"assistant","timestamp":"2025-06-15T10:00:03Z","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-ex","name":"Task","input":{"subagent_type":"Explore","description":"find entrypoint"}}]}}`,
		`{"uuid":"ar1","type":"user","timestamp":"2025-06-15T10:00:20Z","isSidechain":true,"isMeta":true,"sourceToolUseID":"tool-ex","toolUseResult":{"agentId":"B","status":"completed"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-ex","content":"found"}]}}`,
	})
	mustWrite(t, filepath.Join(sd, "agent-A.meta.json"), []string{
		`{"agentType":"general-purpose","description":"do nested","toolUseId":"tool-gp","spawnDepth":1}`,
	})
	mustWrite(t, filepath.Join(sd, "agent-B.jsonl"), []string{
		`{"uuid":"bu1","type":"user","timestamp":"2025-06-15T10:00:04Z","isSidechain":true,"message":{"role":"user","content":"find entrypoint"}}`,
		`{"uuid":"ba1","type":"assistant","timestamp":"2025-06-15T10:00:19Z","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"entrypoint is main.go"}]}}`,
	})
	mustWrite(t, filepath.Join(sd, "agent-B.meta.json"), []string{
		`{"agentType":"Explore","description":"find entrypoint","toolUseId":"tool-ex","spawnDepth":2}`,
	})
	return root
}

func mustWrite(t *testing.T, path string, lines []string) {
	t.Helper()
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChildAgentRefs_NestedFile(t *testing.T) {
	root := writeNestedFixture(t)
	subA := filepath.Join(filepath.Dir(root), "sess", "subagents", "agent-A.jsonl")
	refs := parser.ChildAgentRefs(subA, root)
	if refs["tool-ex"] != "B" {
		t.Fatalf("ChildAgentRefs[tool-ex] = %q, want %q (refs=%v)", refs["tool-ex"], "B", refs)
	}
}

func TestAgentIDsByToolID_TopLevel(t *testing.T) {
	root := writeNestedFixture(t)
	refs := parser.AgentIDsByToolID(root)
	if refs["tool-gp"] != "A" {
		t.Fatalf("AgentIDsByToolID[tool-gp] = %q, want %q", refs["tool-gp"], "A")
	}
}

func TestSpawnDepth(t *testing.T) {
	root := writeNestedFixture(t)
	if d := parser.SpawnDepth(root, "A"); d != 1 {
		t.Errorf("SpawnDepth(A) = %d, want 1", d)
	}
	if d := parser.SpawnDepth(root, "B"); d != 2 {
		t.Errorf("SpawnDepth(B) = %d, want 2", d)
	}
	if d := parser.SpawnDepth(root, "missing"); d != 0 {
		t.Errorf("SpawnDepth(missing) = %d, want 0", d)
	}
}

func TestAgentIDFromPath(t *testing.T) {
	if got := parser.AgentIDFromPath("/x/sess/subagents/agent-A.jsonl"); got != "A" {
		t.Errorf("AgentIDFromPath = %q, want A", got)
	}
}

func TestReadSubagentSession_ClearsSidechain(t *testing.T) {
	root := writeNestedFixture(t)
	subB := filepath.Join(filepath.Dir(root), "sess", "subagents", "agent-B.jsonl")
	chunks, err := parser.ReadSubagentSession(subB)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("ReadSubagentSession dropped all (sidechain not cleared)")
	}
}
