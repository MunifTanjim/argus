package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// spawnPickClient serves a canned server.info and records the spawn it receives.
type spawnPickClient struct {
	nodes       []api.NodeInfo
	projects    []session.HistoryProject
	agents      []api.AgentInfo     // agents.list reply (no spawnable agent → step skipped)
	homes       map[string]string   // host.completeDir home by node_id
	subdirs     map[string][]string // host.completeDir names by path
	completed   []string            // host.completeDir node_ids, in call order
	spawnCalled bool
	spawnNodeID string
	spawnCwd    string
	spawnAgent  string
	spawnPrompt string
}

func (c *spawnPickClient) Call(method string, params, out any) error {
	switch method {
	case api.MethodServerInfo:
		if p, ok := out.(*api.ServerInfo); ok {
			*p = api.ServerInfo{Nodes: c.nodes}
		}
	case api.MethodSessionsHistoryProjects:
		if p, ok := out.(*[]session.HistoryProject); ok {
			*p = c.projects
		}
	case api.MethodAgentsList:
		if p, ok := out.(*api.AgentsListResult); ok {
			*p = api.AgentsListResult{Agents: c.agents}
		}
	case api.MethodHostCompleteDir:
		p := params.(api.HostCompleteDirParams)
		c.completed = append(c.completed, p.NodeID)
		home, path := c.homes[p.NodeID], p.Path
		if path == "" {
			path = home + "/"
		}
		res := api.HostCompleteDirResult{Home: home}
		for _, n := range c.subdirs[path] {
			res.Paths = append(res.Paths, path[:strings.LastIndexByte(path, '/')+1]+n+"/")
		}
		*out.(*api.HostCompleteDirResult) = res
	case api.MethodSessionSpawn:
		c.spawnCalled = true
		if sp, ok := params.(api.SpawnParams); ok {
			c.spawnNodeID = sp.NodeID
			c.spawnCwd = sp.Cwd
			c.spawnAgent = sp.Agent
			c.spawnPrompt = sp.Prompt
		}
	}
	return nil
}

func (c *spawnPickClient) Events() <-chan api.Notification { return make(chan api.Notification) }
func (c *spawnPickClient) States() <-chan bool             { return make(chan bool) }
func (c *spawnPickClient) Reconnect()                      {}
func (c *spawnPickClient) Close() error                    { return nil }
func (c *spawnPickClient) Quarantined() bool               { return false }

func pumpAgents(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		return m
	}
	mm, _ := m.Update(cmd())
	return mm.(model)
}

func openSpawn(t *testing.T, c *spawnPickClient) model {
	t.Helper()
	m := newModel(c, false, nil)
	cmd := m.newSessionCmd()
	if cmd == nil {
		t.Fatal("newSessionCmd returned no command")
	}
	mm, agentsCmd := m.Update(cmd())
	return runSpawnReplies(mm.(model), agentsCmd)
}

// Multi-node: node step first; choosing a node filters the dir list; the spawn
// carries the chosen node, the picked project's cwd, and the typed prompt.
func TestSpawnMultiNodeRoutesAndPicksDir(t *testing.T) {
	c := &spawnPickClient{
		nodes: []api.NodeInfo{
			{ID: "alpha", Capabilities: api.NodeCapabilities{SpawnSession: true}},
			{ID: "beta", Capabilities: api.NodeCapabilities{SpawnSession: true}},
		},
		projects: []session.HistoryProject{
			{Label: "b1", Cwd: "/beta/1", NodeID: "beta"},
			{Label: "a1", Cwd: "/alpha/1", NodeID: "alpha"},
		},
	}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepNode {
		t.Fatalf("step=%v want node", spawnOf(m).step)
	}
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown}) // cursor → beta
	m = mm.(model)
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // choose beta → agent probe
	m = pumpAgents(t, mm.(model), cmd)                          // 0 agents → dir
	if spawnOf(m).step != spawnStepDir || len(spawnOf(m).dirs) != 1 || spawnOf(m).dirs[0].Cwd != "/beta/1" {
		t.Fatalf("dir step not filtered to beta: %+v", spawnOf(m).dirs)
	}
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // pick dir → prompt
	m = mm.(model)
	if spawnOf(m).step != spawnStepPrompt {
		t.Fatalf("step=%v want prompt", spawnOf(m).step)
	}
	// Empty prompt must NOT spawn (mandatory).
	mm, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(model)
	if cmd != nil || c.spawnCalled {
		t.Fatal("empty prompt should not spawn")
	}
	// Type a prompt, then Enter spawns.
	for _, r := range "fix it" {
		mm, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(model)
	}
	_, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("non-empty prompt should spawn")
	}
	runCmd(cmd)
	if !c.spawnCalled || c.spawnNodeID != "beta" || c.spawnCwd != "/beta/1" || c.spawnPrompt != "fix it" {
		t.Fatalf("spawn node=%q cwd=%q prompt=%q", c.spawnNodeID, c.spawnCwd, c.spawnPrompt)
	}
}

