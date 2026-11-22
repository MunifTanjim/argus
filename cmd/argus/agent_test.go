package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type fakeAgentNode struct {
	agents    []api.PAgentInfo
	primary   string
	add       api.PAgentAddResult
	threads   []api.PAgentThreadsResult // served in order, last repeats
	timeline  api.PAgentTimeline
	calls     []string
	params    map[string][]any
	timelines []api.PAgentTimeline // served in order, last repeats; overrides timeline
}

func (f *fakeAgentNode) call(method string, params, out any) error {
	f.calls = append(f.calls, method)
	if f.params == nil {
		f.params = map[string][]any{}
	}
	f.params[method] = append(f.params[method], params)
	switch method {
	case api.MethodPAgentList:
		*out.(*api.PAgentListResult) = api.PAgentListResult{Agents: f.agents, Primary: f.primary}
	case api.MethodPAgentCreate:
		*out.(*api.PAgentInfo) = api.PAgentInfo{ID: "a1", Name: "scout", Dir: "/agents/scout", Status: "idle"}
	case api.MethodPAgentAdd:
		*out.(*api.PAgentAddResult) = f.add
	case api.MethodPAgentThreadSend:
		*out.(*api.PAgentSendResult) = api.PAgentSendResult{ThreadID: "th1", SessionID: "home:claude:s1"}
	case api.MethodPAgentThreadList:
		r := f.threads[0]
		if len(f.threads) > 1 {
			f.threads = f.threads[1:]
		}
		*out.(*api.PAgentThreadsResult) = r
	case api.MethodPAgentThreadTimeline:
		tl := f.timeline
		if len(f.timelines) > 0 {
			tl = f.timelines[0]
			if len(f.timelines) > 1 {
				f.timelines = f.timelines[1:]
			}
		}
		*out.(*api.PAgentTimeline) = tl
	}
	return nil
}

