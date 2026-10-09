package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// workspacePane is the workspace pane of n1:w1 (sessions n1:s1 and n1:s3),
// reached from Home through the tree.
func workspacePane(m model) model {
	return pressKeys(typeKeys(pressKeys(m, cw('h')...), "jjj"), keyMsg("enter"))
}

func TestWorkspacePaneStartsOverAfterATripToHome(t *testing.T) {
	m := typeKeys(workspacePane(homeTestModel()), "j")
	m = typeKeys(pressKeys(m, keyMsg("esc")), "kkk")
	if p, ok := m.main[0].(workspaceComp); !ok || p.ws != "n1:w1" || p.cursor != 1 {
		t.Fatalf("cursor moves must keep the pane of n1:w1 on card 1: root=%#v", m.main[0])
	}
	m = pressKeys(m, keyMsg("enter"))
	if viewOf(m) != viewHome {
		t.Fatalf("<CR> on the Home row: view = %v, want Home", viewOf(m))
	}
	m = pressKeys(typeKeys(pressKeys(m, keyMsg("esc")), "jjj"), keyMsg("enter"))
	if p := paneOf(m); m.focused != mainPane || p.ws != "n1:w1" || p.cursor != 0 {
		t.Errorf("back on n1:w1: focus=%v ws=%q cursor=%d, want the pane on card 0", m.focused, p.ws, p.cursor)
	}
}

func TestTreeAndBackKeepsTheSession(t *testing.T) {
	m := typeKeys(workspacePane(homeTestModel()), "j")
	m = pressKeys(m, keyMsg("enter"))
	if viewOf(m) != viewSession || m.liveSessionID() != "n1:s3" {
		t.Fatalf("enter should open n1:s3: view=%v id=%q", viewOf(m), m.liveSessionID())
	}
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("enter"))
	if trOf(m).sessionID != "n1:s3" || m.focused != mainPane {
		t.Errorf("the tree and back: session=%q focus=%v, want n1:s3 with focus", trOf(m).sessionID, m.focused)
	}
}

func TestWorkspaceKillConfirmation(t *testing.T) {
	rc := &recordingClient{}
	m := killableHome()
	m.client = rc
	m = typeKeys(workspacePane(m), "jdd")
	if paneOf(m).killID != "n1:s3" || homeOf(m).killID != "" {
		t.Fatalf("dd should ask to kill n1:s3 in the pane only: pane=%q home=%q", paneOf(m).killID, homeOf(m).killID)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "kill session repo · %1? y/n") {
		t.Error("the footer should ask to kill")
	}
	if n := typeKeys(m, "n"); paneOf(n).killID != "" {
		t.Error("any key but y should cancel")
	}
	m, cmd := typeKeysCmd(m, "y")
	if paneOf(m).killID != "" || cmd == nil {
		t.Fatal("y should clear the prompt and kill")
	}
	cmd()
	if p := rc.params[len(rc.params)-1].(api.SessionRef); p.SessionID != "n1:s3" {
		t.Errorf("kill sent for %q, want n1:s3", p.SessionID)
	}
}

func TestWorkspaceKillDisarmsWhenTheTreeMovesToHome(t *testing.T) {
	m := typeKeys(workspacePane(killableHome()), "jdd")
	if paneOf(m).killID != "n1:s3" {
		t.Fatalf("dd should arm the pane prompt: %q", paneOf(m).killID)
	}
	m = treeEmptied(m)
	if _, ok := m.main[0].(homeComp); !ok || paneOf(m).killID != "" || m.keysRaw() {
		t.Fatalf("n1:w1 left the list: root=%T kill=%q raw=%v, want Home and the prompt dropped", m.main[0], paneOf(m).killID, m.keysRaw())
	}
	if _, cmd := m.runKey(keyMsg("y")); cmd != nil {
		t.Error("y after the pane left must not kill")
	}
}

func TestKillPromptDropsWhenItsSessionLeaves(t *testing.T) {
	removed := func(id string) tea.Msg {
		params, _ := json.Marshal(registry.Event{Type: registry.EventRemoved, Session: session.Session{ID: id}})
		return notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params})
	}
	m := typeKeys(workspacePane(killableHome()), "jdd")
	m, _ = upd(m, removed("n1:s3"))
	if paneOf(m).killID != "" || m.keysRaw() || strings.Contains(viewText(m), "session ?") {
		t.Errorf("n1:s3 left: pane kill=%q raw=%v, want the prompt dropped", paneOf(m).killID, m.keysRaw())
	}
	m = typeKeys(killableHome(), "jdd")
	m, _ = upd(m, removed("n1:s2"))
	if homeOf(m).killID != "" || m.keysRaw() {
		t.Errorf("n1:s2 left: Home kill=%q raw=%v, want the prompt dropped", homeOf(m).killID, m.keysRaw())
	}
}