func TestSpawnPromptShiftEnterInsertsNewline(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)
	for _, r := range "line1" {
		mm, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(model)
	}
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}) // newline
	m = mm.(model)
	for _, r := range "line2" {
		mm, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(model)
	}
	if spawnOf(m).prompt.Value() != "line1\nline2" {
		t.Fatalf("prompt = %q, want \"line1\\nline2\"", spawnOf(m).prompt.Value())
	}
	if spawnOf(m).step != spawnStepPrompt {
		t.Fatal("shift+enter must not submit")
	}
}

// ctrl+j is the universally-transmitted newline fallback (shift+enter needs the
// Kitty keyboard protocol, which not every terminal supports).
func TestSpawnPromptCtrlJInsertsNewline(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = mm.(model)
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}) // newline
	m = mm.(model)
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m = mm.(model)
	if spawnOf(m).prompt.Value() != "a\nb" {
		t.Fatalf("prompt = %q, want \"a\\nb\"", spawnOf(m).prompt.Value())
	}
	if spawnOf(m).step != spawnStepPrompt {
		t.Fatal("ctrl+j must not submit")
	}
}

// A prompt taller than the terminal must be windowed, not overflow the screen.
func TestSpawnViewPromptFitsHeight(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	m.width, m.height = 80, 24
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)
	for i := 0; i < 60; i++ { // far more lines than the 24-row screen
		mm, _ = m.handleKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
		m = mm.(model)
	}
	if n := strings.Count(spawnView(m), "\n") + 1; n > m.height {
		t.Fatalf("spawn view rendered %d lines, exceeds height %d", n, m.height)
	}
}

func TestSpawnCustomPathThenPrompt(t *testing.T) {
	c := &spawnPickClient{}
	m := openSpawn(t, c) // empty history → custom dir entry
	for _, r := range "/tmp/x" {
		mm, _ := m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(model)
	}
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // custom → prompt
	m = mm.(model)
	if spawnOf(m).step != spawnStepPrompt {
		t.Fatalf("step=%v want prompt", spawnOf(m).step)
	}
	for _, r := range "go" {
		mm, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(model)
	}
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	runCmd(cmd)
	if !c.spawnCalled || !strings.HasSuffix(c.spawnCwd, "/tmp/x") || c.spawnPrompt != "go" {
		t.Fatalf("custom spawn cwd=%q prompt=%q", c.spawnCwd, c.spawnPrompt)
	}
}

// Single node: node step skipped, straight to dir, most-recent pre-selected.
func TestSpawnSingleNodeStartsAtDir(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{ID: "only", Capabilities: api.NodeCapabilities{SpawnSession: true}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1", NodeID: "only"}},
	}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepDir {
		t.Fatalf("single node should start at dir, got %v", spawnOf(m).step)
	}
	if len(spawnOf(m).dirs) != 1 || spawnOf(m).cursor != 0 {
		t.Fatalf("most-recent not pre-selected: dirs=%+v cursor=%d", spawnOf(m).dirs, spawnOf(m).cursor)
	}
}

