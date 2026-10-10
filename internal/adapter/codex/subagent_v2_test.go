package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/transcript"
)

const (
	v2Parent = "01a10a8f-42ca-72f0-8069-7cf2a7b823ed"
	v2Child  = "01a123c9-4e62-75e0-856b-b123ec134a50"
)

// writeV2Fixture lays out a multi-agent v2 session: the parent's spawn_agent
// returns only the agent path, the child is a fork that copies the parent's
// history, and Codex's state DB maps the path to the child thread.
func writeV2Fixture(t *testing.T) (parentPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	day := filepath.Join(home, "sessions", "2026", "10", "10")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	parentPath = filepath.Join(day, "rollout-2026-10-10T03-00-00-"+v2Parent+".jsonl")
	parent := `{"timestamp":"t","type":"session_meta","payload":{"id":"` + v2Parent + `","cwd":"/w"}}
{"timestamp":"t","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"parent question"}]}}
{"timestamp":"t","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","namespace":"collaboration","arguments":"{\"task_name\":\"sleep_demo\",\"fork_turns\":\"all\",\"message\":\"gAAAAABqyaxdL2Gm8cyKE\"}","call_id":"c1","internal_chat_message_metadata_passthrough":{"turn_id":"u7"}}}
{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"{\"task_name\":\"/root/sleep_demo\"}"}}
`
	child := `{"timestamp":"t","type":"session_meta","payload":{"id":"` + v2Child + `","forked_from_id":"` + v2Parent + `","thread_source":"subagent","cwd":"/w"}}
{"timestamp":"t","type":"session_meta","payload":{"id":"` + v2Parent + `","cwd":"/w"}}
{"timestamp":"t","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"parent question"}]}}
{"timestamp":"t","type":"event_msg","payload":{"type":"thread_settings_applied","thread_id":"` + v2Child + `"}}
{"timestamp":"t","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"child done"}]}}
`
	if err := os.WriteFile(parentPath, []byte(parent), 0o600); err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(day, "rollout-2026-10-10T03-09-17-"+v2Child+".jsonl")
	if err := os.WriteFile(childPath, []byte(child), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT, status TEXT);
		CREATE TABLE threads (id TEXT, agent_path TEXT, agent_nickname TEXT, rollout_path TEXT);
		INSERT INTO thread_spawn_edges VALUES ('` + v2Parent + `','` + v2Child + `','open');
		INSERT INTO threads VALUES ('` + v2Child + `','/root/sleep_demo','Newton','` + childPath + `');`); err != nil {
		t.Fatal(err)
	}
	return parentPath
}

func TestV2SpawnLinksChildByAgentPath(t *testing.T) {
	view, err := ReadTranscriptView(writeV2Fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var sub *transcript.Subagent
	for i := range view.Entries {
		if e := &view.Entries[i]; baseToolName(e.ToolName) == "spawn_agent" && len(e.Subagents) > 0 {
			sub = &e.Subagents[0]
		}
	}
	if sub == nil {
		t.Fatal("no spawn_agent entry")
	}
	if sub.ID != v2Child || sub.Name != "Newton" || sub.Status != "open" || !sub.HasTrace {
		t.Fatalf("subagent = %+v; want linked child %s (Newton, open, has trace)", *sub, v2Child)
	}
	if strings.HasPrefix(sub.Desc, "gAAAA") || sub.Desc != "sleep_demo" {
		t.Fatalf("desc = %q; want the task name, not the encrypted message", sub.Desc)
	}
}

func TestV2SubagentViewSkipsForkedParentHistory(t *testing.T) {
	view, ok, err := ReadSubagentView(writeV2Fixture(t), v2Child)
	if err != nil || !ok {
		t.Fatalf("ReadSubagentView: ok=%v err=%v", ok, err)
	}
	var texts []string
	for _, e := range view.Entries {
		if e.Text != "" {
			texts = append(texts, e.Text)
		}
	}
	if got := strings.Join(texts, "|"); got != "child done" {
		t.Fatalf("subagent view texts = %q; want only the child's own work", got)
	}
}

// The daemon answers before the state DB: with no DB at all, the spawn still
// links through the daemon's subAgentActivity item and thread record.
func TestV2SpawnLinksThroughDaemon(t *testing.T) {
	childRollout := ""
	st := &daemonState{threads: map[string]map[string]any{}}
	f := newFakeDaemon(t, func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "thread/items/list":
			return map[string]any{"data": []any{map[string]any{"turnId": "u7", "item": map[string]any{
				"type": "subAgentActivity", "id": "c1", "kind": "started",
				"agentThreadId": v2Child, "agentPath": "/root/sleep_demo"}}}}, nil
		case "thread/read":
			if requestThreadID(params) == v2Child {
				return map[string]any{"thread": map[string]any{"id": v2Child, "path": childRollout, "agentNickname": "Newton"}}, nil
			}
		}
		return st.handle(method, params)
	})
	d := newDiscoverer(registry.New(), nil)
	d.sockPath = func() (string, error) { return f.sock, nil }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.ctx = ctx
	go d.runPump(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for d.conn() == nil {
		if time.Now().After(deadline) {
			t.Fatal("discoverer never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	parent := writeV2Fixture(t)
	childRollout = filepath.Join(filepath.Dir(parent), "rollout-2026-10-10T03-09-17-"+v2Child+".jsonl")
	if err := os.Remove(filepath.Join(os.Getenv("CODEX_HOME"), "state_5.sqlite")); err != nil {
		t.Fatal(err)
	}
	view, err := ReadTranscriptView(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range view.Entries {
		if baseToolName(e.ToolName) == "spawn_agent" {
			if s := e.Subagents[0]; s.ID != v2Child || s.Name != "Newton" || !s.HasTrace {
				t.Fatalf("subagent = %+v; want linked through the daemon", s)
			}
			return
		}
	}
	t.Fatal("no spawn_agent entry")
}

// The parent rollout's SubAgentActivity items link a v2 spawn by call id and give
// the child's progress: an open spawn edge does not mask "completed", a closed
// one wins.
func TestV2SubagentStatusFromActivity(t *testing.T) {
	parent := writeV2Fixture(t)
	act := func(kind string) string {
		return `{"timestamp":"t","type":"event_msg","payload":{"type":"item_completed","item":{"type":"SubAgentActivity","id":"c1","kind":"` + kind + `","agent_thread_id":"` + v2Child + `","agent_path":"/root/sleep_demo"}}}` + "\n"
	}
	f, err := os.OpenFile(parent, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(act("started") + act("completed")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	status := func() string {
		view, err := ReadTranscriptView(parent)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range view.Entries {
			if baseToolName(e.ToolName) == "spawn_agent" {
				if e.Subagents[0].ID != v2Child {
					t.Fatalf("spawn not linked: %+v", e.Subagents[0])
				}
				return e.Subagents[0].Status
			}
		}
		t.Fatal("no spawn_agent entry")
		return ""
	}
	if got := status(); got != "completed" {
		t.Fatalf("status = %q, want completed (the open edge must not mask it)", got)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(os.Getenv("CODEX_HOME"), "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE thread_spawn_edges SET status = 'closed'`); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != "closed" {
		t.Fatalf("status = %q, want closed", got)
	}
}

func TestAgentMessageStartsTurn(t *testing.T) {
	msg := func(content string) rolloutLine {
		var p rolloutPayload
		if err := json.Unmarshal([]byte(`{"type":"agent_message","author":"/root","content":`+content+`}`), &p); err != nil {
			t.Fatal(err)
		}
		return rolloutLine{Type: "response_item", Payload: p}
	}
	entries := foldRollout([]rolloutLine{
		msg(`[{"type":"input_text","text":"Message Type: NEW_TASK\nTask name: /root/a\nSender: /root\nPayload:\n"},{"type":"encrypted_content","encrypted_content":"gAAAAAB"}]`),
		msg(`[{"type":"input_text","text":"Message Type: MESSAGE\nSender: /root\nPayload:\nrun the tests"}]`),
	}, nil, true)
	var got []string
	for _, e := range entries {
		if e.Kind == transcript.EntryUser {
			got = append(got, e.Text)
		}
	}
	want := []string{"New task from /root (message encrypted)", "Message from /root\n\nrun the tests"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("user rows = %q, want %q", got, want)
	}
}
