package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func runSpawnReplies(m model, cmd tea.Cmd) model {
	for _, msg := range execCmd(cmd) {
		switch msg.(type) {
		case spawnNodesMsg, spawnAgentsMsg:
			var next tea.Cmd
			m, next = upd(m, msg)
			m = runSpawnReplies(m, next)
		}
	}
	return m
}

func assertSpawnOpen(t *testing.T, step string, m model, n int) {
	t.Helper()
	if !spawnOpen(m) || len(m.main) != n+1 || m.focused != mainPane {
		t.Fatalf("%s: spawn open=%v stack %d focus %v, want it pushed on %d with the main pane focused", step, spawnOpen(m), len(m.main), m.focused, n)
	}
}

func assertSpawnClosed(t *testing.T, step string, m model, n int, focus container) {
	t.Helper()
	if spawnOpen(m) || len(m.main) != n || m.focused != focus {
		t.Errorf("%s: spawn open=%v stack %d focus %v, want it popped to %d with focus %v", step, spawnOpen(m), len(m.main), m.focused, n, focus)
	}
}

func TestSpawnFromHomeOpensOverHomeAndCloses(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := homeTestModel()
	m.client = c
	n := len(m.main)
	m, cmd := upd(m, keyMsg("s"))
	m = runSpawnReplies(m, cmd)
	assertSpawnOpen(t, "s on Home", m, n)
	if _, ok := m.main[n-1].(homeComp); !ok {
		t.Fatalf("under the spawn = %#v, want Home", m.main[n-1])
	}

	cancelled := pressKeys(m, keyMsg("esc"))
	assertSpawnClosed(t, "esc", cancelled, n, mainPane)

	m = pressKeys(m, keyMsg("enter"))
	m = typeKeys(m, "go")
	m, cmd = upd(m, keyMsg("enter"))
	runCmd(cmd)
	assertSpawnClosed(t, "launch", m, n, mainPane)
	if !c.spawnCalled || c.spawnCwd != "/p" || c.spawnPrompt != "go" {
		t.Errorf("launch: spawn cwd=%q prompt=%q", c.spawnCwd, c.spawnPrompt)
	}
}

func TestSpawnFromAWorkspaceReturnsFocus(t *testing.T) {
	for _, focus := range []container{leftSidebar, mainPane} {
		c := &spawnPickClient{}
		m := projectsTestModel()
		m.width, m.height = 120, 30
		m.client = c
		m = selectRow(m, "n1:w2")
		m = withFocus(m, focus)
		n := len(m.main)
		m = pressKeys(m, keyMsg("s"))
		assertSpawnOpen(t, "s on a workspace", m, n)
		m, _ = upd(m, spawnAgentsMsg{nodeID: "n1"})

		cancelled := pressKeys(m, keyMsg("esc"))
		assertSpawnClosed(t, "esc", cancelled, n, focus)
		if !isWorkspace(cancelled.main.top()) {
			t.Errorf("focus %v, esc: top = %#v, want the workspace pane", focus, cancelled.main.top())
		}

		m = typeKeys(m, "go")
		m, cmd := upd(m, keyMsg("enter"))
		runCmd(cmd)
		assertSpawnClosed(t, "launch", m, n, focus)
		if !c.spawnCalled || c.spawnCwd != "/repo-feat" || c.spawnNodeID != "n1" {
			t.Errorf("focus %v, launch: spawn node=%q cwd=%q", focus, c.spawnNodeID, c.spawnCwd)
		}
	}
}

func TestSpawnFromTreeHomeRowReturnsFocusToTheTree(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = c
	m = selectRow(m, homeRowID)
	m = withFocus(m, leftSidebar)
	n := len(m.main)
	m, cmd := upd(m, keyMsg("s"))
	m = runSpawnReplies(m, cmd)
	assertSpawnOpen(t, "s on the Home row", m, n)
	m = pressKeys(m, keyMsg("esc"))
	assertSpawnClosed(t, "esc", m, n, leftSidebar)
}

func TestSpawnFromIssueOfferReturnsFocusToTheTree(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createComp) { p.creating = true })
	m, _ = upd(m, createDoneMsg{seq: createOf(m).seq,
		res:    api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Dir: "/repo/.worktrees/42-fix", Prompt: "Fix"},
		source: api.SourceIssue,
	})
	focus, n := m.focused, len(m.main)
	if focus != leftSidebar {
		t.Fatalf("the offer: focus = %v, want the tree", focus)
	}
	m, _ = upd(m, keyMsg("y"))
	assertSpawnOpen(t, "y on the offer", m, n)
	m, _ = upd(m, spawnAgentsMsg{nodeID: "n1", agents: []api.AgentInfo{{ID: "claude"}}})
	if spawnStepOf(m) != spawnStepPrompt {
		t.Fatalf("the agents reply: step = %v, want the prompt", spawnStepOf(m))
	}
	m, _ = upd(m, keyMsg("enter"))
	assertSpawnClosed(t, "launch", m, n, leftSidebar)
}

func TestSpawnReplyWithNoSpawnIsDropped(t *testing.T) {
	m := homeTestModel()
	before := append(backStack(nil), m.main...)
	m, cmd := upd(m, spawnAgentsMsg{nodeID: "n1", agents: []api.AgentInfo{{ID: "a"}, {ID: "b"}}})
	if spawnOpen(m) || cmd != nil || len(m.main) != len(before) {
		t.Errorf("an agents reply with no spawn open: open=%v cmd=%v stack %v", spawnOpen(m), cmd != nil, m.main)
	}
}

