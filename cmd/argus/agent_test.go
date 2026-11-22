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

type memoryNode struct {
	*fakeAgentNode
	log       api.PAgentMemoryLogResult
	proposals api.PAgentMemoryProposalsResult
	decide    api.PAgentMemoryDecideResult
	show      api.PAgentMemoryShowResult
	imported  api.PAgentMemoryImportResult
}

func (m *memoryNode) call(method string, params, out any) error {
	switch method {
	case api.MethodPAgentMemoryLog:
		*out.(*api.PAgentMemoryLogResult) = m.log
	case api.MethodPAgentMemoryUndo:
		*out.(*api.PAgentMemoryUndoResult) = api.PAgentMemoryUndoResult{Commit: "rev1"}
	case api.MethodPAgentMemoryProposals:
		*out.(*api.PAgentMemoryProposalsResult) = m.proposals
	case api.MethodPAgentMemoryDecide:
		*out.(*api.PAgentMemoryDecideResult) = m.decide
	case api.MethodPAgentMemoryShow:
		*out.(*api.PAgentMemoryShowResult) = m.show
	case api.MethodPAgentMemoryImport:
		*out.(*api.PAgentMemoryImportResult) = m.imported
	default:
		return m.fakeAgentNode.call(method, params, out)
	}
	m.calls = append(m.calls, method)
	if m.params == nil {
		m.params = map[string][]any{}
	}
	m.params[method] = append(m.params[method], params)
	return nil
}

func execMemory(t *testing.T, m *memoryNode, args ...string) (string, error) {
	t.Helper()
	dial := func(*cobra.Command) (caller, func(), error) { return m.call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(agentCmd(dial))
	root.SetArgs(append([]string{"agent", "memory"}, args...))
	var err error
	out, _ := captureOutput(t, func() { err = root.Execute() })
	return out, err
}

func newMemoryNode() *memoryNode { return &memoryNode{fakeAgentNode: twoAgents()} }

func TestAgentMemoryLogJSON(t *testing.T) {
	m := newMemoryNode()
	m.log = api.PAgentMemoryLogResult{Commits: []api.PAgentMemoryCommit{{Hash: "abcdef1234", Subject: "add x", Source: "capture", When: time.Now()}}}
	out, err := execMemory(t, m, "log", "scout", "--limit", "5", "--profile", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res api.PAgentMemoryLogResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Commits) != 1 || res.Commits[0].Hash != "abcdef1234" {
		t.Errorf("out %q err %v", out, err)
	}
	p := m.params[api.MethodPAgentMemoryLog][0].(api.PAgentMemoryLogParams)
	if p.AgentID != "a1" || p.Limit != 5 || !p.Profile {
		t.Errorf("params %+v", p)
	}
}

func TestAgentMemoryLogTable(t *testing.T) {
	m := newMemoryNode()
	m.log = api.PAgentMemoryLogResult{Commits: []api.PAgentMemoryCommit{{Hash: "abcdef1234", Subject: "add x", Source: "capture", When: time.Now()}}}
	out, err := execMemory(t, m, "log", "scout")
	if err != nil || !strings.Contains(out, "abcdef12 ") || !strings.Contains(out, "capture") || !strings.Contains(out, "add x") {
		t.Errorf("out %q err %v", out, err)
	}
	if p := m.params[api.MethodPAgentMemoryLog][0].(api.PAgentMemoryLogParams); p.Limit != 20 {
		t.Errorf("default limit %d", p.Limit)
	}
}

func TestAgentMemoryUndo(t *testing.T) {
	m := newMemoryNode()
	out, err := execMemory(t, m, "undo", "scout", "abc123", "--profile")
	if err != nil || !strings.Contains(out, "reverted abc123") {
		t.Errorf("out %q err %v", out, err)
	}
	p := m.params[api.MethodPAgentMemoryUndo][0].(api.PAgentMemoryUndoParams)
	if p.AgentID != "a1" || p.Commit != "abc123" || !p.Profile {
		t.Errorf("params %+v", p)
	}
}

func TestAgentMemoryReviewAccept(t *testing.T) {
	m := newMemoryNode()
	m.proposals = api.PAgentMemoryProposalsResult{Proposals: []api.PAgentMemoryProposal{
		{ID: "n:mp1", Ops: []api.PAgentMemoryOp{{Action: "add", Text: "likes tea"}}},
		{ID: "n:mp2", Kind: "instructions", Reason: "tighten tone"},
	}}
	out, err := execMemory(t, m, "review", "scout")
	if err != nil || !strings.Contains(out, "n:mp1") || !strings.Contains(out, "add likes tea") || !strings.Contains(out, "instructions: tighten tone") {
		t.Errorf("out %q err %v", out, err)
	}
	m.decide = api.PAgentMemoryDecideResult{Applied: 1}
	out, err = execMemory(t, m, "review", "scout", "--accept", "n:mp1")
	if err != nil || !strings.Contains(out, "accepted n:mp1") {
		t.Errorf("out %q err %v", out, err)
	}
	d := m.params[api.MethodPAgentMemoryDecide][0].(api.PAgentMemoryDecideParams)
	if d.ProposalID != "n:mp1" || !d.Accept {
		t.Errorf("decide %+v", d)
	}
	if _, err = execMemory(t, m, "review", "scout", "--reject", "n:mp2"); err != nil {
		t.Fatal(err)
	}
	if d := m.params[api.MethodPAgentMemoryDecide][1].(api.PAgentMemoryDecideParams); d.Accept || d.ProposalID != "n:mp2" {
		t.Errorf("decide %+v", d)
	}
}

func TestAgentMemoryImportStdin(t *testing.T) {
	m := newMemoryNode()
	m.imported = api.PAgentMemoryImportResult{Staged: 3}
	old := agentStdin
	agentStdin = strings.NewReader("remember this")
	defer func() { agentStdin = old }()
	out, err := execMemory(t, m, "import", "scout", "-")
	if err != nil || !strings.Contains(out, "staged for review: 3") {
		t.Errorf("out %q err %v", out, err)
	}
	if p := m.params[api.MethodPAgentMemoryImport][0].(api.PAgentMemoryImportParams); p.Content != "remember this" || p.AgentID != "a1" {
		t.Errorf("params %+v", p)
	}
}

func TestAgentMemoryShowUsage(t *testing.T) {
	m := newMemoryNode()
	m.show = api.PAgentMemoryShowResult{Memory: "- a\n", Usage: 14 * 1024, Budget: 24 * 1024, Profile: "prof"}
	out, err := execMemory(t, m, "show", "scout")
	if err != nil || !strings.Contains(out, "MEMORY.md 14 KB / 24 KB") || !strings.Contains(out, "- a") {
		t.Errorf("out %q err %v", out, err)
	}
	out, _ = execMemory(t, m, "show", "scout", "--profile")
	if strings.TrimSpace(out) != "prof" {
		t.Errorf("profile out %q", out)
	}
}
