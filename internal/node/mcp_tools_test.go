package node

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mcphttp"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
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

func TestMCPRejectsWhitespaceOnlyRequiredValues(t *testing.T) {
	d, b := toolNode(t)
	for _, args := range []string{
		`{"session_id":"home:default:%t2","text":"  \n\t"}`,
		`{"session_id":"   ","text":"hello"}`,
	} {
		res := b.Call(t.Context(), "codex", "send", codexMeta("t1"), json.RawMessage(args))
		if !res.IsError || !strings.Contains(res.Text, "missing required argument") {
			t.Errorf("%s: res = %+v", args, res)
		}
	}
	if msgs := messagesTo(t, d, "codex:t2"); len(msgs) != 0 {
		t.Fatalf("stored %+v", msgs)
	}
}

func TestMCPReplyRightsMatchCLI(t *testing.T) {
	d, b := toolNode(t)
	d.addAgentSession("codex", "t3")
	sent := b.Call(t.Context(), "codex", "send", codexMeta("t1"), json.RawMessage(`{"session_id":"home:default:%t2","text":"q"}`))
	var m api.AgentSendResult
	if sent.IsError || json.Unmarshal([]byte(sent.Text), &m) != nil {
		t.Fatalf("send = %+v", sent)
	}
	args, _ := json.Marshal(map[string]string{"message_id": m.MessageID, "text": "a"})

	res := b.Call(t.Context(), "codex", "reply", codexMeta("t3"), args)
	if !res.IsError || !strings.Contains(res.Text, "only the recipient") {
		t.Fatalf("non-recipient: res = %+v", res)
	}
	res = b.Call(t.Context(), "codex", "reply", codexMeta("t2"), args)
	if res.IsError {
		t.Fatalf("recipient: res = %+v", res)
	}
	if msgs := messagesTo(t, d, "codex:t1"); len(msgs) != 1 || msgs[0].Kind != api.MessageReply || msgs[0].ReplyTo != m.MessageID {
		t.Fatalf("replies = %+v", msgs)
	}
}

func TestMCPRespondRightsMatchCLI(t *testing.T) {
	d, parent, resp := noticeNode(t)
	cc := claudecode.New()
	parent.Adapter = cc
	d.adapters["claude"] = struct {
		meshPrompter
		adapter.MCPIdentity
	}{meshPrompter{parent}, cc.(adapter.MCPIdentity)}
	parentSession(d, session.StatusWorking)
	d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "p9", Status: session.StatusIdle, Input: session.InputAPI})
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	b := mcpBackend{d}
	args, _ := json.Marshal(map[string]string{"notice_id": n.ID, "behavior": "allow"})

	res := b.Call(t.Context(), "claude", "respond", announceToolUse(t, d, "tu1", "p9"), args)
	if !res.IsError || !strings.Contains(res.Text, "only the parent") || len(resp.got) != 0 {
		t.Fatalf("non-parent: res = %+v, responder got %+v", res, resp.got)
	}
	res = b.Call(t.Context(), "claude", "respond", announceToolUse(t, d, "tu2", "p1"), args)
	if res.IsError || res.Text != "{}" || len(resp.got) != 1 || resp.got[0].Behavior != "allow" {
		t.Fatalf("parent: res = %+v, responder got %+v", res, resp.got)
	}
}

func TestMCPShowReturnsQueued(t *testing.T) {
	_, b := toolNode(t)
	if res := b.Call(t.Context(), "codex", "send", codexMeta("t1"), json.RawMessage(`{"session_id":"home:default:%t2","text":"q"}`)); res.IsError {
		t.Fatalf("send = %+v", res)
	}
	res := b.Call(t.Context(), "codex", "show", nil, json.RawMessage(`{"session_id":"home:default:%t2"}`))
	var out api.AgentShowResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil {
		t.Fatalf("res = %+v", res)
	}
	if out.Session.ID != "home:default:%t2" || len(out.Queued) != 1 || out.Queued[0].Body != "q" {
		t.Fatalf("out = %+v", out)
	}
}

type transcriptAdapter struct {
	adapter.Adapter
	view transcript.TranscriptView
}

func (a transcriptAdapter) ReadTranscriptView(string) (transcript.TranscriptView, error) {
	return a.view, nil
}

func transcriptToolNode(t *testing.T) mcpBackend {
	t.Helper()
	d, b := toolNode(t)
	d.adapters["codex"] = transcriptAdapter{Adapter: d.adapters["codex"], view: transcript.TranscriptView{Entries: []transcript.Entry{
		{ID: "0", Kind: transcript.EntryUser, Text: "build it"},
		{ID: "1", Kind: transcript.EntryText, Text: "working"},
		{ID: "2", Kind: transcript.EntryText, Text: "done"},
	}}}
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "codex", Server: session.TmuxServerDefault, PaneID: "%t9",
		AgentSessionID: "t9", Status: session.StatusIdle, TranscriptPath: "/t9.jsonl",
	})
	return b
}

func TestMCPOutputReturnsLatestAnswer(t *testing.T) {
	b := transcriptToolNode(t)
	res := b.Call(t.Context(), "codex", "output", nil, json.RawMessage(`{"session_id":"home:default:%t9"}`))
	var out api.AgentOutputResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil || out.Text != "working\n\ndone" {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPTranscriptHonorsLast(t *testing.T) {
	b := transcriptToolNode(t)
	res := b.Call(t.Context(), "codex", "transcript", nil, json.RawMessage(`{"session_id":"home:default:%t9","last":2}`))
	var out api.AgentTranscriptResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil {
		t.Fatalf("res = %+v", res)
	}
	if len(out.Entries) != 2 || out.Entries[0].ID != "1" || out.Entries[1].ID != "2" {
		t.Fatalf("entries = %+v", out.Entries)
	}
}