// Two+ launchable agents surface the agent step; the chosen agent rides the spawn.
func TestSpawnAgentStepSelects(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{ID: "only", Capabilities: api.NodeCapabilities{SpawnSession: true}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1", NodeID: "only"}},
		agents: []api.AgentInfo{
			{ID: "claude", Name: "Claude", Color: "#fe8019", Spawnable: true},
			{ID: "codex", Name: "Codex", Color: "#b8bb26", Spawnable: true},
		},
	}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepAgent || len(spawnOf(m).agents) != 2 {
		t.Fatalf("expected agent step with 2 agents, got step=%v agents=%+v", spawnOf(m).step, spawnOf(m).agents)
	}
	m.width, m.height = 80, 24
	if !strings.Contains(spawnView(m), "Which agent?") {
		t.Fatalf("agent view missing prompt:\n%s", spawnView(m))
	}
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown}) // cursor → codex
	m = mm.(model)
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // choose codex → dir
	m = mm.(model)
	if spawnOf(m).step != spawnStepDir || spawnOf(m).agent != "codex" {
		t.Fatalf("agent not chosen: step=%v agent=%q", spawnOf(m).step, spawnOf(m).agent)
	}
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // pick dir → prompt
	m = mm.(model)
	for _, r := range "go" {
		mm, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(model)
	}
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	runCmd(cmd)
	if !c.spawnCalled || c.spawnAgent != "codex" {
		t.Fatalf("spawn agent=%q, want codex", c.spawnAgent)
	}
}

// A single launchable agent is auto-selected without showing the picker.
func TestSpawnSingleAgentSkipsStep(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{ID: "only", Capabilities: api.NodeCapabilities{SpawnSession: true}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1", NodeID: "only"}},
		agents: []api.AgentInfo{
			{ID: "codex", Name: "Codex", Color: "#b8bb26", Spawnable: true},
		},
	}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepDir || spawnOf(m).agent != "codex" {
		t.Fatalf("single agent should auto-select and skip to dir: step=%v agent=%q", spawnOf(m).step, spawnOf(m).agent)
	}
}

// A lone node without tmux must not auto-advance: the node step shows it disabled
// (marked "no tmux"), and pressing enter on it does nothing.
func TestSpawnSingleNodeNoTmuxStaysDisabled(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{ID: "only", Label: "only", Capabilities: api.NodeCapabilities{SpawnSession: false}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1", NodeID: "only"}},
	}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepNode {
		t.Fatalf("non-tmux node should stay on node step, got %v", spawnOf(m).step)
	}
	m.width, m.height = 80, 24
	if !strings.Contains(spawnView(m), "no tmux") {
		t.Fatalf("node view should mark the node as having no tmux:\n%s", spawnView(m))
	}
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // enter on disabled node
	m = mm.(model)
	if cmd != nil || spawnOf(m).step != spawnStepNode {
		t.Fatalf("enter on a disabled node must be a no-op; step=%v", spawnOf(m).step)
	}
}

// A local node (self-entry with empty ID) without tmux stays gated on the node
// step rather than auto-advancing.
func TestSpawnLocalNodeNoTmuxGated(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{Label: "box", Capabilities: api.NodeCapabilities{SpawnSession: false}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1"}},
	}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepNode {
		t.Fatalf("non-tmux local node should stay on node step, got %v", spawnOf(m).step)
	}
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(model)
	if cmd != nil || spawnOf(m).step != spawnStepNode {
		t.Fatalf("enter on disabled local node must be a no-op; step=%v", spawnOf(m).step)
	}
}

func TestSpawnLocalNodeWithTmuxSkipsToDir(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{Label: "box", Capabilities: api.NodeCapabilities{SpawnSession: true}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1"}},
	}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepDir {
		t.Fatalf("capable local node should skip to dir, got %v", spawnOf(m).step)
	}
	if spawnOf(m).nodeID != "" {
		t.Fatalf("local node_id must stay empty, got %q", spawnOf(m).nodeID)
	}
	if len(spawnOf(m).dirs) != 1 { // project (empty NodeID) not filtered away
		t.Fatalf("projects should be unfiltered for the local node: %+v", spawnOf(m).dirs)
	}
}

