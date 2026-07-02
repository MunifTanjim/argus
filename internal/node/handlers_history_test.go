package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
)

// writeHistoryNestedFixture writes a session (general-purpose A → Explore B)
// under a temp projects root and points HOME at it so safeProjectsPath accepts
// the path. Returns the session transcript path.
func writeHistoryNestedFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, ".claude", "projects", "demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(proj, "sess.jsonl")
	write := func(path string, lines ...string) {
		content := ""
		for _, l := range lines {
			content += l + "\n"
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root,
		`{"uuid":"u1","type":"user","timestamp":"2025-06-15T10:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"uuid":"a1","type":"assistant","timestamp":"2025-06-15T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-gp","name":"Task","input":{"subagent_type":"general-purpose","description":"d"}}]}}`,
		`{"uuid":"r1","type":"user","timestamp":"2025-06-15T10:00:30Z","isMeta":true,"sourceToolUseID":"tool-gp","toolUseResult":{"agentId":"A"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-gp","content":"done"}]}}`,
	)
	sd := filepath.Join(proj, "sess", "subagents")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(sd, "agent-A.jsonl"),
		`{"uuid":"au1","type":"user","timestamp":"2025-06-15T10:00:02Z","isSidechain":true,"message":{"role":"user","content":"d"}}`,
		`{"uuid":"aa1","type":"assistant","timestamp":"2025-06-15T10:00:03Z","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-ex","name":"Task","input":{"subagent_type":"Explore","description":"e"}}]}}`,
		`{"uuid":"ar1","type":"user","timestamp":"2025-06-15T10:00:20Z","isSidechain":true,"isMeta":true,"sourceToolUseID":"tool-ex","toolUseResult":{"agentId":"B"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-ex","content":"f"}]}}`,
	)
	write(filepath.Join(sd, "agent-A.meta.json"), `{"agentType":"general-purpose","spawnDepth":1}`)
	write(filepath.Join(sd, "agent-B.jsonl"),
		`{"uuid":"bu1","type":"user","timestamp":"2025-06-15T10:00:04Z","isSidechain":true,"message":{"role":"user","content":"e"}}`,
	)
	return root
}

func subagentItemInChunks(chunks []claudecode.Chunk) (claudecode.Item, bool) {
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.Kind == claudecode.ItemSubagent {
				return it, true
			}
		}
	}
	return claudecode.Item{}, false
}

func TestHandleHistoryTranscript_NestedByAgentID(t *testing.T) {
	root := writeHistoryNestedFixture(t)
	d := &Node{adapterList: []adapter.Adapter{claudecode.New()}}
	params, _ := json.Marshal(api.HistoryTranscriptParams{TranscriptPath: root, AgentID: "A"})
	res, err := d.handleHistoryTranscript(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	view := res.(claudecode.TranscriptView)
	it, ok := subagentItemInChunks(view.Chunks)
	if !ok || it.AgentID != "B" {
		t.Fatalf("nested view should expose child B, got ok=%v agentID=%q", ok, it.AgentID)
	}
}

func TestHandleHistoryTranscript_UnknownAgentID(t *testing.T) {
	root := writeHistoryNestedFixture(t)
	d := &Node{adapterList: []adapter.Adapter{claudecode.New()}}
	params, _ := json.Marshal(api.HistoryTranscriptParams{TranscriptPath: root, AgentID: "nope"})
	if _, err := d.handleHistoryTranscript(context.Background(), params); err == nil {
		t.Fatal("expected error for unknown agent_id")
	}
}