func TestSpawnReplyUnderTheLiveScreenWaitsForTheLeave(t *testing.T) {
	m, _ := liveSession(t)
	focus, n := m.focused, len(m.main)
	m = openScreen(t, m)
	m, _ = upd(m, spawnNodesMsg{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}, cwd: "/x"})
	if _, ok := m.liveScreen(); !ok || len(m.main) != n+2 {
		t.Fatalf("the spawn reply: top = %#v stack %d, want the live screen on top of %d", m.main.top(), len(m.main), n+2)
	}
	m = pressKeys(m, keyMsg("j"))
	if k := <-m.termKeyCh; string(k.data) != "j" {
		t.Errorf("a key with the spawn under the live screen: queued %q, want j", k.data)
	}
	m = pressKeys(m, screenLeaveKey)
	assertSpawnOpen(t, "^]", m, n)
	m = pressKeys(m, keyMsg("esc"))
	assertSpawnClosed(t, "esc", m, n, focus)
}

func TestSpawnReplyDropsAnArmedHomeKill(t *testing.T) {
	m := killableHome()
	m.client = &spawnPickClient{}
	m = typeKeys(m, "jdd")
	if !homeOf(m).pendingKill {
		t.Fatal("dd on Home should arm the kill")
	}
	m, _ = upd(m, spawnNodesMsg{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}, cwd: "/x"})
	if homeOf(m).pendingKill {
		t.Error("the spawn reply should drop the kill hidden under the flow")
	}
	m, _ = upd(m, spawnAgentsMsg{})
	m = pressKeys(m, keyMsg("j"))
	if s := spawnOf(m); !spawnOpen(m) || s.cursor != 1 {
		t.Errorf("j after the spawn reply: spawn open=%v cursor=%d, want the flow's cursor on 1", spawnOpen(m), s.cursor)
	}
}

func TestFileUnderTheSpawnKeepsItsReads(t *testing.T) {
	m := filesFocused()
	m.client = &spawnPickClient{}
	m = pressKeys(m, keyMsg("enter"))
	assertFileOnTop(t, "open", m, viewTree, "go.mod")
	m = pressKeys(m, cw('h')...)
	if m.focused != leftSidebar {
		t.Fatalf("<C-w>h: focus = %v, want the tree", m.focused)
	}
	m = pressKeys(m, keyMsg("s"))
	if !spawnOpen(m) {
		t.Fatal("s on the tree should open the spawn flow over the file")
	}
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "go.mod", content: "module argus"})
	m = pressKeys(m, keyMsg("esc"))
	assertFileOnTop(t, "esc", m, viewTree, "go.mod")
	if f := fileOf(m); f.loading || len(f.lines) != 1 {
		t.Errorf("esc: the file = %+v, want the content read while the flow showed", f)
	}
}

func TestTreeReloadDuringASpawnFromTheHomeRowMovesTheCursor(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m = selectRow(m, homeRowID)
	m = withFocus(m, leftSidebar)
	m, cmd := upd(m, keyMsg("s"))
	m = runSpawnReplies(m, cmd)
	if !spawnOpen(m) {
		t.Fatal("s on the Home row should open the spawn flow")
	}
	m.left.tree.want = "n1:w2" // e.g. a workspace action's reload
	m, _ = upd(m, projectsTreeMsg{tree: m.left.tree.data})
	if got := m.left.tree.cursorRowID(); got != "n1:w2" {
		t.Errorf("a tree reload under the spawn: cursor on %q, want the wanted row n1:w2", got)
	}
}

func TestFileShowAndLeaveActOnTheFileUnderTheSpawn(t *testing.T) {
	m := filesFocused()
	m.client = &spawnPickClient{}
	m = withFile(m, fileComp{ws: "n1:w1", path: "a.go"})
	m.beginPresetSpawn("n1", "/repo", "")
	n := len(m.main)
	m = withFile(m, fileComp{ws: "n1:w1", path: "b.go"})
	if !spawnOpen(m) || len(m.main) != n || fileOf(m).path != "b.go" {
		t.Fatalf("show under the spawn: spawn open=%v stack %d file %q, want b.go in place of a.go", spawnOpen(m), len(m.main), fileOf(m).path)
	}
	m.closeFileView()
	if !spawnOpen(m) || len(m.main) != n-1 || m.hasOpenFile() {
		t.Errorf("leave under the spawn: spawn open=%v stack %d file open=%v, want the file gone and the flow on top", spawnOpen(m), len(m.main), m.hasOpenFile())
	}
}

// The spawn flow's reply can land after the user left Home; the flow then opens
// over what shows, takes the keys, and the frame draws it.
func TestSpawnReplyOverAnotherComponentDrawsTheFlow(t *testing.T) {
	cases := []struct {
		name  string
		leave func(m model) model
		under string // what the component under the flow draws
	}{
		{"History", func(m model) model { return typeKeys(m, "gt") }, "loading projects"},
		{"a transcript", func(m model) model {
			s := m.sessions["n1:s1"]
			s.Name = "alpha"
			m.sessions["n1:s1"] = s
			return pressKeys(m, keyMsg("enter"))
		}, "alpha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := homeTestModel()
			m.client = &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
			m, cmd := upd(m, keyMsg("s"))
			m = tc.leave(m)
			if out := ansi.Strip(m.View().Content); !strings.Contains(out, tc.under) {
				t.Fatalf("setup: the frame does not show %q:\n%s", tc.under, out)
			}
			m = runSpawnReplies(m, cmd)
			if !spawnOpen(m) {
				t.Fatal("the reply should open the spawn flow")
			}
			out := ansi.Strip(m.View().Content)
			if !strings.Contains(out, "new session") || strings.Contains(out, tc.under) {
				t.Errorf("the frame should draw the spawn flow, not %q:\n%s", tc.under, out)
			}
		})
	}
}