func TestFailedProjectLoadKeepsTheLastTree(t *testing.T) {
	m := pressKeys(wideWorkspace(), keyMsg("enter"))
	data := m.left.tree.data
	m, _ = upd(m, projectsTreeMsg{err: errors.New("down")})
	out := viewText(m)
	for _, want := range []string{"error: down", "repo-feat", "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("view after a failed load lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "workspace not found") {
		t.Errorf("the workspace pane should keep n1:w1 after a failed load:\n%s", out)
	}
	if id := m.left.tree.cursorRowID(); id != "n1:w1" {
		t.Errorf("cursor on %q after a failed load, want n1:w1", id)
	}
	m, _ = upd(m, projectsTreeMsg{tree: data})
	if id := m.left.tree.cursorRowID(); id != "n1:w1" || m.left.tree.err != nil {
		t.Errorf("after recovery: cursor %q err %v, want n1:w1 and no error", id, m.left.tree.err)
	}
}

func TestWorkspaceKillDisarmsWhenItsWorkspaceLeaves(t *testing.T) {
	m := typeKeys(workspacePane(killableHome()), "jdd")
	if paneOf(m).killID != "n1:s3" {
		t.Fatalf("dd should arm the pane prompt: %q", paneOf(m).killID)
	}
	p := m.left.tree.data[0]
	p.Workspaces = p.Workspaces[1:]
	m, _ = upd(m, projectsTreeMsg{tree: []api.ProjectNode{p}})
	if viewOf(m) != viewHome || m.keysRaw() {
		t.Fatalf("n1:w1 gone: view=%v raw=%v, want Home and the prompt dropped", viewOf(m), m.keysRaw())
	}
	if _, cmd := m.runKey(keyMsg("y")); cmd != nil {
		t.Error("y after the pane left must not kill")
	}
}

var workspaceTerminals = []api.Terminal{
	{ID: "n1:@1", Command: "zsh", Cwd: "~", NodeID: "n1", NodeLabel: "home"},
	{ID: "n1:@2", Name: "build", Command: "make", Cwd: "~/repo", NodeID: "n1", NodeLabel: "home", WorkspaceID: "n1:w1"},
	{ID: "n1:@3", Name: "logs", Command: "tail", Cwd: "~/repo", NodeID: "n1", NodeLabel: "home", WorkspaceID: "n1:w1"},
	{ID: "n1:@4", Name: "other", Command: "vim", Cwd: "~/repo-feat", NodeID: "n1", NodeLabel: "home", WorkspaceID: "n1:w2"},
}

func workspaceTerminalsPane(t *testing.T, nodes []api.NodeInfo) (model, *terminalsClient) {
	t.Helper()
	c := &terminalsClient{list: workspaceTerminals, nodes: nodes}
	m := homeTestModel()
	m.client = c
	m, _ = upd(m, terminalsMsg{list: workspaceTerminals, nodes: nodes})
	return workspacePane(m), c
}

func wsTerminalsTab(t *testing.T, m model) terminalsComp {
	t.Helper()
	tc, ok := m.baseComp().(terminalsComp)
	if !ok || tc.ws != "n1:w1" {
		t.Fatalf("base = %#v, want the Terminals tab of n1:w1", m.baseComp())
	}
	return tc
}

func TestWorkspaceTabsSwitch(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, capable("n1"))
	if v := viewText(m); !strings.Contains(v, "Sessions   Terminals") {
		t.Fatalf("the workspace pane lacks its tabs:\n%s", v)
	}
	m = typeKeys(m, "gt")
	wsTerminalsTab(t, m)
	v := viewText(m)
	if !strings.Contains(v, "repo  main") || !strings.Contains(v, "build") || !strings.Contains(v, "logs") ||
		strings.Contains(v, "zsh") || strings.Contains(v, "other") {
		t.Fatalf("the Terminals tab should keep the header and list only n1:w1's terminals:\n%s", v)
	}
	if m.mainRow() != "n1:w1" {
		t.Errorf("mainRow = %q, want n1:w1", m.mainRow())
	}
	m = typeKeys(m, "gT")
	if p, ok := m.baseComp().(workspaceComp); !ok || p.ws != "n1:w1" {
		t.Fatalf("gT: base = %#v, want the Sessions tab", m.baseComp())
	}

	m = withMouse(m)
	y, x := findBlock(t, m, "Terminals")
	m, _ = click(m, x, y)
	wsTerminalsTab(t, m)
	y, x = findBlock(t, m, "Sessions")
	m, _ = click(m, x, y)
	if _, ok := m.baseComp().(workspaceComp); !ok {
		t.Fatalf("a click on Sessions: base = %T, want the Sessions tab", m.baseComp())
	}
}

