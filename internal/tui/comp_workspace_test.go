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
