package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func agentViewThreads() []api.PAgentThreadInfo {
	now := time.Now()
	return []api.PAgentThreadInfo{
		{ID: "n1:t1", AgentID: "n1:u", Kind: "chat", Title: "flat search", Unread: true, LastActiveAt: now.Add(-2 * time.Minute),
			SessionID: "n1:s1", SessionStatus: "idle"},
		{ID: "n1:t2", AgentID: "n1:u", Kind: "run", Title: "migrate-billing", Status: "closed", LastActiveAt: now.Add(-3 * time.Hour)},
	}
}

// loadedAgentView opens unready's agent view and feeds it its load.
func loadedAgentView(t *testing.T, cl *pagentClient) model {
	t.Helper()
	m := calmAgentsModel()
	m.client = cl
	c := &ctx{m: &m}
	cmd := openAgent(c, "n1:u")
	m.apply(c)
	if cmd == nil {
		t.Fatal("opening the agent view loads it")
	}
	m, _ = upd(m, cmd())
	return m
}

func agentViewClient() *pagentClient {
	return &pagentClient{
		threads: map[string][]api.PAgentThreadInfo{"n1:u": agentViewThreads()},
		memory:  &api.PAgentMemoryShowResult{Usage: 14 * 1024, Budget: 24 * 1024},
		commits: []api.PAgentMemoryCommit{{Hash: "abc", When: time.Now().Add(-time.Hour)}},
	}
}

func TestAgentViewRendersThreadsAndMemory(t *testing.T) {
	m := loadedAgentView(t, agentViewClient())
	a, ok := m.baseComp().(agentComp)
	if !ok {
		t.Fatalf("base = %T", m.baseComp())
	}
	c := &ctx{m: &m}
	out := ansi.Strip(a.view(c, 100, 30))
	for _, want := range []string{"Threads", "flat search", "chat · 2m ago", "migrate-billing", "run · 3h ago",
		"Routines", "no routines", "Memory · 14 KB / 24 KB · last change 1h ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "flat search •") {
		t.Errorf("the unread thread lacks its mark:\n%s", out)
	}
	if h, _ := a.paneHeader(c, 100); !strings.Contains(ansi.Strip(h), "unready · Claude · home: home") {
		t.Errorf("header = %q", ansi.Strip(h))
	}

	// A node without pagent.memory.show hides the memory line.
	cl := agentViewClient()
	cl.memory = nil
	m = loadedAgentView(t, cl)
	if out := ansi.Strip(m.baseComp().view(&ctx{m: &m}, 100, 30)); strings.Contains(out, "Memory") {
		t.Errorf("memory shown without the RPC:\n%s", out)
	}
}

func TestAgentViewEnterOpensThreadSession(t *testing.T) {
	m := loadedAgentView(t, agentViewClient())
	c := &ctx{m: &m}
	m.baseComp().handleKey(c, keyMsg("enter"))
	if len(c.actions) == 0 || c.actions[0].kind != actOpenSession || c.actions[0].id != "n1:s1" {
		t.Fatalf("enter opens the thread's session: %v", actionKinds(c))
	}
	m = typeKeys(m, "j")
	m, _ = upd(m, keyMsg("enter"))
	if m.flash != "thread view comes next" {
		t.Fatalf("a thread without a session: flash = %q", m.flash)
	}
}

func TestAgentViewNewChatSends(t *testing.T) {
	cl := agentViewClient()
	cl.sent = api.PAgentSendResult{ThreadID: "n1:t9", SessionID: "n1:s9"}
	m := loadedAgentView(t, cl)
	m = typeKeys(m, "n")
	if !m.baseComp().raw(&ctx{m: &m}) {
		t.Fatal("n opens the chat prompt")
	}
	m = typeKeys(m, "hello there")
	m, cmd := upd(m, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter sends")
	}
	m, _ = upd(m, cmd())
	var p api.PAgentSendParams
	for i, call := range cl.calls {
		if call == api.MethodPAgentThreadSend {
			p = cl.params[i].(api.PAgentSendParams)
		}
	}
	if p != (api.PAgentSendParams{AgentID: "n1:u", Text: "hello there", New: true}) {
		t.Fatalf("send params = %+v", p)
	}
	if tr, ok := m.baseComp().(transcriptComp); !ok || tr.sessionID != "n1:s9" {
		t.Fatalf("the new thread opens: base = %T", m.baseComp())
	}
}

func TestAgentViewMakePrimary(t *testing.T) {
	cl := agentViewClient()
	m := loadedAgentView(t, cl)
	m, cmd := upd(m, cmdMsg("agent make-primary"))
	if cmd == nil {
		t.Fatal("make-primary calls the node")
	}
	m, _ = upd(m, cmd())
	var got []api.PAgentPrimaryParams
	for i, call := range cl.calls {
		if call == api.MethodPAgentSetPrimary {
			got = append(got, cl.params[i].(api.PAgentPrimaryParams))
		}
	}
	if len(got) != 1 || got[0].AgentID != "n1:u" {
		t.Fatalf("setPrimary params = %+v", got)
	}

	m.pagentPrimary = "n1:u"
	m, cmd = upd(m, cmdMsg("agent unset-primary"))
	if cmd == nil {
		t.Fatal("unset-primary calls the node")
	}
	cmd()
	got = nil
	for i, call := range cl.calls {
		if call == api.MethodPAgentSetPrimary {
			got = append(got, cl.params[i].(api.PAgentPrimaryParams))
		}
	}
	if len(got) != 2 || got[1].AgentID != "" {
		t.Fatalf("unset sends an empty id: %+v", got)
	}
	a := m.baseComp().(agentComp)
	if h, _ := a.paneHeader(&ctx{m: &m}, 100); !strings.Contains(ansi.Strip(h), "primary") {
		t.Errorf("header lacks primary: %q", ansi.Strip(h))
	}
}

func TestAgentViewUnreachableDisablesActions(t *testing.T) {
	cl := agentViewClient()
	m := calmAgentsModel()
	m.client = cl
	c := &ctx{m: &m}
	openAgent(c, "n1:x")
	m.apply(c)
	for _, press := range []func(model) (model, tea.Cmd){
		func(m model) (model, tea.Cmd) { return upd(m, keyMsg("n")) },
		func(m model) (model, tea.Cmd) { return upd(m, keyMsg("m")) },
		func(m model) (model, tea.Cmd) { return upd(m, cmdMsg("agent make-primary")) },
	} {
		mm, cmd := press(m)
		if cmd != nil || mm.flash != "agent's node is offline" || mm.baseComp().raw(&ctx{m: &mm}) {
			t.Errorf("flash = %q cmd = %v", mm.flash, cmd != nil)
		}
	}
	if out := ansi.Strip(m.baseComp().view(&ctx{m: &m}, 100, 30)); !strings.Contains(out, "agent's node is offline") {
		t.Errorf("view:\n%s", out)
	}
	for _, call := range cl.calls {
		if call != api.MethodPAgentList {
			t.Errorf("an unreachable agent's view called %s", call)
		}
	}
}
