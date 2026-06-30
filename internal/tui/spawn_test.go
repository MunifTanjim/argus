package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// spawnPickClient serves a canned server.info and records the spawn it receives.
type spawnPickClient struct {
	nodes       []api.NodeInfo
	projects    []session.HistoryProject
	spawnCalled bool
	spawnNodeID string
	spawnCwd    string
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
	case api.MethodSessionSpawn:
		c.spawnCalled = true
		if sp, ok := params.(api.SpawnParams); ok {
			c.spawnNodeID = sp.NodeID
			c.spawnCwd = sp.Cwd
			c.spawnPrompt = sp.Prompt
		}
	}
	return nil
}

func (c *spawnPickClient) Events() <-chan api.Notification { return make(chan api.Notification) }
func (c *spawnPickClient) States() <-chan bool             { return make(chan bool) }
func (c *spawnPickClient) Reconnect()                      {}
func (c *spawnPickClient) Close() error                    { return nil }

// openSpawn runs actListNew and feeds the resulting spawnNodesMsg, returning the
// model parked at its first spawn step.
func openSpawn(t *testing.T, c *spawnPickClient) model {
	t.Helper()
	m := newModel(c, false, nil, nil)
	_, cmd := m.actListNew(tea.KeyPressMsg{})
	if cmd == nil {
		t.Fatal("actListNew returned no command")
	}
	mm, _ := m.Update(cmd())
	return mm.(model)
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
	if m.spawn.step != spawnStepNode {
		t.Fatalf("step=%v want node", m.spawn.step)
	}
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown}) // cursor → beta
	m = mm.(model)
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // choose beta → dir
	m = mm.(model)
	if m.spawn.step != spawnStepDir || len(m.spawn.dirs) != 1 || m.spawn.dirs[0].Cwd != "/beta/1" {
		t.Fatalf("dir step not filtered to beta: %+v", m.spawn.dirs)
	}
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // pick dir → prompt
	m = mm.(model)
	if m.spawn.step != spawnStepPrompt {
		t.Fatalf("step=%v want prompt", m.spawn.step)
	}
	// Empty prompt must NOT spawn (mandatory).
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if m.spawn.prompt != "line1\nline2" {
		t.Fatalf("prompt = %q, want \"line1\\nline2\"", m.spawn.prompt)
	}
	if m.spawn.step != spawnStepPrompt {
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
	if m.spawn.prompt != "a\nb" {
		t.Fatalf("prompt = %q, want \"a\\nb\"", m.spawn.prompt)
	}
	if m.spawn.step != spawnStepPrompt {
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
	if n := strings.Count(m.spawnView(), "\n") + 1; n > m.height {
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
	if m.spawn.step != spawnStepPrompt {
		t.Fatalf("step=%v want prompt", m.spawn.step)
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
	if m.spawn.step != spawnStepDir {
		t.Fatalf("single node should start at dir, got %v", m.spawn.step)
	}
	if len(m.spawn.dirs) != 1 || m.spawn.cursor != 0 {
		t.Fatalf("most-recent not pre-selected: dirs=%+v cursor=%d", m.spawn.dirs, m.spawn.cursor)
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
	if m.spawn.step != spawnStepNode {
		t.Fatalf("non-tmux node should stay on node step, got %v", m.spawn.step)
	}
	m.width, m.height = 80, 24
	if !strings.Contains(m.spawnView(), "no tmux") {
		t.Fatalf("node view should mark the node as having no tmux:\n%s", m.spawnView())
	}
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // enter on disabled node
	m = mm.(model)
	if cmd != nil || m.spawn.step != spawnStepNode {
		t.Fatalf("enter on a disabled node must be a no-op; step=%v", m.spawn.step)
	}
}

// Plain local node (server.info returns one self-entry with empty ID): when it
// lacks tmux the flow is gated on the node step; when capable it skips straight to
// the dir step with node_id left empty (so projects are not filtered away).
func TestSpawnLocalNodeNoTmuxGated(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{Label: "box", Capabilities: api.NodeCapabilities{SpawnSession: false}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1"}},
	}
	m := openSpawn(t, c)
	if m.spawn.step != spawnStepNode {
		t.Fatalf("non-tmux local node should stay on node step, got %v", m.spawn.step)
	}
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(model)
	if cmd != nil || m.spawn.step != spawnStepNode {
		t.Fatalf("enter on disabled local node must be a no-op; step=%v", m.spawn.step)
	}
}

func TestSpawnLocalNodeWithTmuxSkipsToDir(t *testing.T) {
	c := &spawnPickClient{
		nodes:    []api.NodeInfo{{Label: "box", Capabilities: api.NodeCapabilities{SpawnSession: true}}},
		projects: []session.HistoryProject{{Label: "p1", Cwd: "/p/1"}},
	}
	m := openSpawn(t, c)
	if m.spawn.step != spawnStepDir {
		t.Fatalf("capable local node should skip to dir, got %v", m.spawn.step)
	}
	if m.spawn.nodeID != "" {
		t.Fatalf("local node_id must stay empty, got %q", m.spawn.nodeID)
	}
	if len(m.spawn.dirs) != 1 { // project (empty NodeID) not filtered away
		t.Fatalf("projects should be unfiltered for the local node: %+v", m.spawn.dirs)
	}
}

// Empty history drops into free-text path entry seeded with the fallback cwd.
func TestSpawnEmptyHistoryGoesToCustom(t *testing.T) {
	c := &spawnPickClient{}
	m := openSpawn(t, c)
	if m.spawn.step != spawnStepDir || !m.spawn.custom {
		t.Fatalf("empty history → custom; step=%v custom=%v", m.spawn.step, m.spawn.custom)
	}
	if m.spawn.cwd == "" {
		t.Fatal("custom cwd should be seeded with the fallback")
	}
}

// Esc cancels the flow without spawning.
func TestSpawnEscCancels(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = mm.(model)
	if m.spawn.active() {
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
	out := m.spawnView()
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
	if !strings.Contains(strings.ToLower(m.spawnView()), "initial prompt") {
		t.Fatalf("prompt view missing label:\n%s", m.spawnView())
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
	out := m.spawnView()
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
	// Test via the editText helper (covers the dir custom-path buffer).
	for _, tc := range []struct {
		name  string
		input string
		rune  rune
	}{
		{"two-byte é", "é", 'é'},
		{"four-byte rocket", "🚀", '🚀'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := editText(tc.input, tea.KeyPressMsg{Code: tea.KeyBackspace})
			if got != "" {
				t.Fatalf("editText backspace on %q: got %q, want empty", tc.input, got)
			}
		})
	}

	// Also test via the live prompt buffer in the prompt step.
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)

	r := '🚀'
	mm, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	m = mm.(model)
	if m.spawn.prompt != string(r) {
		t.Fatalf("after typing rune: prompt=%q", m.spawn.prompt)
	}

	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = mm.(model)
	if m.spawn.prompt != "" {
		t.Fatalf("after backspace: prompt=%q, want empty", m.spawn.prompt)
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
