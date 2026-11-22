package node

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mcphttp"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func toolNode(t *testing.T) (*Node, mcpBackend) {
	t.Helper()
	d, _ := orchNode(t)
	d.SetIdentity("home", "")
	d.addAgentSession("codex", "t1")
	d.addAgentSession("codex", "t2")
	return d, mcpBackend{d}
}

func codexMeta(id string) map[string]json.RawMessage {
	return rawMeta(map[string]string{"sessionId": id})
}

func TestMCPToolsList(t *testing.T) {
	_, b := toolNode(t)
	var names []string
	for _, tool := range b.Tools() {
		names = append(names, tool.Name)
		if tool.Description == "" || !strings.Contains(string(tool.InputSchema), `"additionalProperties":false`) {
			t.Errorf("%s: incomplete definition", tool.Name)
		}
	}
	want := "get_current_session list_sessions get_session spawn_session send_message reply_to_message respond_to_interaction get_session_output get_session_transcript kill_session rename_session remember propose_instructions request_handoff report_run schedule_check_back routine_list routine_create routine_update routine_pause routine_resume routine_delete offer"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("tools = %s", got)
	}
}

func toolSchema(t *testing.T, b mcpBackend, name string) map[string]any {
	t.Helper()
	for _, tool := range b.Tools() {
		if tool.Name == name {
			var s map[string]any
			if err := json.Unmarshal(tool.InputSchema, &s); err != nil {
				t.Fatal(err)
			}
			return s
		}
	}
	t.Fatalf("no tool %s", name)
	return nil
}

func propSchema(t *testing.T, b mcpBackend, tool, prop string) string {
	t.Helper()
	p, _ := json.Marshal(toolSchema(t, b, tool)["properties"].(map[string]any)[prop])
	return string(p)
}

func TestMCPSpawnSchemaListsOrchestratedAgents(t *testing.T) {
	_, b := toolNode(t)
	if got := propSchema(t, b, "spawn_session", "agent"); got != `{"enum":["claude","codex","opencode","pi"],"type":"string"}` {
		t.Fatalf("agent = %s", got)
	}
}

func TestMCPRespondSchemaConstrainsAnswers(t *testing.T) {
	_, b := toolNode(t)
	if got := propSchema(t, b, "respond_to_interaction", "behavior"); got != `{"enum":["deny"],"type":"string"}` {
		t.Fatalf("behavior = %s", got)
	}
	want := `{"additionalProperties":{"anyOf":[{"type":"string"},{"items":{"type":"string"},"type":"array"}]},"type":"object"}`
	if got := propSchema(t, b, "respond_to_interaction", "answers"); got != want {
		t.Fatalf("answers = %s", got)
	}
}

func TestMCPWhoamiComposite(t *testing.T) {
	_, b := toolNode(t)
	res := b.Call(t.Context(), "codex", "get_current_session", codexMeta("t1"), json.RawMessage(`{}`))
	if res.IsError || !strings.Contains(res.Text, `"id":"home:codex:t1"`) {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPSendQueuesFromMetaCaller(t *testing.T) {
	d, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:codex:t2","text":"hello"}`)
	res := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), args)
	if res.IsError {
		t.Fatalf("res = %+v", res)
	}
	msgs := messagesTo(t, d, "codex:t2")
	if len(msgs) != 1 || msgs[0].From.Key != "codex:t1" {
		t.Fatalf("msgs = %+v", msgs)
	}
	var out mcpSendResult
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil || out.MessageID != msgs[0].ID || out.Next != sendNext {
		t.Fatalf("out = %+v err = %v", out, err)
	}
}