func TestWorkspaceTerminalsTabOpensKillsAndRenames(t *testing.T) {
	m, c := workspaceTerminalsPane(t, capable("n1"))
	m = typeKeys(m, "gtj")
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	s, ok := m.liveScreen()
	if !ok || s.terminalID != "n1:@3" || s.row != "n1:w1" {
		t.Fatalf("enter: screen = %+v, want n1:@3 on the n1:w1 row", s)
	}
	m, cmd = upd(m, screenLeaveKey)
	runCmd(cmd)
	wsTerminalsTab(t, m)

	m = typeKeys(m, "dd")
	m, cmd = upd(m, keyMsg("y"))
	runCmd(cmd)
	if ps := c.paramsOf(api.MethodTerminalKill); len(ps) != 1 || ps[0].(api.TerminalRef).TerminalID != "n1:@3" {
		t.Fatalf("terminal.kill params = %+v", ps)
	}
	m = typeKeys(m, "r")
	m = typeKeys(m, "2")
	m, cmd = upd(m, keyMsg("enter"))
	runCmd(cmd)
	ps := c.paramsOf(api.MethodTerminalRename)
	if len(ps) != 1 || ps[0].(api.TerminalRenameParams) != (api.TerminalRenameParams{TerminalID: "n1:@3", Name: "logs2"}) {
		t.Fatalf("terminal.rename params = %+v", ps)
	}
}

func TestWorkspaceTerminalsTabSpawnsInTheWorkspace(t *testing.T) {
	m, c := workspaceTerminalsPane(t, capable("n1"))
	c.created = api.Terminal{ID: "n1:@7", Command: "zsh", Cwd: "~/repo", NodeID: "n1", WorkspaceID: "n1:w1"}
	m = typeKeys(m, "gt")
	m, cmd := typeKeysCmd(m, "a")
	if cmd == nil {
		t.Fatal("a should create a terminal")
	}
	m, cmd = upd(m, cmd())
	ps := c.paramsOf(api.MethodTerminalCreate)
	if len(ps) != 1 || ps[0].(api.TerminalCreateParams) != (api.TerminalCreateParams{NodeID: "n1", WorkspaceID: "w1"}) {
		t.Fatalf("terminal.create params = %+v, want node n1 and workspace w1", ps)
	}
	runCmd(cmd)
	if s, ok := m.liveScreen(); !ok || s.terminalID != "n1:@7" || s.row != "n1:w1" {
		t.Fatalf("the new terminal should open on the n1:w1 row: screen = %+v", s)
	}
}

func TestWorkspaceTerminalsCursorSurvivesSessionUpdates(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, capable("n1"))
	m = typeKeys(m, "gtj")
	params, _ := json.Marshal(registry.Event{Type: registry.EventUpdated, Session: m.sessions["n1:s1"]})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params}))
	if tc := wsTerminalsTab(t, m); tc.cursor != 1 {
		t.Errorf("a session update moved the terminal cursor to %d, want 1", tc.cursor)
	}
}

func TestWorkspaceWithoutTmuxHasNoTerminalsTab(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, []api.NodeInfo{{ID: "n1", Label: "home"}})
	if v := viewText(m); strings.Contains(v, "Terminals") || !strings.Contains(v, "Sessions") {
		t.Fatalf("the pane of a node without tmux should show only the Sessions tab:\n%s", v)
	}
	m = typeKeys(m, "gt")
	if _, ok := m.baseComp().(workspaceComp); !ok {
		t.Fatalf("gt: base = %T, want the Sessions tab to stay", m.baseComp())
	}
	if f := ansi.Strip(m.currentFooter()); strings.Contains(f, "tabs") {
		t.Errorf("footer = %q, want no tab hint", f)
	}
}