func execAgent(t *testing.T, f *fakeAgentNode, args ...string) (out, errOut string, err error) {
	t.Helper()
	dial := func(*cobra.Command) (caller, func(), error) { return f.call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(agentCmd(dial))
	root.SetArgs(append([]string{"agent"}, args...))
	out, errOut = captureOutput(t, func() { err = root.Execute() })
	return
}

func twoAgents() *fakeAgentNode {
	return &fakeAgentNode{
		agents:  []api.PAgentInfo{{ID: "a1", Name: "scout", Status: "idle", Primary: true}, {ID: "a2", Name: "other", Status: "idle"}},
		primary: "a1",
	}
}

func TestAgentCreateCallsRPC(t *testing.T) {
	f := &fakeAgentNode{}
	out, _, err := execAgent(t, f, "create", "scout", "--dir", "/d", "--agent", "claude", "--model", "m", "--from", "base", "--node", "b")
	if err != nil {
		t.Fatal(err)
	}
	got := f.params[api.MethodPAgentCreate][0].(api.PAgentCreateParams)
	want := api.PAgentCreateParams{Name: "scout", Dir: "/d", Agent: "claude", Model: "m", From: "base", NodeID: "b"}
	if got != want {
		t.Fatalf("params = %+v, want %+v", got, want)
	}
	if !strings.Contains(out, "created agent scout") {
		t.Fatalf("out = %q", out)
	}
}

func TestAgentChatPrimaryFallback(t *testing.T) {
	f := twoAgents()
	out, _, err := execAgent(t, f, "chat", "-m", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodPAgentThreadSend][0].(api.PAgentSendParams); got.AgentID != "a1" || got.Text != "hi" {
		t.Fatalf("send = %+v", got)
	}
	if strings.TrimSpace(out) != "th1" {
		t.Fatalf("out = %q", out)
	}

	f = twoAgents()
	f.primary = ""
	_, errOut, err := execAgent(t, f, "chat", "-m", "hi")
	if err == nil || !strings.Contains(errOut, "several agents; name one") {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
	if len(f.params[api.MethodPAgentThreadSend]) != 0 {
		t.Fatal("sent without a target")
	}

	f = &fakeAgentNode{agents: []api.PAgentInfo{{ID: "a2", Name: "only"}}}
	if _, _, err := execAgent(t, f, "chat", "-m", "hi"); err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodPAgentThreadSend][0].(api.PAgentSendParams); got.AgentID != "a2" {
		t.Fatalf("send = %+v", got)
	}

	f = twoAgents()
	out, _, err = execAgent(t, f, "chat")
	if err != nil || !strings.Contains(out, "open the TUI and choose the agent: argus") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func fastAgentPoll(t *testing.T) {
	old := agentWaitPoll
	agentWaitPoll = time.Millisecond
	t.Cleanup(func() { agentWaitPoll = old })
}

func chatThread(status string) api.PAgentThreadsResult {
	return api.PAgentThreadsResult{Threads: []api.PAgentThreadInfo{{ID: "th1", Kind: "chat", Status: "active", SessionStatus: status}}}
}

func userText(s string) transcript.Entry {
	return transcript.Entry{Kind: transcript.EntryUser, Text: s}
}
func replyText(s string) transcript.Entry {
	return transcript.Entry{Kind: transcript.EntryText, Text: s}
}

func TestAgentChatWaitPrintsReply(t *testing.T) {
	fastAgentPoll(t)
	f := twoAgents()
	f.threads = []api.PAgentThreadsResult{chatThread("idle"), chatThread("working"), chatThread("idle")}
	f.timelines = []api.PAgentTimeline{
		{Entries: []transcript.Entry{userText("older"), replyText("old reply")}}, // baseline
		{Entries: []transcript.Entry{userText("older"), replyText("old reply"), userText("context\n\nhi there"), replyText("the reply")}},
	}
	out, _, err := execAgent(t, f, "chat", "scout", "hi there", "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "the reply" {
		t.Fatalf("out = %q", out)
	}
	if len(f.params[api.MethodPAgentThreadList]) < 3 {
		t.Fatal("did not poll while working")
	}
}

func TestAgentChatWaitIgnoresEarlierIdenticalMessage(t *testing.T) {
	fastAgentPoll(t)
	f := twoAgents()
	f.threads = []api.PAgentThreadsResult{chatThread("idle")}
	f.timelines = []api.PAgentTimeline{
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply")}}, // baseline
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply")}}, // new prompt not recorded yet
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply"), userText("continue")}},
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply"), userText("continue"), replyText("new reply")}},
	}
	out, _, err := execAgent(t, f, "chat", "scout", "continue", "--wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res agentChatResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res != (agentChatResult{ThreadID: "th1", SessionID: "home:claude:s1", Reply: "new reply"}) {
		t.Fatalf("res = %+v", res)
	}
}

func TestAgentChatWaitWaitingOnInteraction(t *testing.T) {
	fastAgentPoll(t)
	f := twoAgents()
	f.threads = []api.PAgentThreadsResult{chatThread("idle"), chatThread("awaiting_input")}
	f.timelines = []api.PAgentTimeline{
		{},
		{Entries: []transcript.Entry{userText("go"), replyText("partial")}},
	}
	out, _, err := execAgent(t, f, "chat", "scout", "go", "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "partial") || !strings.Contains(out, "agent is waiting for you") {
		t.Fatalf("out = %q", out)
	}

	f.threads = []api.PAgentThreadsResult{chatThread("idle"), chatThread("awaiting_input")}
	f.timelines = []api.PAgentTimeline{{}, {Entries: []transcript.Entry{userText("go")}}}
	out, _, err = execAgent(t, f, "chat", "scout", "go", "--wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res agentChatResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || !res.Waiting {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestAgentChatHintJSON(t *testing.T) {
	out, _, err := execAgent(t, twoAgents(), "chat", "--json")
	if err != nil || !strings.Contains(out, `"hint"`) {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestAgentAddFoundInstructionsNonTTY(t *testing.T) {
	old := agentStdinIsTTY
	agentStdinIsTTY = func() bool { return false }
	t.Cleanup(func() { agentStdinIsTTY = old })
	f := &fakeAgentNode{add: api.PAgentAddResult{FoundInstructions: true}}
	_, errOut, err := execAgent(t, f, "add", "/some/dir")
	if err == nil {
		t.Fatal("want failure")
	}
	if !strings.Contains(errOut, "use --import-instructions") {
		t.Fatalf("stderr = %q", errOut)
	}
	if len(f.params[api.MethodPAgentAdd]) != 1 {
		t.Fatalf("add calls = %d", len(f.params[api.MethodPAgentAdd]))
	}

	f = &fakeAgentNode{add: api.PAgentAddResult{Agent: api.PAgentInfo{Name: "scout"}}}
	if _, _, err := execAgent(t, f, "add", "/some/dir", "--import-instructions"); err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodPAgentAdd][0].(api.PAgentAddParams); !got.ImportInstructions || got.Dir != "/some/dir" {
		t.Fatalf("params = %+v", got)
	}
}

func TestAgentListJSON(t *testing.T) {
	f := twoAgents()
	out, _, err := execAgent(t, f, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res api.PAgentListResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 2 || res.Primary != "a1" {
		t.Fatalf("res = %+v", res)
	}
}

func TestAgentNameResolution(t *testing.T) {
	f := &fakeAgentNode{agents: []api.PAgentInfo{
		{ID: "a1", Name: "scout", NodeID: "n1", NodeLabel: "laptop"},
		{ID: "a2", Name: "scout", NodeID: "n2", NodeLabel: "server"},
	}}
	_, errOut, err := execAgent(t, f, "show", "scout")
	if err == nil || !strings.Contains(errOut, "use scout@<node-label>") {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
	if _, _, err := execAgent(t, f, "show", "scout@server"); err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodPAgentShow][0].(api.PAgentRef); got.AgentID != "a2" {
		t.Fatalf("show = %+v", got)
	}
	_, errOut, _ = execAgent(t, f, "show", "nobody")
	if !strings.Contains(errOut, `unknown agent "nobody"`) {
		t.Fatalf("stderr = %q", errOut)
	}
}