func TestMCPSendRequestIDQueuesOnce(t *testing.T) {
	d, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:codex:t2","text":"hello","request_id":"r1"}`)
	first := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), args)
	again := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), args)
	if first.IsError || again.Text != first.Text {
		t.Fatalf("first = %+v again = %+v", first, again)
	}
	if msgs := messagesTo(t, d, "codex:t2"); len(msgs) != 1 {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestToolArgumentCannotSetCaller(t *testing.T) {
	d, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:codex:t2","text":"hello","caller":"claude:p1"}`)
	res := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), args)
	if !res.IsError || !strings.Contains(res.Text, "caller") {
		t.Fatalf("res = %+v", res)
	}
	if msgs := messagesTo(t, d, "codex:t2"); len(msgs) != 0 {
		t.Fatalf("stored %+v", msgs)
	}
}

func TestMCPCallerToolWithoutIdentityFails(t *testing.T) {
	_, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:codex:t2","text":"hello"}`)
	res := b.Call(t.Context(), "codex", "send_message", map[string]json.RawMessage{}, args)
	if !res.IsError || !strings.Contains(res.Text, "not running inside an argus agent session") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPListWorksWithoutIdentity(t *testing.T) {
	_, b := toolNode(t)
	res := b.Call(t.Context(), "codex", "list_sessions", nil, nil)
	if res.IsError || !strings.Contains(res.Text, "home:codex:t1") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPKillRightsMatchCLI(t *testing.T) {
	_, b := toolNode(t)
	args := json.RawMessage(`{"session_id":"home:codex:t1"}`)
	res := b.Call(t.Context(), "codex", "kill_session", codexMeta("t2"), args)
	if !res.IsError || !strings.Contains(res.Text, "only the parent") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPOrchDisabled(t *testing.T) {
	d := mcpNode()
	res := mcpBackend{d}.Call(t.Context(), "codex", "get_session", nil, json.RawMessage(`{"session_id":"x"}`))
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
		res := b.Call(t.Context(), "claude", "spawn_session", announceToolUse(t, d, "tu1", "p1"), json.RawMessage(args))
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
	res := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), json.RawMessage(`{"text":"hi"}`))
	if !res.IsError || !strings.Contains(res.Text, "session_id") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPSpawnLinksParentAndReturnsTask(t *testing.T) {
	d, b, calls := claudeSpawnNode(t)
	args, _ := json.Marshal(map[string]string{"agent": "fake", "cwd": t.TempDir(), "prompt": "build it"})
	res := b.Call(t.Context(), "claude", "spawn_session", announceToolUse(t, d, "tu2", "p1"), args)
	if res.IsError || *calls != 1 {
		t.Fatalf("res = %+v calls = %d", res, *calls)
	}
	var out mcpSpawnResult
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil || !strings.HasPrefix(out.TaskID, d.id+":m-") || out.Next != spawnNext {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	link, ok, err := d.orch.Link(t.Context(), api.Actor{Node: d.id, Key: "fake:ses_x"})
	if err != nil || !ok || link.Parent.Key != "claude:p1" || link.TaskID != out.TaskID {
		t.Fatalf("link = %+v ok=%v err=%v", link, ok, err)
	}
}

func TestMCPSpawnWithoutPromptHasNoNext(t *testing.T) {
	d, b, _ := claudeSpawnNode(t)
	args, _ := json.Marshal(map[string]string{"agent": "fake", "cwd": t.TempDir()})
	res := b.Call(t.Context(), "claude", "spawn_session", announceToolUse(t, d, "tu5", "p1"), args)
	if res.IsError || strings.Contains(res.Text, `"next"`) {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPSpawnRequestIDSpawnsOnce(t *testing.T) {
	d, b, calls := claudeSpawnNode(t)
	args, _ := json.Marshal(map[string]string{"agent": "fake", "cwd": t.TempDir(), "prompt": "build it", "request_id": "r1"})
	first := b.Call(t.Context(), "claude", "spawn_session", announceToolUse(t, d, "tu3", "p1"), args)
	again := b.Call(t.Context(), "claude", "spawn_session", announceToolUse(t, d, "tu4", "p1"), args)
	if first.IsError || again.Text != first.Text || *calls != 1 {
		t.Fatalf("first = %+v again = %+v calls = %d", first, again, *calls)
	}
}

func TestMCPRenameRenamesAnySession(t *testing.T) {
	d, b := toolNode(t)
	fr := &fakeRenamer{Adapter: d.adapters["codex"]}
	d.adapters["codex"] = fr
	// t2 is neither the caller's child nor the caller.
	res := b.Call(t.Context(), "codex", "rename_session", nil, json.RawMessage(`{"session_id":"home:codex:t2","name":"Review"}`))
	var out api.SessionRenameResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil || out.Name != "Review" || !out.Applied {
		t.Fatalf("res = %+v", res)
	}
	if fr.names["t2"] != "Review" {
		t.Errorf("agent saw %v", fr.names)
	}
}

func TestMCPRejectsWhitespaceOnlyRequiredValues(t *testing.T) {
	d, b := toolNode(t)
	for _, args := range []string{
		`{"session_id":"home:codex:t2","text":"  \n\t"}`,
		`{"session_id":"   ","text":"hello"}`,
	} {
		res := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), json.RawMessage(args))
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
	sent := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), json.RawMessage(`{"session_id":"home:codex:t2","text":"q"}`))
	var m api.OrchSendResult
	if sent.IsError || json.Unmarshal([]byte(sent.Text), &m) != nil {
		t.Fatalf("send = %+v", sent)
	}
	args, _ := json.Marshal(map[string]string{"message_id": m.MessageID, "text": "a"})

	res := b.Call(t.Context(), "codex", "reply_to_message", codexMeta("t3"), args)
	if !res.IsError || !strings.Contains(res.Text, "only the recipient") {
		t.Fatalf("non-recipient: res = %+v", res)
	}
	res = b.Call(t.Context(), "codex", "reply_to_message", codexMeta("t2"), args)
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
		orchPrompter
		adapter.MCPIdentity
	}{orchPrompter{parent}, cc.(adapter.MCPIdentity)}
	parentSession(d, session.StatusWorking)
	d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "p9", Status: session.StatusIdle, Input: session.InputAPI})
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	b := mcpBackend{d}
	args, _ := json.Marshal(map[string]any{"message_id": n.ID, "answers": proceed})

	res := b.Call(t.Context(), "claude", "respond_to_interaction", announceToolUse(t, d, "tu1", "p9"), args)
	if !res.IsError || !strings.Contains(res.Text, "only the parent") || len(resp.got) != 0 {
		t.Fatalf("non-parent: res = %+v, responder got %+v", res, resp.got)
	}
	res = b.Call(t.Context(), "claude", "respond_to_interaction", announceToolUse(t, d, "tu2", "p1"), args)
	if res.IsError || res.Text != "{}" || len(resp.got) != 1 || resp.got[0].Answers["Proceed?"] != "Yes" {
		t.Fatalf("parent: res = %+v, responder got %+v", res, resp.got)
	}
}

