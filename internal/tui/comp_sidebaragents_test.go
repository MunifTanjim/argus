package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func agentsTestModel() model {
	m := projectsTestModel()
	m.width, m.height = 160, 40
	m.pagents = []api.PAgentInfo{
		{ID: "n1:i", Name: "idler", Agent: "claude", Status: "idle", NodeLabel: "home"},
		{ID: "n1:w", Name: "worker", Agent: "codex", Status: "working", NodeLabel: "home"},
		{ID: "n1:u", Name: "unready", Agent: "claude", Status: "idle", NodeLabel: "home"},
		{ID: "n1:x", Name: "offline", Agent: "claude", Status: "unreachable", NodeLabel: "far"},
		{ID: "n1:n", Name: "needy", Agent: "claude", Status: "needs_you", NodeLabel: "home"},
		{ID: "n1:q", Name: "waiter", Agent: "claude", Status: "waiting", NodeLabel: "home"},
		{ID: "n1:p", Name: "prime", Agent: "claude", Status: "idle", NodeLabel: "home"},
	}
	m.pagentPrimary = "n1:p"
	m.pagentThreads = map[string][]api.PAgentThreadInfo{
		"n1:u": {{ID: "n1:t1", AgentID: "n1:u", Unread: true}, {ID: "n1:t2", AgentID: "n1:u"}},
		"n1:i": {{ID: "n1:t3", AgentID: "n1:i"}},
	}
	m.left.tab = leftAgents
	return withFocus(m, leftSidebar)
}

func TestAgentsTabListsSections(t *testing.T) {
	m := agentsTestModel()
	c := &ctx{m: &m}
	out := ansi.Strip(agentsSidebarComp{}.view(c, 34, 40))
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
	if ids := (agentsSidebarComp{}).ids(c); ids[0] != "n1:p" || ids[len(ids)-1] != "n1:x" {
		t.Errorf("ids = %v", ids)
	}
}

func TestAgentsTabEmptyState(t *testing.T) {
	m := agentsTestModel()
	m.pagents = nil
	c := &ctx{m: &m}
	if out := ansi.Strip(agentsSidebarComp{}.view(c, 34, 20)); !strings.Contains(out, "no agents") {
		t.Fatalf("empty state = %q", out)
	}
	m = agentsTestModel()
	c = &ctx{m: &m}
	if out := ansi.Strip(agentsSidebarComp{query: "zzz"}.view(c, 34, 20)); !strings.Contains(out, "no agents match /zzz") {
		t.Fatalf("filtered empty state = %q", out)
	}
}

func TestAgentsTabFilter(t *testing.T) {
	m := agentsTestModel()
	m = typeKeys(m, "/wor")
	m = pressKeys(m, keyMsg("enter"))
	c := &ctx{m: &m}
	if ids := m.left.agents.ids(c); len(ids) != 1 || ids[0] != "n1:w" {
		t.Fatalf("filtered ids = %v", ids)
	}
}

func TestAgentsTabEnterOpensAgentView(t *testing.T) {
	m := agentsTestModel()
	m = typeKeys(m, "j")
	m, _ = upd(m, keyMsg("enter"))
	a, ok := m.baseComp().(agentComp)
	if !ok || a.agentID != "n1:n" {
		t.Fatalf("base = %#v, want needy's agent view", m.baseComp())
	}
}

func TestLeftTabCycleIncludesAgents(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	for _, want := range []leftTab{leftSessions, leftAgents, leftProjects} {
		if m = typeKeys(m, "gt"); m.left.tab != want {
			t.Fatalf("gt: tab = %v, want %v", m.left.tab, want)
		}
	}
	m = typeKeys(m, "gT")
	if m.left.tab != leftAgents {
		t.Fatalf("gT wraps to agents: %v", m.left.tab)
	}
	if _, ok := m.focusedComp().(agentsSidebarComp); !ok {
		t.Fatalf("focused = %T", m.focusedComp())
	}
	if tab, ok := parseLeftTab("agents"); !ok || tab != leftAgents {
		t.Fatalf("parseLeftTab(agents) = %v %v", tab, ok)
	}
}

func agentViewModel() model {
	m := calmAgentsModel()
	m.main = m.main.replaceTop(agentComp{agentID: "n1:u"})
	return withFocus(m, mainPane)
}

// calmAgentsModel has no working agent, so no spinner runs.
func calmAgentsModel() model {
	m := agentsTestModel()
	m.pagents = slices.DeleteFunc(m.pagents, func(a api.PAgentInfo) bool { return a.Status == "working" })
	return m
}

