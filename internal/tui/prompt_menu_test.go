package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestCompletionQuery(t *testing.T) {
	for _, tc := range []struct {
		triggers, before string
		trigger          byte
		query            string
		ok               bool
	}{
		{"/", "/com", '/', "com", true},
		{"/", "fix /com", 0, "", false},
		{"/", "/compact now", 0, "", false},
		{"/$", "use $sk", '$', "sk", true},
		{"/$", "a$sk", 0, "", false},
		{"/@", "@", '@', "", true},
		{"/", "@x", 0, "", false},
		{"/@", "line\n@sk", '@', "sk", true},
		{"/", "/a\nb", 0, "", false},
		{"", "/com", 0, "", false},
		{"/$", "/tmp/x use $sk", '$', "sk", true},
		{"/@", "/compact @sk", '@', "sk", true},
		{"/@", "/review @", '@', "", true},
		{"/$", "/tmp/x is broken", 0, "", false},
		{"/$", "/tmp/x /co", 0, "", false},
	} {
		trigger, query, ok := completionQuery(tc.triggers, tc.before)
		if trigger != tc.trigger || query != tc.query || ok != tc.ok {
			t.Errorf("completionQuery(%q, %q) = %q, %q, %v; want %q, %q, %v",
				tc.triggers, tc.before, trigger, query, ok, tc.trigger, tc.query, tc.ok)
		}
	}
}

func TestApplyCompletion(t *testing.T) {
	if got := applyCompletion("use $sk", '$', "sk", "skill-a"); got != "use $skill-a " {
		t.Errorf("applyCompletion = %q, want %q", got, "use $skill-a ")
	}
	if got := applyCompletion("/", '/', "", "clear"); got != "/clear " {
		t.Errorf("applyCompletion = %q, want %q", got, "/clear ")
	}
}

func TestPromptTriggers(t *testing.T) {
	for agent, want := range map[string]string{"claude": "/", "antigravity": "/", "codex": "/$", "opencode": "/@", "pi": ""} {
		if got := promptTriggers(agent); got != want {
			t.Errorf("promptTriggers(%q) = %q, want %q", agent, got, want)
		}
	}
}

var menuItems = []api.PromptCompletion{
	{Name: "compact", Description: "Compact the conversation"},
	{Name: "clear", Description: "Clear the conversation"},
}

func menuModel(agent string) model {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	s := m.sessions["s1"]
	s.Agent = agent
	m.sessions["s1"] = s
	return m
}

func cachedMenuModel() model {
	m := menuModel("claude")
	m.completions = map[string]map[byte][]api.PromptCompletion{"s1": {'/': menuItems}}
	return m
}

func TestPromptMenuEnterSelects(t *testing.T) {
	m := typeKeys(cachedMenuModel(), "/c")
	if !m.promptMenuShown() {
		t.Fatal("the menu should open over cached items")
	}
	name := func(it api.PromptCompletion) string { return it.Name }
	want := "/" + menuItems[fuzzyFind(menuItems, name, "c")[0].Index].Name + " "
	m, cmd := upd(m, keyMsg("enter"))
	if got := m.dock.reply.Value(); got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if m.promptMenuShown() {
		t.Error("the menu should close after a selection")
	}
	if cmd != nil {
		if _, sent := cmd().(inputSentMsg); sent {
			t.Error("Enter on the menu sent the prompt")
		}
	}
	if m.focused != sessionDock {
		t.Errorf("focus = %v, want the dock kept", m.focused)
	}
}