// Empty history drops into free-text path entry seeded with the node's home.
func TestSpawnEmptyHistoryGoesToCustom(t *testing.T) {
	c := &spawnPickClient{homes: map[string]string{"": "/home/u"}}
	m := openSpawn(t, c)
	if spawnOf(m).step != spawnStepDir || !spawnOf(m).custom {
		t.Fatalf("empty history → custom; step=%v custom=%v", spawnOf(m).step, spawnOf(m).custom)
	}
	if got := spawnOf(m).cwd.Value(); got != "/home/u/" {
		t.Fatalf("custom cwd = %q, want the node's home /home/u/", got)
	}
}

// The custom path row on a remote node seeds that node's home, not this one's.
func TestSpawnCustomPathSeedsChosenNodeHome(t *testing.T) {
	c := &spawnPickClient{
		nodes: []api.NodeInfo{
			{ID: "local", Capabilities: api.NodeCapabilities{SpawnSession: true}},
			{ID: "remote", Capabilities: api.NodeCapabilities{SpawnSession: true}},
		},
		projects: []session.HistoryProject{{NodeID: "remote", Label: "p", Cwd: "/srv/p"}},
		homes:    map[string]string{"local": "/Users/me", "remote": "/home/remote"},
	}
	m := openSpawn(t, c)
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"}) // the remote node
	m = runSpawnReplies(mm.(model), cmd)
	mm, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = runSpawnReplies(mm.(model), cmd)
	mm, cmd = m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"}) // the "Custom path…" row
	m = runSpawnReplies(mm.(model), cmd)
	mm, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = runSpawnReplies(mm.(model), cmd)
	if got := spawnOf(m).cwd.Value(); got != "/home/remote/" {
		t.Fatalf("custom cwd = %q, want the remote node's home", got)
	}
	for _, id := range c.completed {
		if id != "remote" {
			t.Fatalf("completions asked of %v, want only the remote node", c.completed)
		}
	}
}

// Esc cancels the flow without spawning.
func TestSpawnEscCancels(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = mm.(model)
	if spawnOpen(m) {
		t.Fatal("esc should cancel the flow")
	}
	if c.spawnCalled {
		t.Fatal("esc should not spawn")
	}
}

func TestSpawnViewDirStep(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{
		{Label: "argus", Cwd: "/a"},
		{Label: "cmp", Cwd: "/b"},
	}}
	m := openSpawn(t, c)
	m.width, m.height = 80, 24
	out := spawnView(m)
	if !strings.Contains(out, "argus") || !strings.Contains(out, "Custom path") {
		t.Fatalf("dir view missing rows:\n%s", out)
	}
}

func TestSpawnViewPromptStep(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "argus", Cwd: "/a"}}}
	m := openSpawn(t, c)
	m.width, m.height = 80, 24
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)
	if !strings.Contains(strings.ToLower(spawnView(m)), "initial prompt") {
		t.Fatalf("prompt view missing label:\n%s", spawnView(m))
	}
}

// The dir list must render vertically and never overflow the terminal width,
// even with many projects and long labels/paths.
func TestSpawnViewDirListFitsWidth(t *testing.T) {
	var projects []session.HistoryProject
	for i := 0; i < 12; i++ {
		projects = append(projects, session.HistoryProject{
			Label: fmt.Sprintf("project-with-a-fairly-long-name-%02d", i),
			Cwd:   fmt.Sprintf("/home/user/dev/workspace/project-directory-%02d", i),
		})
	}
	c := &spawnPickClient{projects: projects}
	m := openSpawn(t, c)
	m.width, m.height = 80, 24
	out := spawnView(m)
	// No rendered line may exceed the terminal width (the bug: horizontal overflow).
	for _, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w > 80 {
			t.Fatalf("spawn view line exceeds width 80 (got %d): %q", w, ln)
		}
	}
	// The list is vertical: distinct projects render on distinct lines (not joined
	// onto one row). The cursor sits on the most recent, so it must be visible.
	if !strings.Contains(out, "project-with-a-fairly-long-name-00") {
		t.Fatalf("selected project not visible in windowed list:\n%s", out)
	}
	rows := 0
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "project-with-a-fairly-long-name-") {
			rows++
		}
	}
	if rows < 2 {
		t.Fatalf("expected projects on separate lines, found %d project rows:\n%s", rows, out)
	}
}

