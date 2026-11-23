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

type fakeBotNode struct {
	bots        []api.BotInfo
	primary     string
	unreachable []string
	add         api.BotAddResult
	threads     []api.BotThreadsResult // served in order, last repeats
	timeline    api.BotTimeline
	calls       []string
	params      map[string][]any
	timelines   []api.BotTimeline // served in order, last repeats; overrides timeline
}

func (f *fakeBotNode) call(method string, params, out any) error {
	f.calls = append(f.calls, method)
	if f.params == nil {
		f.params = map[string][]any{}
	}
	f.params[method] = append(f.params[method], params)
	switch method {
	case api.MethodBotList:
		*out.(*api.BotListResult) = api.BotListResult{Bots: f.bots, Primary: f.primary, Unreachable: f.unreachable}
	case api.MethodBotCreate:
		*out.(*api.BotInfo) = api.BotInfo{ID: "a1", Name: "scout", Dir: "/bots/scout", Status: "idle"}
	case api.MethodBotAdd:
		*out.(*api.BotAddResult) = f.add
	case api.MethodBotThreadSend:
		*out.(*api.BotSendResult) = api.BotSendResult{ThreadID: "th1", SessionID: "home:claude:s1"}
	case api.MethodBotThreadList:
		r := f.threads[0]
		if len(f.threads) > 1 {
			f.threads = f.threads[1:]
		}
		*out.(*api.BotThreadsResult) = r
	case api.MethodBotThreadTimeline:
		tl := f.timeline
		if len(f.timelines) > 0 {
			tl = f.timelines[0]
			if len(f.timelines) > 1 {
				f.timelines = f.timelines[1:]
			}
		}
		*out.(*api.BotTimeline) = tl
	}
	return nil
}

