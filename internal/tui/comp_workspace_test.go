package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

// workspacePane is the workspace pane of n1:w1 (sessions n1:s1 and n1:s3),
// reached from Home through the tree.
func workspacePane(m model) model {
	return pressKeys(typeKeys(pressKeys(m, cw('h')...), "jj"), keyMsg("enter"))
}

func TestWorkspacePaneFollowsTheTreeCursor(t *testing.T) {
	m := typeKeys(workspacePane(homeTestModel()), "j")
	if p := paneOf(m); p.ws != "n1:w1" || p.cursor != 1 {
		t.Fatalf("pane: ws=%q cursor=%d, want n1:w1 on card 1", p.ws, p.cursor)
	}
	m = pressKeys(m, keyMsg("esc"))
	m = typeKeys(m, "k")
	if p, ok := m.main[0].(workspaceComp); !ok || p.ws != "n1:w1" || p.cursor != 1 {
		t.Fatalf("a project row keeps the workspace pane: root=%#v", m.main[0])
	}
	m = typeKeys(m, "j")
	if p, ok := m.main[0].(workspaceComp); !ok || p.ws != "n1:w1" || p.cursor != 1 {
		t.Fatalf("back on the same workspace keeps its cursor: root=%#v", m.main[0])
	}
	m = typeKeys(m, "j")
	if p, ok := m.main[0].(workspaceComp); !ok || p.ws != "n1:w2" || p.cursor != 0 {
		t.Fatalf("another workspace starts over: root=%#v", m.main[0])
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "repo-feat  feature") {
		t.Error("the pane should show n1:w2")
	}
}

func TestWorkspacePaneSurvivesATripToTheHomeRow(t *testing.T) {
	m := typeKeys(workspacePane(homeTestModel()), "j")
	m = typeKeys(pressKeys(m, keyMsg("esc")), "kk")
	if _, ok := m.main[0].(homeComp); !ok {
		t.Fatalf("the Home row should show the Home pane: root=%T", m.main[0])
	}
	m = pressKeys(typeKeys(m, "jj"), keyMsg("enter"))
	if p := paneOf(m); m.focused != mainPane || p.ws != "n1:w1" || p.cursor != 1 {
		t.Errorf("back on n1:w1: focus=%v ws=%q cursor=%d, want the pane on card 1", m.focused, p.ws, p.cursor)
	}
}

func TestWorkspacePaneKeepsItsCursorUnderASession(t *testing.T) {
	m := typeKeys(workspacePane(homeTestModel()), "j")
	m = pressKeys(m, keyMsg("enter"))
	if viewOf(m) != viewSession || m.liveSessionID() != "n1:s3" {
		t.Fatalf("enter should open n1:s3: view=%v id=%q", viewOf(m), m.liveSessionID())
	}
	m = pressKeys(m, cw('h')...)
	m = pressKeys(m, keyMsg("enter"))
	if p := paneOf(m); m.focused != mainPane || p.ws != "n1:w1" || p.cursor != 1 {
		t.Errorf("the tree and back to the pane: focus=%v ws=%q cursor=%d, want card 1", m.focused, p.ws, p.cursor)
	}
}

func TestWorkspaceKillConfirmation(t *testing.T) {
	rc := &recordingClient{}
	m := killableHome()
	m.client = rc
	m = typeKeys(workspacePane(m), "jdd")
	if paneOf(m).killID != "n1:s3" || homeOf(m).pendingKill {
		t.Fatalf("dd should ask to kill n1:s3 in the pane only: pane=%q home=%v", paneOf(m).killID, homeOf(m).pendingKill)
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
		t.Fatalf("the tree on the Home row: root=%T kill=%q raw=%v, want Home and the prompt dropped", m.main[0], paneOf(m).killID, m.keysRaw())
	}
	if _, cmd := m.runKey(keyMsg("y")); cmd != nil {
		t.Error("y after the pane left must not kill")
	}
}

func TestWorkspaceKillDisarmsWhenTheTreeMovesToAnotherWorkspace(t *testing.T) {
	m := typeKeys(workspacePane(killableHome()), "jdd")
	if paneOf(m).killID != "n1:s3" {
		t.Fatalf("dd should arm the pane prompt: %q", paneOf(m).killID)
	}
	p := m.left.tree.data[0]
	p.Workspaces = p.Workspaces[1:]
	m, _ = upd(m, projectsTreeMsg{tree: []api.ProjectNode{p}})
	if pane := paneOf(m); pane.ws != "n1:w2" || pane.killID != "" || m.keysRaw() {
		t.Fatalf("n1:w1 gone: ws=%q kill=%q raw=%v, want n1:w2 and the prompt dropped", pane.ws, pane.killID, m.keysRaw())
	}
	if _, cmd := m.runKey(keyMsg("y")); cmd != nil {
		t.Error("y in another workspace must not kill")
	}
}