// Backspace on a multi-byte UTF-8 rune must delete the whole rune, not one byte.
func TestSpawnPromptRuneAwareBackspace(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)

	r := '🚀'
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	m = mm.(model)
	if spawnOf(m).prompt.Value() != string(r) {
		t.Fatalf("after typing rune: prompt=%q", spawnOf(m).prompt.Value())
	}

	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = mm.(model)
	if spawnOf(m).prompt.Value() != "" {
		t.Fatalf("after backspace: prompt=%q, want empty", spawnOf(m).prompt.Value())
	}
}

func TestProjectsForNode(t *testing.T) {
	all := []session.HistoryProject{
		{Label: "argus", Cwd: "/a", NodeID: "home"},
		{Label: "cmp", Cwd: "/b", NodeID: "work"},
		{Label: "scratch", Cwd: "/c", NodeID: "home"},
	}
	got := projectsForNode(all, "home")
	if len(got) != 2 || got[0].Label != "argus" || got[1].Label != "scratch" {
		t.Fatalf("filter by node = %+v", got)
	}
	if len(projectsForNode(all, "")) != 3 {
		t.Fatalf("empty node should return all")
	}
}

func TestProjectsForNodeDedupesCwd(t *testing.T) {
	all := []session.HistoryProject{
		{Label: "argus", Cwd: "/a", NodeID: "home"},
		{Label: "argus-dup", Cwd: "/a", NodeID: "work"},
		{Label: "argus-again", Cwd: "/a", NodeID: "home"},
		{Label: "scratch", Cwd: "/c", NodeID: "home"},
	}
	got := projectsForNode(all, "")
	if len(got) != 2 || got[0].Label != "argus" || got[1].Label != "scratch" {
		t.Fatalf("dedupe by cwd = %+v", got)
	}
	if len(projectsForNode(all, "home")) != 2 {
		t.Fatalf("dedupe within node = %+v", projectsForNode(all, "home"))
	}
}

func TestProjectsForNodeDropsMissing(t *testing.T) {
	all := []session.HistoryProject{
		{Label: "gone", Cwd: "/a", NodeID: "work", Missing: true},
		{Label: "argus", Cwd: "/a", NodeID: "home"},
		{Label: "old", Cwd: "/b", NodeID: "home", Missing: true},
	}
	got := projectsForNode(all, "")
	if len(got) != 1 || got[0].Label != "argus" {
		t.Fatalf("missing dirs should be dropped before dedupe, got %+v", got)
	}
}

// A pasted PR link must land in the prompt buffer; the custom path drops newlines.
func TestSpawnPaste(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)

	// Dir step, custom path row: paste is stripped of CR and LF line breaks.
	m = withSpawn(m, func(s *spawnComp, _ *ctx) {
		s.custom = true
		s.cwd = newSpawnCwdInput()
	})
	mm, _ := m.Update(tea.PasteMsg{Content: "/tmp/x\r\n"})
	m = mm.(model)
	if spawnOf(m).cwd.Value() != "/tmp/x" {
		t.Fatalf("custom path after paste = %q, want %q", spawnOf(m).cwd.Value(), "/tmp/x")
	}

	// Prompt step: paste lands verbatim.
	m = withSpawn(m, func(s *spawnComp, c *ctx) {
		s.custom = false
		s.enterPrompt(c)
	})
	url := "review https://github.com/o/r/pull/1"
	mm, _ = m.Update(tea.PasteMsg{Content: url})
	m = mm.(model)
	if spawnOf(m).prompt.Value() != url {
		t.Fatalf("prompt after paste = %q, want %q", spawnOf(m).prompt.Value(), url)
	}
}

