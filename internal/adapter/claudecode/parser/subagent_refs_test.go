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

// writeRunningSubagent writes a subagent whose meta.json sidecar exists (spawn
// time) but whose parent session has NO tool_result yet — the still-running case.
func writeRunningSubagent(t *testing.T, agentID, toolID, content string) string {
	t.Helper()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "parent.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "parent", "subagents")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".jsonl"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	meta := `{"agentType":"Explore","description":"d","toolUseId":"` + toolID + `"}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".meta.json"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}
	return sessionPath
}

func TestMetaAgentRefs_RunningSubagent(t *testing.T) {
	sessionPath := writeRunningSubagent(t, "bbb", "tool-9", "{\"x\":1}\n")

	// Link-based refs are empty (no tool_result), but meta-based refs find it.
	if got := parser.AgentRefsFromLinks(sessionPath, map[string]string{}); len(got) != 0 {
		t.Fatalf("AgentRefsFromLinks = %v, want empty", got)
	}
	refs := parser.MetaAgentRefs(sessionPath, nil)
	if refs["tool-9"] != "bbb" {
		t.Fatalf("MetaAgentRefs[tool-9] = %q, want bbb", refs["tool-9"])
	}
}

func TestMetaAgentRefs_EmptyJSONLNotLinked(t *testing.T) {
	// Sidecar present but the .jsonl has no content — nothing to drill into yet.
	sessionPath := writeRunningSubagent(t, "ccc", "tool-1", "")
	if refs := parser.MetaAgentRefs(sessionPath, nil); len(refs) != 0 {
		t.Fatalf("MetaAgentRefs = %v, want empty for empty jsonl", refs)
	}
}

func TestMetaAgentRefs_CacheAvoidsReread(t *testing.T) {
	sessionPath := writeRunningSubagent(t, "ddd", "tool-2", "{\"x\":1}\n")
	cache := map[string]string{}
	parser.MetaAgentRefs(sessionPath, cache)
	if cache["ddd"] != "tool-2" {
		t.Fatalf("cache[ddd] = %q, want tool-2", cache["ddd"])
	}

	// Meta content is immutable in practice; rewriting it proves the cached value
	// is reused (no re-read) rather than re-parsed from disk.
	meta := filepath.Join(filepath.Dir(sessionPath), "parent", "subagents", "agent-ddd.meta.json")
	if err := os.WriteFile(meta, []byte(`{"toolUseId":"CHANGED"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if refs := parser.MetaAgentRefs(sessionPath, cache); refs["tool-2"] != "ddd" {
		t.Fatalf("cached MetaAgentRefs[tool-2] = %q, want ddd (stale-cache hit)", refs["tool-2"])
	}
}

// A not-yet-written meta.json must not be negatively cached: once the sidecar
// appears, the next call links the subagent.
func TestMetaAgentRefs_EmptyMetaNotCached(t *testing.T) {
	sessionPath := writeRunningSubagent(t, "eee", "tool-3", "{\"x\":1}\n")
	meta := filepath.Join(filepath.Dir(sessionPath), "parent", "subagents", "agent-eee.meta.json")
	if err := os.WriteFile(meta, []byte(`{}`), 0644); err != nil { // no toolUseId yet
		t.Fatal(err)
	}
	cache := map[string]string{}
	if refs := parser.MetaAgentRefs(sessionPath, cache); len(refs) != 0 {
		t.Fatalf("MetaAgentRefs = %v, want empty while meta lacks toolUseId", refs)
	}
	if _, cached := cache["eee"]; cached {
		t.Fatal("empty toolUseId was negatively cached")
	}

	if err := os.WriteFile(meta, []byte(`{"toolUseId":"tool-3"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if refs := parser.MetaAgentRefs(sessionPath, cache); refs["tool-3"] != "eee" {
		t.Fatalf("MetaAgentRefs[tool-3] = %q, want eee after meta written", refs["tool-3"])
	}
}
