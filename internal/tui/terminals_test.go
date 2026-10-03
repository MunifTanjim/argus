package tui

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

type terminalsClient struct {
	recordingClient
	list    []api.Terminal
	nodes   []api.NodeInfo
	created api.Terminal
}

func (c *terminalsClient) Call(method string, params, out any) error {
	_ = c.recordingClient.Call(method, params, out)
	switch method {
	case api.MethodTerminalList:
		if p, ok := out.(*api.TerminalListResult); ok {
			*p = api.TerminalListResult{Terminals: c.list}
		}
	case api.MethodServerInfo:
		if p, ok := out.(*api.ServerInfo); ok {
			*p = api.ServerInfo{Nodes: c.nodes}
		}
	case api.MethodTerminalCreate:
		if p, ok := out.(*api.Terminal); ok {
			*p = c.created
		}
	}
	return nil
}

func capable(ids ...string) []api.NodeInfo {
	var out []api.NodeInfo
	for _, id := range ids {
		out = append(out, api.NodeInfo{ID: id, Label: id + "-box", Capabilities: api.NodeCapabilities{SpawnSession: true, Terminal: true}})
	}
	return out
}

var twoTerminals = []api.Terminal{
	{ID: "n1:@1", Command: "zsh", Cwd: "~", NodeID: "n1", NodeLabel: "home"},
	{ID: "n1:@2", Name: "build", Command: "make", Cwd: "~/src", NodeID: "n1", NodeLabel: "home"},
}

func terminalsTab(t *testing.T, list []api.Terminal, nodes []api.NodeInfo) (model, *terminalsClient) {
	t.Helper()
	c := &terminalsClient{list: list, nodes: nodes}
	m := homeTestModel()
	m.client = c
	m, _ = upd(m, terminalsMsg{list: list, nodes: nodes})
	m = typeKeys(m, "gtgt")
	if _, ok := m.baseComp().(terminalsComp); !ok {
		t.Fatalf("gtgt from Sessions: base = %T, want the Terminals tab", m.baseComp())
	}
	return m, c
}

func TestTerminalsKeepAFailedNodesList(t *testing.T) {
	m := homeTestModel()
	prev := []api.Terminal{
		{ID: "n1:@1", NodeID: "n1", NodeLabel: "b"},
		{ID: "n2:@1", NodeID: "n2", NodeLabel: "a"},
	}
	m, _ = upd(m, terminalsMsg{list: prev})
	m, _ = upd(m, terminalsMsg{list: []api.Terminal{{ID: "n1:@2", NodeID: "n1", NodeLabel: "b"}}, failed: []string{"n2"}})
	var got []string
	for _, t := range m.terminals {
		got = append(got, t.ID)
	}
	if want := []string{"n2:@1", "n1:@2"}; !slices.Equal(got, want) {
		t.Fatalf("terminals = %v, want %v", got, want)
	}
}

func TestTerminalsTabListsAndOpens(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Terminals") || !strings.Contains(view, "zsh") || !strings.Contains(view, "build") {
		t.Fatalf("view lacks the tab or the terminals:\n%s", view)
	}
	m = typeKeys(m, "j")
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	s, ok := m.liveScreen()
	if !ok || s.terminalID != "n1:@2" || s.title != "build" {
		t.Fatalf("enter: screen = %+v, want the build terminal", s)
	}
	ps := c.paramsOf(api.MethodTerminalOpen)
	if len(ps) != 1 || ps[0].(api.TerminalOpenParams).TerminalID != "n1:@2" || ps[0].(api.TerminalOpenParams).SessionID != "" {
		t.Fatalf("terminal.open params = %+v", ps)
	}
	m, cmd = upd(m, screenLeaveKey)
	runCmd(cmd)
	if _, ok := m.baseComp().(terminalsComp); !ok {
		t.Errorf("^]: base = %T, want the Terminals tab back", m.baseComp())
	}
}

func TestTerminalsKillAsksFirst(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	m = typeKeys(m, "dd")
	if p := m.focusedComp().(footerPrompter).footerPrompt(&ctx{m: &m}); !strings.Contains(ansi.Strip(p), "kill terminal zsh") {
		t.Fatalf("prompt = %q, want it to name the terminal", p)
	}
	m, cmd := upd(m, keyMsg("y"))
	runCmd(cmd)
	ps := c.paramsOf(api.MethodTerminalKill)
	if len(ps) != 1 || ps[0].(api.TerminalRef).TerminalID != "n1:@1" {
		t.Fatalf("terminal.kill params = %+v", ps)
	}
}

