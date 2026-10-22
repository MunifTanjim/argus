package node

import (
	"context"

	"encoding/json"
	"github.com/MunifTanjim/argus/internal/api"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/mcphttp"
)

func toolNode(t *testing.T) (*Node, mcpBackend) {
	t.Helper()
	d, _ := meshNode(t)
	d.id = "home"
	d.addAgentSession("codex", "t1")
	d.addAgentSession("codex", "t2")
	return d, mcpBackend{d}
}

func codexMeta(id string) map[string]json.RawMessage {
	return rawMeta(map[string]string{"sessionId": id})
}

func TestMCPToolsListHasTenTools(t *testing.T) {
	var names []string
	for _, tool := range (mcpBackend{}).Tools() {
		names = append(names, tool.Name)
		if tool.Description == "" || !strings.Contains(string(tool.InputSchema), `"additionalProperties":false`) {
			t.Errorf("%s: incomplete definition", tool.Name)
		}
	}
	want := "whoami list show spawn send reply respond output transcript kill"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("tools = %s", got)
	}
}

func TestMCPWhoamiComposite(t *testing.T) {
	_, b := toolNode(t)
	res := b.Call(t.Context(), "codex", "whoami", codexMeta("t1"), json.RawMessage(`{}`))
	if res.IsError || !strings.Contains(res.Text, `"id":"home:default:%t1"`) {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPSendQueuesFromMetaCaller(t *testing.T) {
	d, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:default:%t2","text":"hello"}`)
	res := b.Call(t.Context(), "codex", "send", codexMeta("t1"), args)
	if res.IsError {
		t.Fatalf("res = %+v", res)
	}
	msgs := messagesTo(t, d, "codex:t2")
	if len(msgs) != 1 || msgs[0].From.Key != "codex:t1" {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestToolArgumentCannotSetCaller(t *testing.T) {
	d, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:default:%t2","text":"hello","caller":"claude:p1"}`)
	res := b.Call(t.Context(), "codex", "send", codexMeta("t1"), args)
	if !res.IsError || !strings.Contains(res.Text, "caller") {
		t.Fatalf("res = %+v", res)
	}
	if msgs := messagesTo(t, d, "codex:t2"); len(msgs) != 0 {
		t.Fatalf("stored %+v", msgs)
	}
}

func TestMCPCallerToolWithoutIdentityFails(t *testing.T) {
	_, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:default:%t2","text":"hello"}`)
	res := b.Call(t.Context(), "codex", "send", map[string]json.RawMessage{}, args)
	if !res.IsError || !strings.Contains(res.Text, "not running inside an argus agent session") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPListWorksWithoutIdentity(t *testing.T) {
	_, b := toolNode(t)
	res := b.Call(t.Context(), "codex", "list", nil, nil)
	if res.IsError || !strings.Contains(res.Text, "home:default:%t1") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPKillRightsMatchCLI(t *testing.T) {
	_, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:default:%t1"}`)
	res := b.Call(t.Context(), "codex", "kill", codexMeta("t2"), args)
	if !res.IsError || !strings.Contains(res.Text, "only the parent") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPMeshDisabled(t *testing.T) {
	d := mcpNode()
	res := mcpBackend{d}.Call(t.Context(), "codex", "show", nil, json.RawMessage(`{"session_id":"x"}`))
	if !res.IsError || !strings.Contains(res.Text, "disabled") {
		t.Fatalf("res = %+v", res)
	}
}

var _ mcphttp.Backend = mcpBackend{}

func claudeSpawnNode(t *testing.T) (*Node, mcpBackend, *int) {
	t.Helper()
	d, _, _ := spawnNode(t, true)
	calls := new(int)
	d.spawnFn = func(context.Context, api.SpawnParams, string) (api.SpawnResult, error) {
		*calls++
		return api.SpawnResult{SessionID: "fake:ses_x"}, nil
	}
	return d, mcpBackend{d}, calls
}

func TestMCPSpawnRequiresAbsoluteCwd(t *testing.T) {
	for _, args := range []string{
		`{"agent":"fake"}`, `{"agent":"fake","cwd":""}`, `{"agent":"fake","cwd":"rel/dir"}`,
	} {
		d, b, calls := claudeSpawnNode(t)
		res := b.Call(t.Context(), "claude", "spawn", announceToolUse(t, d, "tu1", "p1"), json.RawMessage(args))
		if !res.IsError || !strings.Contains(res.Text, "cwd") {
			t.Errorf("%s: res = %+v", args, res)
		}
		if *calls != 0 {
			t.Errorf("%s: spawn called", args)
		}
	}
}

func TestMCPSendRequiresSessionID(t *testing.T) {
	_, b := toolNode(t)
	res := b.Call(t.Context(), "codex", "send", codexMeta("t1"), json.RawMessage(`{"text":"hi"}`))
	if !res.IsError || !strings.Contains(res.Text, "session_id") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPSpawnLinksParentAndReturnsTask(t *testing.T) {
	d, b, calls := claudeSpawnNode(t)
	args, _ := json.Marshal(map[string]string{"agent": "fake", "cwd": t.TempDir(), "prompt": "build it"})
	res := b.Call(t.Context(), "claude", "spawn", announceToolUse(t, d, "tu2", "p1"), args)
	if res.IsError || *calls != 1 {
		t.Fatalf("res = %+v calls = %d", res, *calls)
	}
	var out api.AgentSpawnResult
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil || !strings.HasPrefix(out.TaskID, "m-") {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	link, ok, err := d.mesh.Link(t.Context(), "fake:ses_x")
	if err != nil || !ok || link.Parent.Key != "claude:p1" || link.TaskID != out.TaskID {
		t.Fatalf("link = %+v ok=%v err=%v", link, ok, err)
	}
}
