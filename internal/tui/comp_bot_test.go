package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func botViewThreads() []api.BotThreadInfo {
	now := time.Now()
	return []api.BotThreadInfo{
		{ID: "n1:t1", BotID: "n1:u", Kind: "chat", Title: "flat search", Unread: true, LastActiveAt: now.Add(-2 * time.Minute),
			SessionID: "n1:s1", SessionStatus: "idle"},
		{ID: "n1:t2", BotID: "n1:u", Kind: "run", Title: "migrate-billing", Status: "closed", LastActiveAt: now.Add(-3 * time.Hour)},
	}
}

// loadedBotView opens unready's bot view and feeds it its load.
func loadedBotView(t *testing.T, cl *botClient) model {
	t.Helper()
	m := calmBotsModel()
	m.client = cl
	c := &ctx{m: &m}
	cmd := openBot(c, "n1:u")
	m.apply(c)
	if cmd == nil {
		t.Fatal("opening the bot view loads it")
	}
	m, _ = upd(m, cmd())
	return m
}

func botViewClient() *botClient {
	return &botClient{
		threads: map[string][]api.BotThreadInfo{"n1:u": botViewThreads()},
		memory:  &api.BotMemoryShowResult{Usage: 14 * 1024, Budget: 24 * 1024},
		commits: []api.BotMemoryCommit{{Hash: "abc", When: time.Now().Add(-time.Hour)}},
	}
}

func TestBotViewRendersThreadsAndMemory(t *testing.T) {
	m := loadedBotView(t, botViewClient())
	a, ok := m.baseComp().(botComp)
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

	// A node without bot.memory.show hides the memory line.
	cl := botViewClient()
	cl.memory = nil
	m = loadedBotView(t, cl)
	if out := ansi.Strip(m.baseComp().view(&ctx{m: &m}, 100, 30)); strings.Contains(out, "Memory") {
		t.Errorf("memory shown without the RPC:\n%s", out)
	}
}

func TestBotViewEnterOpensThread(t *testing.T) {
	m := loadedBotView(t, botViewClient())
	m = typeKeys(m, "j")
	m, cmd := upd(m, keyMsg("enter"))
	if th, ok := m.main.top().(threadComp); !ok || th.threadID != "n1:t2" || cmd == nil {
		t.Fatalf("enter opens the thread view: top = %T", m.main.top())
	}
}

func TestBotViewNewChatSends(t *testing.T) {
	cl := botViewClient()
	cl.sent = api.BotSendResult{ThreadID: "n1:t9", SessionID: "n1:s9"}
	m := loadedBotView(t, cl)
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
	var p api.BotSendParams
	for i, call := range cl.calls {
		if call == api.MethodBotThreadSend {
			p = cl.params[i].(api.BotSendParams)
		}
	}
	if p != (api.BotSendParams{BotID: "n1:u", Text: "hello there", New: true}) {
		t.Fatalf("send params = %+v", p)
	}
	if th, ok := m.main.top().(threadComp); !ok || th.threadID != "n1:t9" {
		t.Fatalf("the new thread opens: top = %T", m.main.top())
	}
}

func TestBotViewMakePrimary(t *testing.T) {
	cl := botViewClient()
	m := loadedBotView(t, cl)
	m, cmd := upd(m, cmdMsg("bot make-primary"))
	if cmd == nil {
		t.Fatal("make-primary calls the node")
	}
	m, _ = upd(m, cmd())
	var got []api.BotPrimaryParams
	for i, call := range cl.calls {
		if call == api.MethodBotSetPrimary {
			got = append(got, cl.params[i].(api.BotPrimaryParams))
		}
	}
	if len(got) != 1 || got[0].BotID != "n1:u" {
		t.Fatalf("setPrimary params = %+v", got)
	}

	m.botPrimary = "n1:u"
	m, cmd = upd(m, cmdMsg("bot unset-primary"))
	if cmd == nil {
		t.Fatal("unset-primary calls the node")
	}
	cmd()
	got = nil
	for i, call := range cl.calls {
		if call == api.MethodBotSetPrimary {
			got = append(got, cl.params[i].(api.BotPrimaryParams))
		}
	}
	if len(got) != 2 || got[1].BotID != "" {
		t.Fatalf("unset sends an empty id: %+v", got)
	}
	a := m.baseComp().(botComp)
	if h, _ := a.paneHeader(&ctx{m: &m}, 100); !strings.Contains(ansi.Strip(h), "primary") {
		t.Errorf("header lacks primary: %q", ansi.Strip(h))
	}
}

func TestBotViewUnreachableDisablesActions(t *testing.T) {
	cl := botViewClient()
	m := calmBotsModel()
	m.client = cl
	c := &ctx{m: &m}
	openBot(c, "n1:x")
	m.apply(c)
	for _, press := range []func(model) (model, tea.Cmd){
		func(m model) (model, tea.Cmd) { return upd(m, keyMsg("n")) },
		func(m model) (model, tea.Cmd) { return upd(m, keyMsg("m")) },
		func(m model) (model, tea.Cmd) { return upd(m, cmdMsg("bot make-primary")) },
	} {
		mm, cmd := press(m)
		if cmd != nil || mm.flash != "bot's node is offline" || mm.baseComp().raw(&ctx{m: &mm}) {
			t.Errorf("flash = %q cmd = %v", mm.flash, cmd != nil)
		}
	}
	if out := ansi.Strip(m.baseComp().view(&ctx{m: &m}, 100, 30)); !strings.Contains(out, "bot's node is offline") {
		t.Errorf("view:\n%s", out)
	}
	for _, call := range cl.calls {
		if call != api.MethodBotList {
			t.Errorf("an unreachable bot's view called %s", call)
		}
	}
}