func TestTerminalsFailedActionReloads(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	m, cmd := upd(m, terminalActionMsg{verb: "kill terminal", err: errors.New("unknown terminal: @1")})
	runCmd(cmd)
	if !strings.Contains(m.flash, "unknown terminal") || called(&c.recordingClient, api.MethodTerminalList) != 1 {
		t.Fatalf("flash = %q, calls = %v; want the error and one terminal.list", m.flash, c.calledMethods())
	}
}

func TestTerminalsRename(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	m = typeKeys(m, "r")
	m = typeKeys(m, "logs")
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	ps := c.paramsOf(api.MethodTerminalRename)
	if len(ps) != 1 || ps[0].(api.TerminalRenameParams) != (api.TerminalRenameParams{TerminalID: "n1:@1", Name: "logs"}) {
		t.Fatalf("terminal.rename params = %+v", ps)
	}
}

func TestTerminalsNewOnTheOnlyNodeOpensIt(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	c.created = api.Terminal{ID: "n1:@3", Command: "zsh", NodeID: "n1", NodeLabel: "home"}
	m, cmd := upd(m, keyMsg("a"))
	runCmd(cmd)
	ps := c.paramsOf(api.MethodTerminalCreate)
	if len(ps) != 1 || ps[0].(api.TerminalCreateParams).NodeID != "n1" {
		t.Fatalf("terminal.create params = %+v", ps)
	}
	m, cmd = upd(m, terminalActionMsg{verb: "new terminal", created: &c.created})
	runCmd(cmd)
	if s, ok := m.liveScreen(); !ok || s.terminalID != "n1:@3" {
		t.Fatalf("after create: screen = %+v, want the new terminal open", s)
	}
}

func TestTerminalsNewPicksANode(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1", "n2"))
	m = typeKeys(m, "a")
	if _, ok := m.popups.front().(terminalNodePicker); !ok {
		t.Fatalf("a with two nodes: popup = %T, want the node picker", m.popups.front())
	}
	m = typeKeys(m, "j")
	_, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	ps := c.paramsOf(api.MethodTerminalCreate)
	if len(ps) != 1 || ps[0].(api.TerminalCreateParams).NodeID != "n2" {
		t.Fatalf("terminal.create params = %+v, want n2", ps)
	}
}

func TestTerminalsTabShowsLoadingThenError(t *testing.T) {
	m := homeTestModel()
	m.client = &terminalsClient{}
	m = typeKeys(m, "gtgt")
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "loading terminals") {
		t.Fatalf("view before a load:\n%s", view)
	}
	m, _ = upd(m, terminalsMsg{err: errors.New("boom")})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "error: boom") {
		t.Fatalf("view after a failed load:\n%s", view)
	}
}

func TestTerminalsTabHidesWithoutTmux(t *testing.T) {
	m, _ := terminalsTab(t, twoTerminals, capable("n1"))
	m, _ = upd(m, terminalsMsg{nodes: []api.NodeInfo{{ID: "n1"}}})
	if _, ok := m.baseComp().(homeComp); !ok {
		t.Fatalf("base = %T, want Sessions once no node has tmux", m.baseComp())
	}
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "Terminals") {
		t.Fatalf("view shows a Terminals tab:\n%s", view)
	}
	m = typeKeys(m, "gtgt")
	if _, ok := m.baseComp().(homeComp); !ok {
		t.Fatalf("gtgt: base = %T, want Sessions after History wraps", m.baseComp())
	}
}

func TestTerminalChangedReloads(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	_, cmd := upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalChanged, Params: json.RawMessage(`{}`)}))
	runCmd(cmd)
	if called(&c.recordingClient, api.MethodTerminalList) != 1 {
		t.Fatalf("calls = %v, want one terminal.list", c.calledMethods())
	}
}