func TestMCPShowReturnsQueued(t *testing.T) {
	_, b := toolNode(t)
	if res := b.Call(t.Context(), "codex", "send_message", codexMeta("t1"), json.RawMessage(`{"session_id":"home:codex:t2","text":"q"}`)); res.IsError {
		t.Fatalf("send = %+v", res)
	}
	res := b.Call(t.Context(), "codex", "get_session", nil, json.RawMessage(`{"session_id":"home:codex:t2"}`))
	var out api.OrchGetResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil {
		t.Fatalf("res = %+v", res)
	}
	if out.Session.ID != "home:codex:t2" || len(out.Queued) != 1 || out.Queued[0].Body != "q" {
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
	res := b.Call(t.Context(), "codex", "get_session_output", nil, json.RawMessage(`{"session_id":"home:codex:t9"}`))
	var out api.OrchOutputResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil || out.Text != "working\n\ndone" {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPTranscriptHonorsLast(t *testing.T) {
	b := transcriptToolNode(t)
	res := b.Call(t.Context(), "codex", "get_session_transcript", nil, json.RawMessage(`{"session_id":"home:codex:t9","last":2}`))
	var out api.OrchTranscriptResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil {
		t.Fatalf("res = %+v", res)
	}
	if len(out.Entries) != 2 || out.Entries[0].ID != "1" || out.Entries[1].ID != "2" {
		t.Fatalf("entries = %+v", out.Entries)
	}
}

func TestMCPTranscriptOmitsArgusMessages(t *testing.T) {
	d, b := toolNode(t)
	entries := []transcript.Entry{{ID: "0", Kind: transcript.EntryUser, Text: "<argus-message raw",
		ArgusMessages: []transcript.ArgusMessage{{ID: "m-1", Kind: "task", Body: "build it"}}}}
	d.adapters["codex"] = transcriptAdapter{Adapter: d.adapters["codex"], view: transcript.TranscriptView{Entries: entries}}
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "codex", Server: session.TmuxServerDefault, PaneID: "%t9",
		AgentSessionID: "t9", Status: session.StatusIdle, TranscriptPath: "/t9.jsonl",
	})
	res := b.Call(t.Context(), "codex", "get_session_transcript", nil, json.RawMessage(`{"session_id":"home:codex:t9"}`))
	var out api.OrchTranscriptResult
	if res.IsError || json.Unmarshal([]byte(res.Text), &out) != nil {
		t.Fatalf("res = %+v", res)
	}
	if strings.Contains(res.Text, "argusMessages") || len(out.Entries) != 1 || out.Entries[0].Text != "<argus-message raw" {
		t.Fatalf("res = %s", res.Text)
	}
	if entries[0].ArgusMessages == nil {
		t.Fatal("the adapter's entries were changed")
	}
}

func TestMCPSpawnSchemaHasNode(t *testing.T) {
	_, b := toolNode(t)
	if got := propSchema(t, b, "spawn_session", "node"); got != `{"type":"string"}` {
		t.Fatalf("node = %s", got)
	}
	for _, r := range toolSchema(t, b, "spawn_session")["required"].([]any) {
		if r == "node" {
			t.Fatal("node is required")
		}
	}
}

func TestMCPIDNoteMentionsNodes(t *testing.T) {
	_, b := toolNode(t)
	for _, tool := range b.Tools() {
		if strings.Contains(tool.Description, "IDs are composite") && !strings.Contains(tool.Description, "any connected node") {
			t.Errorf("%s: description lacks \"any connected node\"", tool.Name)
		}
	}
}

// oldToolName matches a tool name from before the rename, as text that names a
// tool: in backticks, or as "the <name> tool", or after "with" or "Use".
var oldToolName = regexp.MustCompile("`(whoami|list|show|spawn|send|reply|respond|output|transcript|kill|rename)`|\\b(whoami|list|show|spawn|send|reply|respond|output|transcript|kill|rename) tool\\b|\\b(with|Use) (whoami|list|show|spawn|send|reply|respond|output|transcript|kill|rename)\\b")

func TestMCPTextNamesOnlyCurrentTools(t *testing.T) {
	_, b := toolNode(t)
	texts := map[string]string{}
	for _, tool := range b.Tools() {
		texts[tool.Name] = tool.Description
	}
	from := api.Actor{Node: "home", Key: "claude:p"}
	for _, kind := range []api.MessageKind{api.MessageTask, api.MessageText, api.MessageInteraction} {
		texts["render "+string(kind)] = orch.RenderPrompt([]orch.Delivery{{Msg: api.Message{ID: "home:m-00000001", Kind: kind, From: from, Body: "x"}, From: "p"}})
	}
	for name, text := range texts {
		if m := oldToolName.FindString(text); m != "" {
			t.Errorf("%s names an old tool: %q", name, m)
		}
	}
}

func TestMCPRespondRejectsNoticeID(t *testing.T) {
	_, b := toolNode(t)
	res := b.Call(t.Context(), "codex", "respond_to_interaction", codexMeta("t1"), json.RawMessage(`{"notice_id":"x","behavior":"deny"}`))
	if !res.IsError || res.Text != "unknown argument: notice_id" {
		t.Fatalf("res = %+v", res)
	}
}
