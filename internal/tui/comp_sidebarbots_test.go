package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func botsTestModel() model {
	m := projectsTestModel()
	m.width, m.height = 160, 40
	m.bots = []api.BotInfo{
		{ID: "n1:i", Name: "idler", Agent: "claude", Status: "idle", NodeLabel: "home"},
		{ID: "n1:w", Name: "worker", Agent: "codex", Status: "working", NodeLabel: "home"},
		{ID: "n1:u", Name: "unready", Agent: "claude", Status: "idle", NodeLabel: "home"},
		{ID: "n1:x", Name: "offline", Agent: "claude", Status: "unreachable", NodeLabel: "far"},
		{ID: "n1:n", Name: "needy", Agent: "claude", Status: "needs_you", NodeLabel: "home"},
		{ID: "n1:q", Name: "waiter", Agent: "claude", Status: "waiting", NodeLabel: "home"},
		{ID: "n1:p", Name: "prime", Agent: "claude", Status: "idle", NodeLabel: "home", Primary: true},
	}
	m.botThreads = map[string][]api.BotThreadInfo{
		"n1:u": {{ID: "n1:t1", BotID: "n1:u", Unread: true}, {ID: "n1:t2", BotID: "n1:u"}},
		"n1:i": {{ID: "n1:t3", BotID: "n1:i"}},
	}
	m.left.tab = leftBots
	return withFocus(m, leftSidebar)
}