func execBot(t *testing.T, f *fakeBotNode, args ...string) (out, errOut string, err error) {
	t.Helper()
	dial := func(*cobra.Command) (caller, func(), error) { return f.call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(botCmd(dial))
	root.SetArgs(append([]string{"bot"}, args...))
	out, errOut = captureOutput(t, func() { err = root.Execute() })
	return
}

func twoBots() *fakeBotNode {
	return &fakeBotNode{
		bots:    []api.BotInfo{{ID: "a1", Name: "scout", Status: "idle", Primary: true}, {ID: "a2", Name: "other", Status: "idle"}},
		primary: "a1",
	}
}

func TestBotCreateCallsRPC(t *testing.T) {
	f := &fakeBotNode{}
	out, _, err := execBot(t, f, "create", "scout", "--dir", "/d", "--agent", "claude", "--model", "m", "--from", "base", "--node", "b")
	if err != nil {
		t.Fatal(err)
	}
	got := f.params[api.MethodBotCreate][0].(api.BotCreateParams)
	want := api.BotCreateParams{Name: "scout", Dir: "/d", Agent: "claude", Model: "m", From: "base", NodeID: "b"}
	if got != want {
		t.Fatalf("params = %+v, want %+v", got, want)
	}
	if !strings.Contains(out, "created bot scout") {
		t.Fatalf("out = %q", out)
	}
}

func TestBotChatPrimaryFallback(t *testing.T) {
	f := twoBots()
	out, _, err := execBot(t, f, "chat", "-m", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodBotThreadSend][0].(api.BotSendParams); got.BotID != "a1" || got.Text != "hi" {
		t.Fatalf("send = %+v", got)
	}
	if strings.TrimSpace(out) != "th1" {
		t.Fatalf("out = %q", out)
	}

	f = twoBots()
	f.primary = ""
	_, errOut, err := execBot(t, f, "chat", "-m", "hi")
	if err == nil || !strings.Contains(errOut, "several bots; name one") {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
	if len(f.params[api.MethodBotThreadSend]) != 0 {
		t.Fatal("sent without a target")
	}

	f = &fakeBotNode{bots: []api.BotInfo{{ID: "a2", Name: "only"}}}
	if _, _, err := execBot(t, f, "chat", "-m", "hi"); err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodBotThreadSend][0].(api.BotSendParams); got.BotID != "a2" {
		t.Fatalf("send = %+v", got)
	}

	f = twoBots()
	out, _, err = execBot(t, f, "chat")
	if err != nil || !strings.Contains(out, "open the TUI and choose the bot: argus") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func fastBotPoll(t *testing.T) {
	old := botWaitPoll
	botWaitPoll = time.Millisecond
	t.Cleanup(func() { botWaitPoll = old })
}

func chatThread(status string) api.BotThreadsResult {
	return api.BotThreadsResult{Threads: []api.BotThreadInfo{{ID: "th1", Kind: "chat", Status: "active", SessionStatus: status}}}
}

func userText(s string) transcript.Entry {
	return transcript.Entry{Kind: transcript.EntryUser, Text: s}
}
func replyText(s string) transcript.Entry {
	return transcript.Entry{Kind: transcript.EntryText, Text: s}
}

func TestBotChatWaitPrintsReply(t *testing.T) {
	fastBotPoll(t)
	f := twoBots()
	f.threads = []api.BotThreadsResult{chatThread("idle"), chatThread("working"), chatThread("idle")}
	f.timelines = []api.BotTimeline{
		{Entries: []transcript.Entry{userText("older"), replyText("old reply")}}, // baseline
		{Entries: []transcript.Entry{userText("older"), replyText("old reply"), userText("context\n\nhi there"), replyText("the reply")}},
	}
	out, _, err := execBot(t, f, "chat", "scout", "hi there", "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "the reply" {
		t.Fatalf("out = %q", out)
	}
	if len(f.params[api.MethodBotThreadList]) < 3 {
		t.Fatal("did not poll while working")
	}
}

func TestBotChatWaitIgnoresEarlierIdenticalMessage(t *testing.T) {
	fastBotPoll(t)
	f := twoBots()
	f.threads = []api.BotThreadsResult{chatThread("idle")}
	f.timelines = []api.BotTimeline{
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply")}}, // baseline
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply")}}, // new prompt not recorded yet
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply"), userText("continue")}},
		{Entries: []transcript.Entry{userText("continue"), replyText("old reply"), userText("continue"), replyText("new reply")}},
	}
	out, _, err := execBot(t, f, "chat", "scout", "continue", "--wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res botChatResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res != (botChatResult{ThreadID: "th1", SessionID: "home:claude:s1", Reply: "new reply"}) {
		t.Fatalf("res = %+v", res)
	}
}

func TestBotChatWaitWaitingOnInteraction(t *testing.T) {
	fastBotPoll(t)
	f := twoBots()
	f.threads = []api.BotThreadsResult{chatThread("idle"), chatThread("awaiting_input")}
	f.timelines = []api.BotTimeline{
		{},
		{Entries: []transcript.Entry{userText("go"), replyText("partial")}},
	}
	out, _, err := execBot(t, f, "chat", "scout", "go", "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "partial") || !strings.Contains(out, "bot is waiting for you") {
		t.Fatalf("out = %q", out)
	}

	f.threads = []api.BotThreadsResult{chatThread("idle"), chatThread("awaiting_input")}
	f.timelines = []api.BotTimeline{{}, {Entries: []transcript.Entry{userText("go")}}}
	out, _, err = execBot(t, f, "chat", "scout", "go", "--wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res botChatResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || !res.Waiting {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestBotChatHintJSON(t *testing.T) {
	out, _, err := execBot(t, twoBots(), "chat", "--json")
	if err != nil || !strings.Contains(out, `"hint"`) {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestBotAddFoundInstructionsNonTTY(t *testing.T) {
	old := botStdinIsTTY
	botStdinIsTTY = func() bool { return false }
	t.Cleanup(func() { botStdinIsTTY = old })
	f := &fakeBotNode{add: api.BotAddResult{FoundInstructions: true}}
	_, errOut, err := execBot(t, f, "add", "/some/dir")
	if err == nil {
		t.Fatal("want failure")
	}
	if !strings.Contains(errOut, "use --import-instructions") {
		t.Fatalf("stderr = %q", errOut)
	}
	if len(f.params[api.MethodBotAdd]) != 1 {
		t.Fatalf("add calls = %d", len(f.params[api.MethodBotAdd]))
	}

	f = &fakeBotNode{add: api.BotAddResult{Bot: api.BotInfo{Name: "scout"}}}
	if _, _, err := execBot(t, f, "add", "/some/dir", "--import-instructions"); err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodBotAdd][0].(api.BotAddParams); !got.ImportInstructions || got.Dir != "/some/dir" {
		t.Fatalf("params = %+v", got)
	}
}

func TestBotListJSON(t *testing.T) {
	f := twoBots()
	out, _, err := execBot(t, f, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res api.BotListResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Bots) != 2 || res.Primary != "a1" {
		t.Fatalf("res = %+v", res)
	}
}

func TestBotNameResolution(t *testing.T) {
	f := &fakeBotNode{bots: []api.BotInfo{
		{ID: "a1", Name: "scout", NodeID: "n1", NodeLabel: "laptop"},
		{ID: "a2", Name: "scout", NodeID: "n2", NodeLabel: "server"},
	}}
	_, errOut, err := execBot(t, f, "show", "scout")
	if err == nil || !strings.Contains(errOut, "on several nodes (laptop, server); use scout@<node> or --node") {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
	if _, _, err := execBot(t, f, "show", "scout@server"); err != nil {
		t.Fatal(err)
	}
	if got := f.params[api.MethodBotShow][0].(nodeParams); got.Node != "n2" || got.Params.(api.BotRef).BotID != "a2" {
		t.Fatalf("show = %+v", got)
	}
	_, errOut, _ = execBot(t, f, "show", "nobody")
	if !strings.Contains(errOut, `unknown bot "nobody"`) {
		t.Fatalf("stderr = %q", errOut)
	}
}

type memoryNode struct {
	*fakeBotNode
	log       api.BotMemoryLogResult
	proposals api.BotMemoryProposalsResult
	decide    api.BotMemoryDecideResult
	show      api.BotMemoryShowResult
	imported  api.BotMemoryImportResult
}

func (m *memoryNode) call(method string, params, out any) error {
	switch method {
	case api.MethodBotMemoryLog:
		*out.(*api.BotMemoryLogResult) = m.log
	case api.MethodBotMemoryUndo:
		*out.(*api.BotMemoryUndoResult) = api.BotMemoryUndoResult{Commit: "rev1"}
	case api.MethodBotMemoryProposals:
		*out.(*api.BotMemoryProposalsResult) = m.proposals
	case api.MethodBotMemoryDecide:
		*out.(*api.BotMemoryDecideResult) = m.decide
	case api.MethodBotMemoryShow:
		*out.(*api.BotMemoryShowResult) = m.show
	case api.MethodBotMemoryImport:
		*out.(*api.BotMemoryImportResult) = m.imported
	default:
		return m.fakeBotNode.call(method, params, out)
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
	root.AddCommand(botCmd(dial))
	root.SetArgs(append([]string{"bot", "memory"}, args...))
	var err error
	out, _ := captureOutput(t, func() { err = root.Execute() })
	return out, err
}

func newMemoryNode() *memoryNode { return &memoryNode{fakeBotNode: twoBots()} }

func TestBotMemoryLogJSON(t *testing.T) {
	m := newMemoryNode()
	m.log = api.BotMemoryLogResult{Commits: []api.BotMemoryCommit{{Hash: "abcdef1234", Subject: "add x", Source: "capture", When: time.Now()}}}
	out, err := execMemory(t, m, "log", "scout", "--limit", "5", "--profile", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res api.BotMemoryLogResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Commits) != 1 || res.Commits[0].Hash != "abcdef1234" {
		t.Errorf("out %q err %v", out, err)
	}
	p := m.params[api.MethodBotMemoryLog][0].(api.BotMemoryLogParams)
	if p.BotID != "a1" || p.Limit != 5 || !p.Profile {
		t.Errorf("params %+v", p)
	}
}

func TestBotMemoryLogTable(t *testing.T) {
	m := newMemoryNode()
	m.log = api.BotMemoryLogResult{Commits: []api.BotMemoryCommit{{Hash: "abcdef1234", Subject: "add x", Source: "capture", When: time.Now()}}}
	out, err := execMemory(t, m, "log", "scout")
	if err != nil || !strings.Contains(out, "abcdef12 ") || !strings.Contains(out, "capture") || !strings.Contains(out, "add x") {
		t.Errorf("out %q err %v", out, err)
	}
	if p := m.params[api.MethodBotMemoryLog][0].(api.BotMemoryLogParams); p.Limit != 20 {
		t.Errorf("default limit %d", p.Limit)
	}
}

func TestBotMemoryUndo(t *testing.T) {
	m := newMemoryNode()
	out, err := execMemory(t, m, "undo", "scout", "abc123", "--profile")
	if err != nil || !strings.Contains(out, "reverted abc123") {
		t.Errorf("out %q err %v", out, err)
	}
	p := m.params[api.MethodBotMemoryUndo][0].(api.BotMemoryUndoParams)
	if p.BotID != "a1" || p.Commit != "abc123" || !p.Profile {
		t.Errorf("params %+v", p)
	}
}

func TestBotMemoryReviewAccept(t *testing.T) {
	m := newMemoryNode()
	m.proposals = api.BotMemoryProposalsResult{Proposals: []api.BotMemoryProposal{
		{ID: "n:mp1", Ops: []api.BotMemoryOp{{Action: "add", Text: "likes tea"}}},
		{ID: "n:mp2", Kind: "instructions", Reason: "tighten tone"},
	}}
	out, err := execMemory(t, m, "review", "scout")
	if err != nil || !strings.Contains(out, "n:mp1") || !strings.Contains(out, "add likes tea") || !strings.Contains(out, "instructions: tighten tone") {
		t.Errorf("out %q err %v", out, err)
	}
	m.decide = api.BotMemoryDecideResult{Applied: 1}
	out, err = execMemory(t, m, "review", "scout", "--accept", "n:mp1")
	if err != nil || !strings.Contains(out, "accepted n:mp1") {
		t.Errorf("out %q err %v", out, err)
	}
	d := m.params[api.MethodBotMemoryDecide][0].(api.BotMemoryDecideParams)
	if d.ProposalID != "n:mp1" || !d.Accept {
		t.Errorf("decide %+v", d)
	}
	if _, err = execMemory(t, m, "review", "scout", "--reject", "n:mp2"); err != nil {
		t.Fatal(err)
	}
	if d := m.params[api.MethodBotMemoryDecide][1].(api.BotMemoryDecideParams); d.Accept || d.ProposalID != "n:mp2" {
		t.Errorf("decide %+v", d)
	}
}

func TestBotMemoryImportStdin(t *testing.T) {
	m := newMemoryNode()
	m.imported = api.BotMemoryImportResult{Staged: 3}
	old := botStdin
	botStdin = strings.NewReader("remember this")
	defer func() { botStdin = old }()
	out, err := execMemory(t, m, "import", "scout", "-")
	if err != nil || !strings.Contains(out, "staged for review: 3") {
		t.Errorf("out %q err %v", out, err)
	}
	if p := m.params[api.MethodBotMemoryImport][0].(api.BotMemoryImportParams); p.Content != "remember this" || p.BotID != "a1" {
		t.Errorf("params %+v", p)
	}
}

func TestBotMemoryShowUsage(t *testing.T) {
	m := newMemoryNode()
	m.show = api.BotMemoryShowResult{Memory: "- a\n", Usage: 14 * 1024, Budget: 24 * 1024, Profile: "prof"}
	out, err := execMemory(t, m, "show", "scout")
	if err != nil || !strings.Contains(out, "MEMORY.md 14 KB / 24 KB") || !strings.Contains(out, "- a") {
		t.Errorf("out %q err %v", out, err)
	}
	out, _ = execMemory(t, m, "show", "scout", "--profile")
	if strings.TrimSpace(out) != "prof" {
		t.Errorf("profile out %q", out)
	}
}
