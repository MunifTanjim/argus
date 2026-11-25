package tui

import (
	"errors"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func synced(m model) model {
	m, _ = m.syncMemory()
	return m
}

func TestMemoryRemembersTheOpenSessionInItsWorkspace(t *testing.T) {
	m := pressKeys(homeTestModel(), keyMsg("enter")) // Home's cursor is on n1:s1
	if trOf(m).sessionID != "n1:s1" {
		t.Fatalf("enter on Home should open n1:s1, got %q", trOf(m).sessionID)
	}
	if got := m.memory.ws["n1:w1"]; got != "n1:s1" {
		t.Errorf("n1:w1 remembers %q, want n1:s1", got)
	}
	if m.memory.home.view != homeSessions {
		t.Errorf("Home remembers %v, want the Home pane: a session with a workspace belongs to it", m.memory.home.view)
	}
}

func TestMemoryRemembersASessionWithNoWorkspaceInHome(t *testing.T) {
	m := homeTestModel()
	m.sessions["n1:s9"] = session.Session{ID: "n1:s9", Repo: "loose"}
	m = synced(openLive(m, "n1:s9"))
	if e := m.memory.home; e.view != homeSession || e.session != "n1:s9" {
		t.Errorf("Home entry = %+v, want session n1:s9", e)
	}
}

func TestMemorySkipsASessionThatIsNotListed(t *testing.T) {
	m := synced(withHistoryProjects(homeTestModel(), historyProjects()...))
	m = synced(openLive(m, "n1:unknown"))
	if m.memory.home.view != homeHistory {
		t.Errorf("Home entry = %+v, want History: a session not in the list is not written", m.memory.home)
	}
}

func TestMemoryWorkspacePaneMeansNoSession(t *testing.T) {
	m := wideWorkspace()
	m.memory.ws = map[string]string{"n1:w1": "n1:s1"}
	m = synced(m)
	if _, ok := m.memory.ws["n1:w1"]; ok {
		t.Error("the workspace pane of n1:w1 should remove its entry")
	}
}

func TestMemoryRemembersHomeHistoryAndLogs(t *testing.T) {
	m := synced(withHistoryProjects(homeTestModel(), historyProjects()...))
	if m.memory.home.view != homeHistory {
		t.Errorf("History: Home entry = %v, want homeHistory", m.memory.home.view)
	}
	m = synced(logsModel(120, false))
	if m.memory.home.view != homeLogs {
		t.Errorf("Logs: Home entry = %v, want homeLogs", m.memory.home.view)
	}
}

func TestMemoryStoresHomeWithoutAKillPrompt(t *testing.T) {
	m := synced(typeKeys(killableHome(), "jdd"))
	if homeOf(m).killID == "" {
		t.Fatal("dd should arm the Home kill prompt")
	}
	if m.memory.home.home.killID != "" || m.memory.home.home.cursor != 1 {
		t.Errorf("stored Home = %+v, want cursor 1 and no kill prompt", m.memory.home.home)
	}
}

func TestBackOutOfASessionForgetsIt(t *testing.T) {
	m := pressKeys(homeTestModel(), keyMsg("enter"))
	m = pressKeys(m, keyMsg("esc"))
	if viewOf(m) != viewHome {
		t.Fatalf("esc should go back to Home, got %v", viewOf(m))
	}
	if _, ok := m.memory.ws["n1:w1"]; ok {
		t.Error("back out of n1:s1 should make n1:w1 forget it")
	}
}

func TestBackFromTheLiveScreenForgetsNothing(t *testing.T) {
	m := pressKeys(homeTestModel(), keyMsg("enter"))
	m, _ = m.enterScreen("n1:s1")
	m, _ = leaveScreen(m)
	m = synced(m)
	if got := m.memory.ws["n1:w1"]; got != "n1:s1" {
		t.Errorf("n1:w1 remembers %q after leaving the live screen, want n1:s1", got)
	}
}

func TestMemoryDropsASessionTheRegistryRemoved(t *testing.T) {
	m := pressKeys(homeTestModel(), keyMsg("enter"))
	m = pressKeys(m, keyMsg("esc"))
	m.memory.ws["n1:w1"] = "n1:s1"
	delete(m.sessions, "n1:s1")
	if m = synced(m); len(m.memory.ws) != 0 {
		t.Errorf("memory = %v, want n1:s1 dropped", m.memory.ws)
	}
}

func TestMemoryKeepsAnEndedSession(t *testing.T) {
	m := pressKeys(homeTestModel(), keyMsg("enter"))
	s := m.sessions["n1:s1"]
	s.Status = session.StatusDead
	m.sessions["n1:s1"] = s
	if m = synced(m); m.memory.ws["n1:w1"] != "n1:s1" {
		t.Error("an ended session in the list stays remembered")
	}
}

func TestFocusTreeWithUnknownSessionDoesNotMoveToHome(t *testing.T) {
	m := wideWorkspace()
	m = withFocus(m, mainPane)
	m = openLive(m, "n1:unknown")
	if got := m.mainRow(); got != "" {
		t.Fatalf("mainRow = %q, want empty for unknown session", got)
	}
	initial := m.left.tree.cursor
	m = pressKeys(m, cw('h')...)
	if m.left.tree.cursor != initial {
		t.Errorf("cursor moved to %d (was %d), want no move for unknown session", m.left.tree.cursor, initial)
	}
}

func TestMemoryDropsAWorkspaceTheRegistryRemoved(t *testing.T) {
	m := pressKeys(homeTestModel(), keyMsg("enter"))
	m = pressKeys(m, keyMsg("esc"))
	m.memory.ws["n1:w2"] = "n1:s2"
	p := m.left.tree.data[0]
	p.Workspaces = p.Workspaces[:1] // n1:w2 leaves
	m, _ = upd(m, projectsTreeMsg{tree: []api.ProjectNode{p}})
	if _, ok := m.memory.ws["n1:w2"]; ok {
		t.Error("n1:w2 left the project list, so its entry should go")
	}
	m.memory.ws["n1:w1"] = "n1:s1"
	m, _ = upd(m, projectsTreeMsg{err: errors.New("down")})
	if m.memory.ws["n1:w1"] != "n1:s1" {
		t.Error("a failed project load must not remove entries")
	}
}