// pagentClient serves the pagent RPCs from fixed data and records the calls.
type pagentClient struct {
	recordingClient
	list     api.PAgentListResult
	threads  map[string][]api.PAgentThreadInfo
	memory   *api.PAgentMemoryShowResult // nil: pagent.memory.show is missing
	commits  []api.PAgentMemoryCommit
	sent     api.PAgentSendResult
	timeline api.PAgentTimeline
	undoErr  error
	props    []api.PAgentMemoryProposal
	routines []api.PAgentRoutineInfo
	usage    *api.PAgentUsageResult
	decided  api.PAgentMemoryDecideResult
	notFound bool // every pagent RPC is missing
}

func (c *pagentClient) Call(method string, params, out any) error {
	_ = c.recordingClient.Call(method, params, out)
	if c.notFound {
		return &api.RPCError{Code: api.CodeMethodNotFound, Message: "no " + method}
	}
	switch method {
	case api.MethodPAgentList:
		*out.(*api.PAgentListResult) = c.list
	case api.MethodPAgentThreadList:
		*out.(*api.PAgentThreadsResult) = api.PAgentThreadsResult{Threads: c.threads[params.(api.PAgentRef).AgentID]}
	case api.MethodPAgentMemoryShow:
		if c.memory == nil {
			return &api.RPCError{Code: api.CodeMethodNotFound, Message: "no " + method}
		}
		*out.(*api.PAgentMemoryShowResult) = *c.memory
	case api.MethodPAgentMemoryLog:
		*out.(*api.PAgentMemoryLogResult) = api.PAgentMemoryLogResult{Commits: c.commits}
	case api.MethodPAgentThreadSend:
		*out.(*api.PAgentSendResult) = c.sent
	case api.MethodPAgentThreadTimeline:
		*out.(*api.PAgentTimeline) = c.timeline
	case api.MethodPAgentMemoryUndo:
		return c.undoErr
	case api.MethodPAgentMemoryProposals:
		*out.(*api.PAgentMemoryProposalsResult) = api.PAgentMemoryProposalsResult{Proposals: c.props}
	case api.MethodSessionToolDetail, api.MethodSessionHistoryToolDetail:
		*out.(*api.ToolDetail) = api.ToolDetail{ToolInput: `{"command":"ls"}`, Result: "detail from " + method}
	case api.MethodPAgentUsage:
		if c.usage == nil {
			return &api.RPCError{Code: api.CodeMethodNotFound, Message: "no " + method}
		}
		*out.(*api.PAgentUsageResult) = *c.usage
	case api.MethodPAgentRoutineList:
		*out.(*api.PAgentRoutineListResult) = api.PAgentRoutineListResult{Routines: c.routines}
	case api.MethodPAgentMemoryDecide:
		*out.(*api.PAgentMemoryDecideResult) = c.decided
	}
	return nil
}

func TestLoadPAgentsKeepsAgentsAndThreads(t *testing.T) {
	cl := &pagentClient{
		list: api.PAgentListResult{Primary: "n1:a", Agents: []api.PAgentInfo{
			{ID: "n1:a", Name: "amy", Status: "idle"},
			{ID: "n1:b", Name: "bob", Status: "unreachable"},
		}},
		threads: map[string][]api.PAgentThreadInfo{"n1:a": {{ID: "n1:t", Unread: true}}},
	}
	m := testModel()
	m.client = cl
	m, _ = upd(m, m.loadPAgentsCmd()())
	if m.pagentPrimary != "n1:a" || len(m.pagents) != 2 || m.pagentNames["n1:b"] != "bob" || !m.pagentUnread("n1:a") {
		t.Fatalf("primary=%q agents=%v names=%v threads=%v", m.pagentPrimary, m.pagents, m.pagentNames, m.pagentThreads)
	}
	for i, call := range cl.calls {
		if call == api.MethodPAgentThreadList && cl.params[i].(api.PAgentRef).AgentID == "n1:b" {
			t.Error("threads asked of an unreachable agent")
		}
	}
	m.client = &pagentClient{notFound: true}
	m, _ = upd(m, m.loadPAgentsCmd()())
	if len(m.pagents) != 0 || len(m.pagentNames) != 0 {
		t.Fatalf("an older node lists no agents: %v", m.pagents)
	}
}

func TestAgentPromptsTakePaste(t *testing.T) {
	m := agentsTestModel()
	m = typeKeys(m, "/")
	m, _ = upd(m, tea.PasteMsg{Content: "wor"})
	m = pressKeys(m, keyMsg("enter"))
	if ids := m.left.agents.ids(&ctx{m: &m}); len(ids) != 1 || ids[0] != "n1:w" {
		t.Fatalf("pasted filter ids = %v", ids)
	}

	cl := threadClient()
	m = loadedAgentView(t, cl)
	m = typeKeys(m, "n")
	m, _ = upd(m, tea.PasteMsg{Content: "pasted chat"})
	if v := m.baseComp().(agentComp).chat.input.Value(); v != "pasted chat" {
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