func TestTerminalClientPane(t *testing.T) {
	onArgus := "/tmp/tmux-501/argus,1,0"
	local := api.Terminal{NodeID: "box", NodeLabel: "box"}
	for _, tc := range []struct {
		name string
		t    api.Terminal
		env  string
		want string
	}{
		{"same machine, argus server", local, onArgus, "%7"},
		{"plain node", api.Terminal{}, onArgus, "%7"},
		{"default server", local, "/tmp/tmux-501/default,1,0", ""},
		{"other machine", api.Terminal{NodeID: "far", NodeLabel: "far"}, onArgus, ""},
		{"not in tmux", local, "", ""},
	} {
		if got := terminalClientPane(tc.t, "box", tc.env, "%7"); got != tc.want {
			t.Errorf("%s: pane = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTerminalsRefreshAndBack(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	m, cmd := typeKeysCmd(m, "gr")
	runCmd(cmd)
	if called(&c.recordingClient, api.MethodTerminalList) != 1 {
		t.Fatalf("gr: calls = %v, want one terminal.list", c.calledMethods())
	}
	if m, _ = upd(m, keyMsg("esc")); !m.treeFocused() {
		t.Errorf("esc: focus = %v, want the tree", m.focused)
	}
}

// nodePane is a two-node model on n1's node pane.
func nodePane(t *testing.T, nodes []api.NodeInfo) (model, *terminalsClient) {
	t.Helper()
	list := append(append([]api.Terminal{}, twoTerminals...), api.Terminal{ID: "n2:@1", Command: "htop", NodeID: "n2", NodeLabel: "work"})
	c := &terminalsClient{list: list, nodes: nodes}
	m := homeTestModel()
	m.client = c
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{
		ID: "n2:p9", Name: "infra", Kind: "git", NodeID: "n2", NodeLabel: "work",
		Workspaces: []api.WorkspaceNode{{ID: "n2:w9", Dir: "/infra", IsMain: true, Branch: "main"}},
	})
	m.left.tree.rebuild()
	m, _ = upd(m, terminalsMsg{list: list, nodes: nodes})
	m.main = backStack{summaryComp{kind: rowNode, id: "n1"}}
	m = withFocus(m, mainPane)
	return m, c
}

func TestNodePaneTabsSwitch(t *testing.T) {
	m, _ := nodePane(t, capable("n1", "n2"))
	if v := ansi.Strip(m.View().Content); !strings.Contains(v, "Projects") || !strings.Contains(v, "Terminals") {
		t.Fatalf("node pane lacks its tabs:\n%s", v)
	}
	m = typeKeys(m, "gt")
	tc, ok := m.baseComp().(terminalsComp)
	if !ok || tc.nodeID != "n1" {
		t.Fatalf("gt: base = %#v, want n1's terminals", m.baseComp())
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "build") || strings.Contains(v, "htop") {
		t.Fatalf("node terminals show another node's terminal:\n%s", v)
	}
	if m.mainRow() != "n1" {
		t.Errorf("mainRow = %q, want n1", m.mainRow())
	}
	m = typeKeys(m, "gt")
	if s, ok := m.baseComp().(summaryComp); !ok || s.id != "n1" {
		t.Fatalf("gt again: base = %#v, want the Projects tab", m.baseComp())
	}
}

func TestNodePaneWithoutTmuxHasNoTerminalsTab(t *testing.T) {
	nodes := capable("n2")
	nodes = append(nodes, api.NodeInfo{ID: "n1", Label: "home"})
	m, _ := nodePane(t, nodes)
	if v := ansi.Strip(m.View().Content); strings.Contains(v, "Terminals") {
		t.Fatalf("node pane of a node without tmux shows Terminals:\n%s", v)
	}
	m = typeKeys(m, "gt")
	if _, ok := m.baseComp().(summaryComp); !ok {
		t.Fatalf("gt: base = %T, want the summary to stay", m.baseComp())
	}
}

func TestNodeTerminalsNewUsesTheNode(t *testing.T) {
	m, c := nodePane(t, capable("n1", "n2"))
	m = typeKeys(m, "gt")
	_, cmd := upd(m, keyMsg("a"))
	runCmd(cmd)
	ps := c.paramsOf(api.MethodTerminalCreate)
	if len(ps) != 1 || ps[0].(api.TerminalCreateParams).NodeID != "n1" {
		t.Fatalf("terminal.create params = %+v, want n1 without a picker", ps)
	}
}

func TestTerminalOpenFailureReloadsTheList(t *testing.T) {
	m, c := terminalsTab(t, twoTerminals, capable("n1"))
	m, _ = upd(m, keyMsg("enter"))
	s, ok := m.liveScreen()
	if !ok {
		t.Fatal("enter: no live screen")
	}
	_, cmd := upd(m, termOpenedMsg{termID: s.termID, err: errors.New("unknown terminal: @1")})
	runCmd(cmd)
	if called(&c.recordingClient, api.MethodTerminalList) != 1 {
		t.Fatalf("calls = %v, want one terminal.list after the failed open", c.calledMethods())
	}
}

func TestTerminalsNameTheNodeWhenSeveralCanRunThem(t *testing.T) {
	list := []api.Terminal{{ID: "n1:@1", Command: "zsh", Cwd: "~", NodeID: "n1", NodeLabel: "alpha"}}
	m, _ := terminalsTab(t, list, capable("n1", "n2"))
	if v := ansi.Strip(m.View().Content); !strings.Contains(v, "alpha") {
		t.Fatalf("view does not name the node of the only terminal:\n%s", v)
	}
}