func TestWorkspaceTerminalsTabLeavesWithItsWorkspace(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, capable("n1"))
	m = typeKeys(m, "gt")
	wsTerminalsTab(t, m)
	p := m.left.tree.data[0]
	p.Workspaces = p.Workspaces[1:]
	m, _ = upd(m, projectsTreeMsg{tree: []api.ProjectNode{p}})
	if viewOf(m) != viewHome {
		t.Fatalf("n1:w1 gone: view = %v, want Home", viewOf(m))
	}
}

func topBar(m model) string { return frameLines(m)[0] }

// barHeader is the column of the top bar's dot and of the pane header after
// it, or -1 without a dot.
func barHeader(m model) (dot, header int) {
	bar := topBar(m)
	i := strings.Index(bar, "·")
	if i < 0 {
		return -1, -1
	}
	rest := bar[i+len("·"):]
	return ansi.StringWidth(bar[:i]), ansi.StringWidth(bar[:i+len("·")]) + len(rest) - len(strings.TrimLeft(rest, " "))
}

// paneCell is the column of s on the pane's first row.
func paneCell(t *testing.T, m model, s string) int {
	t.Helper()
	col := columnOf(frameLines(m)[2], s)
	if col < 0 {
		t.Fatalf("%q is not on the pane's first row %q", s, frameLines(m)[2])
	}
	return col
}

// paneStart is the column of the first character right of the tree divider
// on the pane's first row.
func paneStart(t *testing.T, m model) int {
	t.Helper()
	_, after, _ := strings.Cut(frameLines(m)[2], "│")
	return paneCell(t, m, "│") + 1 + len(after) - len(strings.TrimLeft(after, " "))
}

func TestTopBarAlignsTheOpenWorkspace(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, capable("n1"))
	_, divider := findBlock(t, m, "│")
	_, tabs := findBlock(t, m, "Sessions   Terminals")
	if dot, header := barHeader(m); dot != divider || header != tabs || !strings.Contains(topBar(m), "repo  main") {
		t.Fatalf("Sessions tab: dot at %d, header at %d; want %d over the divider and %d over the tabs:\n%s",
			dot, header, divider, tabs, topBar(m))
	}
	if v := viewText(m); strings.Count(v, "repo  main") != 2 {
		t.Errorf("the pane should not repeat the header; only the top bar and the tree name it:\n%s", v)
	}
	m = typeKeys(m, "gt")
	if dot, header := barHeader(m); dot != divider || header != tabs {
		t.Errorf("Terminals tab: dot at %d, header at %d; want %d and %d", dot, header, divider, tabs)
	}
	m = typeKeys(m, "gT")
	m = withFile(m, fileComp{ws: "n1:w1", path: "a.go", lines: []string{"x"}})
	if dot, header := barHeader(m); dot != divider || header != tabs {
		t.Errorf("file over the pane: dot at %d, header at %d; want %d and %d", dot, header, divider, tabs)
	}

	if strings.Contains(topBar(homeTestModel()), "repo  main") {
		t.Errorf("Home: top bar = %q, want no workspace", topBar(homeTestModel()))
	}
}

func TestWorkspaceTabLineCarriesTheFilter(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, capable("n1"))
	m = typeKeys(m, "za")
	if v := viewText(m); !strings.Contains(v, "Sessions   Terminals  active") {
		t.Fatalf("active-only should mark the tab line:\n%s", v)
	}
}

func TestTopBarWorkspaceFollowsTheBrandWithoutRoom(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, capable("n1"))
	got := ansi.Strip(m.titleWithPane("Argus", "repo  main", 60, m.width-40))
	if got != "Argus · repo  main" {
		t.Errorf("with a wide right side: title = %q, want the header after the brand", got)
	}
}

func TestTopBarAlignsTheLiveTranscript(t *testing.T) {
	m := pressKeys(workspacePane(homeTestModel()), keyMsg("enter"))
	if viewOf(m) != viewSession {
		t.Fatalf("enter should open a session: view = %v", viewOf(m))
	}
	m = withEntries(m, sampleEntries())
	divider, marker := paneCell(t, m, "│"), paneCell(t, m, "▌")
	if dot, header := barHeader(m); dot != divider || header != marker+contentPadX || !strings.Contains(topBar(m), "repo  [") {
		t.Fatalf("dot at %d, header at %d; want %d over the divider and %d over the transcript: %q",
			dot, header, divider, marker+contentPadX, topBar(m))
	}
	if strings.Contains(topBar(m), "repo  main") {
		t.Errorf("a session should replace the workspace in the top bar: %q", topBar(m))
	}

	m = withFile(m, fileComp{ws: "n1:w1", path: "a.go", lines: []string{"x"}})
	if !strings.Contains(topBar(m), "repo  [") {
		t.Errorf("file over the transcript: top bar = %q, want the session", topBar(m))
	}
	if v := strings.Split(viewText(m), "\n"); len(v) > 2 && strings.Contains(v[2], "repo") {
		t.Errorf("the file should have no header over it: %q", v[2])
	}
}