func TestPromptMenuSelectKeepsTextAfterCursor(t *testing.T) {
	m := menuModel("codex")
	m.completions = map[string]map[byte][]api.PromptCompletion{"s1": {'$': {{Name: "skill-a"}}}}
	m = typeKeys(m, "use  now")
	for range len(" now") {
		m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	m = typeKeys(m, "$sk")
	m = pressKeys(m, keyMsg("tab"))
	if got := m.dock.reply.Value(); got != "use $skill-a  now" {
		t.Fatalf("prompt = %q, want %q", got, "use $skill-a  now")
	}
	m = typeKeys(m, "x")
	if got := m.dock.reply.Value(); got != "use $skill-a x now" {
		t.Errorf("typing after a selection = %q, want the cursor after the inserted name", got)
	}
}

func TestPromptMenuNavigation(t *testing.T) {
	m := typeKeys(cachedMenuModel(), "/")
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.dock.menu.sel != 1 {
		t.Fatalf("<Down>: sel = %d, want 1", m.dock.menu.sel)
	}
	m = pressKeys(m, ctrlKey('p'))
	if m.dock.menu.sel != 0 {
		t.Fatalf("<C-p>: sel = %d, want 0", m.dock.menu.sel)
	}
	m = pressKeys(m, ctrlKey('n'), keyMsg("enter"))
	if got := m.dock.reply.Value(); got != "/clear " {
		t.Errorf("prompt = %q, want /clear", got)
	}
}

func TestPromptMenuEnterSendsWhenClosed(t *testing.T) {
	m := typeKeys(cachedMenuModel(), "hello")
	if m.promptMenuShown() {
		t.Fatal("no menu should open for plain text")
	}
	m.client = &recordingClient{}
	m, cmd := upd(m, keyMsg("enter"))
	if m.dock.reply.Value() != "" {
		t.Fatalf("Enter: prompt = %q, want it cleared", m.dock.reply.Value())
	}
	sent, ok := findMsg[inputSentMsg](cmd)
	if !ok || sent.id != "s1" || sent.text != "hello" {
		t.Fatalf("Enter: sent %+v (found %v), want hello sent to s1", sent, ok)
	}
}

// findMsg runs cmd and the commands of any batch it returns until one yields
// a T.
func findMsg[T tea.Msg](cmd tea.Cmd) (T, bool) {
	var zero T
	if cmd == nil {
		return zero, false
	}
	switch msg := cmd().(type) {
	case T:
		return msg, true
	case tea.BatchMsg:
		for _, c := range msg {
			if found, ok := findMsg[T](c); ok {
				return found, true
			}
		}
	}
	return zero, false
}

func TestPromptMenuFetchesOnNewTrigger(t *testing.T) {
	m, cmd := typeKeysCmd(menuModel("claude"), "/")
	if cmd == nil || m.dock.completeSeq != 1 {
		t.Fatalf("typing the trigger: cmd = %v seq = %d, want a fetch", cmd, m.dock.completeSeq)
	}
	m = typeKeys(m, "co")
	if m.dock.completeSeq != 1 {
		t.Errorf("typing after the trigger fetched again: seq = %d", m.dock.completeSeq)
	}
	if m = typeKeys(menuModel("pi"), "/"); m.dock.completeSeq != 0 {
		t.Error("an agent without triggers fetched completions")
	}
}

func TestPromptMenuEmptyResultNotCached(t *testing.T) {
	m := typeKeys(menuModel("claude"), "/")
	m, _ = upd(m, promptCompleteMsg{id: "s1", trigger: '/', seq: m.dock.completeSeq})
	if _, ok := m.completions["s1"]['/']; ok {
		t.Fatal("an empty result was cached")
	}
	if m.promptMenuShown() {
		t.Fatal("an empty result opened the menu")
	}
	m, _ = upd(m, promptCompleteMsg{id: "s1", trigger: '/', seq: m.dock.completeSeq, items: menuItems})
	if _, ok := m.completions["s1"]['/']; !ok {
		t.Fatal("a non-empty result was not cached")
	}
	if !m.promptMenuShown() {
		t.Fatal("a non-empty result did not open the menu")
	}
}

func TestPromptMenuErrorShowsNothing(t *testing.T) {
	m := typeKeys(menuModel("claude"), "/")
	m, _ = upd(m, promptCompleteMsg{id: "s1", trigger: '/', seq: m.dock.completeSeq, err: errString("unknown method")})
	if m.promptMenuShown() || m.flash != "" || len(m.completions["s1"]) != 0 {
		t.Errorf("an error: menu %v flash %q cache %v, want nothing", m.promptMenuShown(), m.flash, m.completions)
	}
}

func TestPromptMenuStaleResultIgnored(t *testing.T) {
	m := typeKeys(menuModel("claude"), "/")
	stale := m.dock.completeSeq
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = typeKeys(m, "/")
	m, _ = upd(m, promptCompleteMsg{id: "s1", trigger: '/', seq: stale, items: menuItems})
	if m.promptMenuShown() || len(m.completions["s1"]) != 0 {
		t.Error("a superseded result was used")
	}
}

func TestPromptMenuCacheDroppedOnSend(t *testing.T) {
	m := typeKeys(cachedMenuModel(), "hi")
	m = pressKeys(m, keyMsg("enter"))
	if _, ok := m.completions["s1"]; ok {
		t.Error("the session's cache survived a send")
	}
}

func TestPromptMenuEscKeepsText(t *testing.T) {
	m := typeKeys(cachedMenuModel(), "/co")
	m = pressKeys(m, keyMsg("esc"))
	if m.promptMenuShown() {
		t.Error("<Esc> should close the menu")
	}
	if got := m.dock.reply.Value(); got != "/co" {
		t.Errorf("prompt = %q, want /co", got)
	}
	if m.focused != sessionDock {
		t.Errorf("focus = %v, want the dock kept", m.focused)
	}
	if m = typeKeys(m, "m"); m.promptMenuShown() {
		t.Error("the menu reopened in the dismissed word")
	}
}

func TestPromptMenuDrawsAboveTheInput(t *testing.T) {
	m := typeKeys(cachedMenuModel(), "/c")
	lines := strings.Split(ansi.Strip(m.frame()), "\n")
	rule := -1
	for i, l := range lines {
		if strings.Contains(l, "/c") && strings.Contains(l, promptGlyph) {
			rule = i - 1
		}
	}
	if rule < 2 {
		t.Fatalf("no input line in the frame:\n%s", strings.Join(lines, "\n"))
	}
	box := strings.Join(lines[:rule], "\n")
	if !strings.Contains(box, "compact") || !strings.Contains(box, "clear") || !strings.Contains(box, "Clear the conversation") {
		t.Fatalf("the menu is not drawn above the input:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[rule-1], "╰") {
		t.Errorf("the menu should end right above the dock's top rule:\n%s", strings.Join(lines, "\n"))
	}
	col := strings.Index(lines[rule+1], "/c")
	for _, l := range lines[:rule] {
		if i := strings.Index(l, "clear"); i >= 0 && ansi.StringWidth(l[:i]) != ansi.StringWidth(lines[rule+1][:col]) {
			t.Errorf("the names should align with the input text:\n%s", strings.Join(lines, "\n"))
		}
	}
}

func TestPromptMenuClosesOnSessionSwitch(t *testing.T) {
	m := typeKeys(menuModel("claude"), "/")
	old := m.dock.completeSeq
	m, _ = upd(m, promptCompleteMsg{id: "s1", trigger: '/', seq: old, items: menuItems})
	if !m.promptMenuShown() {
		t.Fatal("the menu should open")
	}
	m.completions = nil
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = typeKeys(m, "/")
	inFlight := m.dock.completeSeq

	s2 := m.sessions["s1"]
	s2.ID = "s2"
	m.sessions["s2"] = s2
	m = withTr(m, func(t *transcriptComp) { t.sessionID = "s2" })
	m = m.syncDock()
	if m.dock.session != "s2" || m.promptMenuShown() {
		t.Fatalf("after the switch: dock session %q menu shown %v, want s2 and closed", m.dock.session, m.promptMenuShown())
	}
	m, _ = upd(m, promptCompleteMsg{id: "s1", trigger: '/', seq: inFlight, items: menuItems})
	if m.promptMenuShown() || len(m.completions) != 0 {
		t.Errorf("the old session's fetch was used: menu %v cache %v", m.promptMenuShown(), m.completions)
	}
}

func manyMenuItems(n int) []api.PromptCompletion {
	items := make([]api.PromptCompletion, n)
	for i := range items {
		items[i] = api.PromptCompletion{Name: fmt.Sprintf("cmd%02d", i), Description: "d"}
	}
	return items
}

func TestPromptMenuStaysBelowTheTitle(t *testing.T) {
	m := menuModel("claude")
	m.completions = map[string]map[byte][]api.PromptCompletion{"s1": {'/': manyMenuItems(cmdListRows)}}
	m, _ = upd(m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = typeKeys(m, "/")
	if !m.promptMenuShown() {
		t.Fatal("the menu should open")
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	top := -1
	for i, l := range lines {
		if strings.Contains(l, "╭") && strings.Contains(l, "╮") {
			top = i
			break
		}
	}
	if top < 0 {
		t.Fatalf("no menu box drawn:\n%s", strings.Join(lines, "\n"))
	}
	if top < m.mainRect().Min.Y {
		t.Errorf("the menu box starts on row %d, over the title rows:\n%s", top, strings.Join(lines, "\n"))
	}
}

func TestPromptMenuSelectedRowIsReversedAcross(t *testing.T) {
	m := typeKeys(cachedMenuModel(), "/")
	desc := firstLine(m.dock.menu.matches[m.dock.menu.sel].Description)
	for _, line := range strings.Split(m.View().Content, "\n") {
		plain := ansi.Strip(line)
		if !strings.Contains(plain, desc) {
			continue
		}
		open := strings.Index(plain, "│")
		closeAt := strings.LastIndex(plain, "│")
		x0 := ansi.StringWidth(plain[:open]) + 2
		x1 := ansi.StringWidth(plain[:closeAt]) - 1
		for x, rev := range reversedAt(line, x0, x1-x0) {
			if !rev {
				t.Fatalf("cell %d of the selected row is not reversed: %q", x0+x, plain)
			}
		}
		return
	}
	t.Fatal("the selected row is not drawn")
}

func TestPromptMenuCacheDroppedForEndedSessions(t *testing.T) {
	m := cachedMenuModel()
	m.completions["gone"] = map[byte][]api.PromptCompletion{'/': menuItems}
	res, _ := m.Update(sessionsReplacedMsg([]session.Session{m.sessions["s1"]}))
	m = res.(model)
	if _, ok := m.completions["gone"]; ok {
		t.Error("a snapshot without the session kept its cache")
	}
	params, _ := json.Marshal(registry.Event{Type: registry.EventRemoved, Session: session.Session{ID: "s1"}})
	m.applyEvent(api.Notification{Method: api.MethodSessionEvent, Params: params})
	if _, ok := m.completions["s1"]; ok {
		t.Error("a removed session kept its cache")
	}
}