func TestBotsTabListsSections(t *testing.T) {
	m := botsTestModel()
	c := &ctx{m: &m}
	out := ansi.Strip(botsSidebarComp{}.view(c, 34, 40))
	order := []string{"Primary", "prime", "Needs you", "needy", "Waiting", "waiter", "Working", "worker",
		"Unread", "unready", "Idle", "idler", "Unreachable", "offline"}
	at := 0
	for _, w := range order {
		i := strings.Index(out[at:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", w, out)
		}
		at += i + len(w)
	}
	if !strings.Contains(out, "Claude · home · 2 threads") || !strings.Contains(out, "Claude · home · 1 thread\n") {
		t.Errorf("rows lack cli · node · thread count:\n%s", out)
	}
	if ids := (botsSidebarComp{}).ids(c); ids[0] != "n1:p" || ids[len(ids)-1] != "n1:x" {
		t.Errorf("ids = %v", ids)
	}
}

// TestBotsTabPrimaryPerNode: each node's primary is listed first, by node,
// with its node on the row.
func TestBotsTabPrimaryPerNode(t *testing.T) {
	m := botsTestModel()
	m.bots = append(m.bots, api.BotInfo{ID: "n2:z", Name: "alpha", Agent: "claude", Status: "idle", NodeLabel: "away", Primary: true})
	c := &ctx{m: &m}
	if ids := (botsSidebarComp{}).ids(c); ids[0] != "n2:z" || ids[1] != "n1:p" {
		t.Fatalf("ids = %v", ids)
	}
	out := ansi.Strip(botsSidebarComp{}.view(c, 34, 40))
	if strings.Count(out, "Primary") != 1 || !strings.Contains(out, "Claude · away") || !strings.Contains(out, "Claude · home") {
		t.Errorf("view:\n%s", out)
	}
}

func TestBotsTabEmptyState(t *testing.T) {
	m := botsTestModel()
	m.bots = nil
	c := &ctx{m: &m}
	if out := ansi.Strip(botsSidebarComp{}.view(c, 34, 20)); !strings.Contains(out, "no bots") {
		t.Fatalf("empty state = %q", out)
	}
	m = botsTestModel()
	c = &ctx{m: &m}
	if out := ansi.Strip(botsSidebarComp{query: "zzz"}.view(c, 34, 20)); !strings.Contains(out, "no bots match /zzz") {
		t.Fatalf("filtered empty state = %q", out)
	}
}

func TestBotsTabFilter(t *testing.T) {
	m := botsTestModel()
	m = typeKeys(m, "/wor")
	m = pressKeys(m, keyMsg("enter"))
	c := &ctx{m: &m}
	if ids := m.left.bots.ids(c); len(ids) != 1 || ids[0] != "n1:w" {
		t.Fatalf("filtered ids = %v", ids)
	}
}

func TestBotsTabEnterOpensBotView(t *testing.T) {
	m := botsTestModel()
	m = typeKeys(m, "j")
	m, _ = upd(m, keyMsg("enter"))
	a, ok := m.baseComp().(botComp)
	if !ok || a.botID != "n1:n" {
		t.Fatalf("base = %#v, want needy's bot view", m.baseComp())
	}
}

func TestLeftTabCycleIncludesBots(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	for _, want := range []leftTab{leftSessions, leftBots, leftProjects} {
		if m = typeKeys(m, "gt"); m.left.tab != want {
			t.Fatalf("gt: tab = %v, want %v", m.left.tab, want)
		}
	}
	m = typeKeys(m, "gT")
	if m.left.tab != leftBots {
		t.Fatalf("gT wraps to bots: %v", m.left.tab)
	}
	if _, ok := m.focusedComp().(botsSidebarComp); !ok {
		t.Fatalf("focused = %T", m.focusedComp())
	}
	if tab, ok := parseLeftTab("bots"); !ok || tab != leftBots {
		t.Fatalf("parseLeftTab(bots) = %v %v", tab, ok)
	}
}

func botViewModel() model {
	m := calmBotsModel()
	m.main = m.main.replaceTop(botComp{botID: "n1:u"})
	return withFocus(m, mainPane)
}

// calmBotsModel has no working bot, so no spinner runs.
func calmBotsModel() model {
	m := botsTestModel()
	m.bots = slices.DeleteFunc(m.bots, func(a api.BotInfo) bool { return a.Status == "working" })
	return m
}

// botClient serves the bot RPCs from fixed data and records the calls.
type botClient struct {
	recordingClient
	list     api.BotListResult
	threads  map[string][]api.BotThreadInfo
	memory   *api.BotMemoryShowResult // nil: bot.memory.show is missing
	commits  []api.BotMemoryCommit
	sent     api.BotSendResult
	timeline api.BotTimeline
	undoErr  error
	props    []api.BotMemoryProposal
	routines []api.BotRoutineInfo
	usage    *api.BotUsageResult
	decided  api.BotMemoryDecideResult
	notFound bool // every bot RPC is missing
}

func (c *botClient) Call(method string, params, out any) error {
	_ = c.recordingClient.Call(method, params, out)
	if c.notFound {
		return &api.RPCError{Code: api.CodeMethodNotFound, Message: "no " + method}
	}
	switch method {
	case api.MethodBotList:
		*out.(*api.BotListResult) = c.list
	case api.MethodBotThreadList:
		*out.(*api.BotThreadsResult) = api.BotThreadsResult{Threads: c.threads[params.(api.BotRef).BotID]}
	case api.MethodBotMemoryShow:
		if c.memory == nil {
			return &api.RPCError{Code: api.CodeMethodNotFound, Message: "no " + method}
		}
		*out.(*api.BotMemoryShowResult) = *c.memory
	case api.MethodBotMemoryLog:
		*out.(*api.BotMemoryLogResult) = api.BotMemoryLogResult{Commits: c.commits}
	case api.MethodBotThreadSend:
		*out.(*api.BotSendResult) = c.sent
	case api.MethodBotThreadTimeline:
		*out.(*api.BotTimeline) = c.timeline
	case api.MethodBotMemoryUndo:
		return c.undoErr
	case api.MethodBotMemoryProposals:
		*out.(*api.BotMemoryProposalsResult) = api.BotMemoryProposalsResult{Proposals: c.props}
	case api.MethodSessionToolDetail, api.MethodSessionHistoryToolDetail:
		*out.(*api.ToolDetail) = api.ToolDetail{ToolInput: `{"command":"ls"}`, Result: "detail from " + method}
	case api.MethodBotUsage:
		if c.usage == nil {
			return &api.RPCError{Code: api.CodeMethodNotFound, Message: "no " + method}
		}
		*out.(*api.BotUsageResult) = *c.usage
	case api.MethodBotRoutineList:
		*out.(*api.BotRoutineListResult) = api.BotRoutineListResult{Routines: c.routines}
	case api.MethodBotMemoryDecide:
		*out.(*api.BotMemoryDecideResult) = c.decided
	}
	return nil
}

func TestLoadBotsKeepsBotsAndThreads(t *testing.T) {
	cl := &botClient{
		list: api.BotListResult{Bots: []api.BotInfo{
			{ID: "n1:a", Name: "amy", Status: "idle", Primary: true},
			{ID: "n1:b", Name: "bob", Status: "unreachable"},
		}},
		threads: map[string][]api.BotThreadInfo{"n1:a": {{ID: "n1:t", Unread: true}}},
	}
	m := testModel()
	m.client = cl
	m, _ = upd(m, m.loadBotsCmd()())
	if len(m.bots) != 2 || !m.bots[0].Primary || m.botNames["n1:b"] != "bob" || !m.botUnread("n1:a") {
		t.Fatalf("bots=%v names=%v threads=%v", m.bots, m.botNames, m.botThreads)
	}
	for i, call := range cl.calls {
		if call == api.MethodBotThreadList && cl.params[i].(api.BotRef).BotID == "n1:b" {
			t.Error("threads asked of an unreachable bot")
		}
	}
	m.client = &botClient{notFound: true}
	m, _ = upd(m, m.loadBotsCmd()())
	if len(m.bots) != 0 || len(m.botNames) != 0 {
		t.Fatalf("an older node lists no bots: %v", m.bots)
	}
}

func TestBotPromptsTakePaste(t *testing.T) {
	m := botsTestModel()
	m = typeKeys(m, "/")
	m, _ = upd(m, tea.PasteMsg{Content: "wor"})
	m = pressKeys(m, keyMsg("enter"))
	if ids := m.left.bots.ids(&ctx{m: &m}); len(ids) != 1 || ids[0] != "n1:w" {
		t.Fatalf("pasted filter ids = %v", ids)
	}

	cl := threadClient()
	m = loadedBotView(t, cl)
	m = typeKeys(m, "n")
	m, _ = upd(m, tea.PasteMsg{Content: "pasted chat"})
	if v := m.baseComp().(botComp).chat.input.Value(); v != "pasted chat" {
		t.Fatalf("new chat prompt = %q", v)
	}
	m = pressKeys(m, keyMsg("esc"))

	m = openedThread(t, cl)
	m = pressKeys(m, keyMsg("tab"))
	m, _ = upd(m, tea.PasteMsg{Content: "pasted reply"})
	if v := m.main.top().(threadComp).reply.input.Value(); v != "pasted reply" {
		t.Fatalf("thread reply prompt = %q", v)
	}
}