func TestTopBarHasNoPaneHeaderUnderTheSpawnFlow(t *testing.T) {
	m := homeTestModel()
	m.client = &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m, cmd := upd(m, keyMsg("s"))
	m = workspacePane(m)
	m = runSpawnReplies(m, cmd)
	if !spawnOpen(m) {
		t.Fatal("the reply should open the spawn flow")
	}
	if b := topBar(m); strings.Contains(b, "repo") {
		t.Errorf("top bar under the spawn flow = %q, want no pane header", b)
	}
}

func TestTopBarNamesTheNodePane(t *testing.T) {
	m, _ := nodePane(t, capable("n1", "n2"))
	_, divider := findBlock(t, m, "│")
	tabs := paneCell(t, m, "Projects   Terminals")
	if dot, header := barHeader(m); dot != divider || header != tabs || !strings.Contains(topBar(m), "home") {
		t.Fatalf("Projects tab: dot at %d, header at %d; want %d and %d: %q", dot, header, divider, tabs, topBar(m))
	}
	m = typeKeys(m, "gt")
	if dot, header := barHeader(m); dot != divider || header != tabs || !strings.Contains(topBar(m), "home") {
		t.Errorf("Terminals tab: dot at %d, header at %d; want %d and %d: %q", dot, header, divider, tabs, topBar(m))
	}
	if col := paneCell(t, m, "Projects   Terminals"); col != tabs {
		t.Errorf("the tabs moved from %d to %d between the node tabs", tabs, col)
	}
}

func TestTopBarNamesTheProjectPane(t *testing.T) {
	m := selectRow(wideWorkspace(), "n1:p1")
	_, divider := findBlock(t, m, "│")
	rows := paneStart(t, m)
	if dot, header := barHeader(m); dot != divider || header != rows || !strings.Contains(topBar(m), "argus  /repo/.git") {
		t.Fatalf("dot at %d, header at %d; want %d and %d over the workspace rows: %q", dot, header, divider, rows, topBar(m))
	}
}

func TestTopBarNamesTheLiveScreen(t *testing.T) {
	m := liveScreenModel()
	_, divider := findBlock(t, m, "│")
	box := paneCell(t, m, "╭")
	if dot, header := barHeader(m); dot != divider || header != box || !strings.Contains(topBar(m), "fix-login") {
		t.Fatalf("dot at %d, header at %d; want %d and %d over the box: %q", dot, header, divider, box, topBar(m))
	}
}

func TestWorkspaceTerminalsTabLeavesWhenTheNodeLosesTmux(t *testing.T) {
	m, _ := workspaceTerminalsPane(t, capable("n1"))
	m = typeKeys(m, "gt")
	wsTerminalsTab(t, m)
	m, _ = upd(m, terminalsMsg{list: workspaceTerminals, nodes: []api.NodeInfo{{ID: "n1", Label: "home"}}})
	if p, ok := m.baseComp().(workspaceComp); !ok || p.ws != "n1:w1" {
		t.Fatalf("n1 lost tmux: base = %#v, want the Sessions tab", m.baseComp())
	}
}

func TestTopBarClipsALongHeaderAndKeepsTheBrand(t *testing.T) {
	m := pressKeys(workspacePane(homeTestModel()), keyMsg("enter"))
	s := m.sessions["n1:s1"]
	s.Name = strings.Repeat("refactor-auth-", 6)
	s.Branch = "feature/long-branch-name-for-auth"
	m.sessions["n1:s1"] = s
	for _, w := range []int{80, 60} {
		m.width = w
		bar := topBar(m)
		if !strings.Contains(bar, frameRow) || ansi.StringWidth(bar) > w || strings.Contains(bar, "long-branch") {
			t.Errorf("width %d: top bar = %q, want the brand kept and the header clipped to fit", w, bar)
		}
	}
}