func manyDirs(n int) []session.HistoryProject {
	out := make([]session.HistoryProject, n)
	for i := range out {
		out[i] = session.HistoryProject{Label: fmt.Sprintf("p%d", i), Cwd: fmt.Sprintf("/src/p%d", i)}
	}
	return out
}

// The dir list takes the list keys: G, gg, and half-page jumps.
func TestSpawnDirListKeys(t *testing.T) {
	m := openSpawn(t, &spawnPickClient{projects: manyDirs(30)})
	m.width, m.height = 80, 40
	m = typeKeys(m, "G")
	if got := spawnOf(m).cursor; got != 30 { // the trailing "Custom path…" row
		t.Fatalf("G: cursor=%d want 30", got)
	}
	m = typeKeys(m, "gg")
	if got := spawnOf(m).cursor; got != 0 {
		t.Fatalf("gg: cursor=%d want 0", got)
	}
	m = pressKeys(m, ctrlKey('d'))
	down := spawnOf(m).cursor
	if down <= 0 {
		t.Fatalf("<C-d>: cursor=%d, want it to move down", down)
	}
	m = pressKeys(m, ctrlKey('u'))
	if got := spawnOf(m).cursor; got != 0 {
		t.Fatalf("<C-u>: cursor=%d want 0", got)
	}
}

// / filters the dirs by label or path; enter keeps the filter and picks from
// the filtered list; esc clears it without leaving the flow.
func TestSpawnDirFilter(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{
		{Label: "argus", Cwd: "/home/u/argus"},
		{Label: "web", Cwd: "/srv/web"},
		{Label: "cmp", Cwd: "/home/u/cmp"},
	}}
	m := openSpawn(t, c)
	m.width, m.height = 80, 24
	m = typeKeys(m, "/srv")
	if dirs := spawnOf(m).shownDirs(); len(dirs) != 1 || dirs[0].Label != "web" {
		t.Fatalf("filter by path: %+v", dirs)
	}
	if out := spawnView(m); strings.Contains(out, "argus") || !strings.Contains(out, "Custom path") {
		t.Fatalf("filtered view should hide argus and keep Custom path:\n%s", out)
	}
	m = pressKeys(m, keyMsg("esc"))
	if !spawnOpen(m) || spawnOf(m).dirQuery != "" || len(spawnOf(m).shownDirs()) != 3 {
		t.Fatalf("esc should clear the filter and stay open: open=%v query=%q", spawnOpen(m), spawnOf(m).dirQuery)
	}
	m = typeKeys(m, "/cmp")
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // keep the filter
	if spawnOf(m).filter.on || spawnOf(m).dirQuery != "cmp" {
		t.Fatalf("enter should keep the filter: on=%v query=%q", spawnOf(m).filter.on, spawnOf(m).dirQuery)
	}
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // pick
	if spawnOf(m).step != spawnStepPrompt || spawnOf(m).cwd.Value() != "/home/u/cmp" {
		t.Fatalf("pick from filtered list: step=%v cwd=%q", spawnOf(m).step, spawnOf(m).cwd.Value())
	}
}

// Esc clears a kept filter before it leaves the flow.
func TestSpawnDirFilterEscClearsFirst(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{
		{Label: "argus", Cwd: "/home/u/argus"},
		{Label: "cmp", Cwd: "/home/u/cmp"},
	}}
	m := openSpawn(t, c)
	m = typeKeys(m, "/cmp")
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // keep the filter
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "esc clear filter") {
		t.Fatalf("footer with a kept filter should offer clearing it: %q", f)
	}
	m = pressKeys(m, keyMsg("esc"))
	if !spawnOpen(m) || spawnOf(m).dirQuery != "" || len(spawnOf(m).shownDirs()) != 2 {
		t.Fatalf("esc should clear the kept filter and stay open: open=%v query=%q", spawnOpen(m), spawnOf(m).dirQuery)
	}
	m = pressKeys(m, keyMsg("esc"))
	if spawnOpen(m) {
		t.Fatal("a second esc should leave the flow")
	}
}

// pumpSpawnDirs feeds the completion replies among cmd's messages back in,
// skipping commands that wait (cursor blink ticks).
func pumpSpawnDirs(m model, cmd tea.Cmd) model {
	if cmd == nil {
		return m
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return m
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = pumpSpawnDirs(m, c)
		}
	case spawnDirsDueMsg, spawnDirsMsg:
		var next tea.Cmd
		m, next = upd(m, msg)
		m = pumpSpawnDirs(m, next)
	}
	return m
}

func cwdMatches(m model) []string {
	s := spawnOf(m)
	return s.cwd.MatchedSuggestions()
}

// Typing asks the node for matches; ↓ moves the marked match and tab accepts
// it, after which its children are offered.
func TestSpawnCustomPathCompletes(t *testing.T) {
	c := &spawnPickClient{
		homes: map[string]string{"": "/home/u"},
		subdirs: map[string][]string{
			"/home/u/":      {"Dev", "Docs", "src"},
			"/home/u/D":     {"Dev", "Docs"},
			"/home/u/Docs/": {"notes"},
		},
	}
	m := openSpawn(t, c)
	m.width, m.height = 80, 24
	defer func(d time.Duration) { spawnDirsDebounce = d }(spawnDirsDebounce)
	spawnDirsDebounce = time.Millisecond
	press := func(k tea.KeyPressMsg) {
		t.Helper()
		mm, cmd := m.handleKey(k)
		m = pumpSpawnDirs(mm.(model), cmd)
	}
	if got := cwdMatches(m); len(got) != 3 {
		t.Fatalf("home's children = %v, want 3", got)
	}
	press(tea.KeyPressMsg{Code: 'D', Text: "D"})
	press(tea.KeyPressMsg{Code: tea.KeyDown})
	view := ansi.Strip(spawnView(m))
	if !strings.Contains(view, "  Dev/") || !strings.Contains(view, "› Docs/") || strings.Contains(view, "src/") {
		t.Fatalf("matches under the input should list Dev/ and mark Docs/:\n%s", view)
	}
	press(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := spawnOf(m).cwd.Value(); got != "/home/u/Docs/" {
		t.Fatalf("tab accepted %q, want /home/u/Docs/", got)
	}
	if got := cwdMatches(m); len(got) != 1 || got[0] != "/home/u/Docs/notes/" {
		t.Fatalf("after accepting, matches = %v, want Docs' children", got)
	}
}

// Edits in quick succession ask for completions once, for the final path.
func TestSpawnCustomPathDebouncesCompletion(t *testing.T) {
	c := &spawnPickClient{homes: map[string]string{"": "/home/u"}}
	m := openSpawn(t, c)
	asked := len(c.completed)
	var dues []spawnDirsDueMsg
	for _, r := range "De" {
		mm, cmd := m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(model)
		for _, msg := range execCmd(cmd) {
			if due, ok := msg.(spawnDirsDueMsg); ok {
				dues = append(dues, due)
			}
		}
	}
	if len(dues) != 2 || len(c.completed) != asked {
		t.Fatalf("typing scheduled %d requests and sent %d, want 2 scheduled and none sent", len(dues), len(c.completed)-asked)
	}
	if _, cmd := upd(m, dues[0]); cmd != nil {
		t.Fatal("the superseded edit's debounce should not ask for completions")
	}
	_, cmd := upd(m, dues[1])
	msgs := execCmd(cmd)
	if len(msgs) != 1 || msgs[0].(spawnDirsMsg).path != "/home/u/De" {
		t.Fatalf("the latest edit's debounce: %v, want one request for /home/u/De", msgs)
	}
}
