package tui

import (
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestConnStateTogglesReconnecting(t *testing.T) {
	m := modelWith(session.Session{ID: "a"})

	// Disconnect: flag set, stale sessions kept visible.
	nm, _ := m.Update(connStateMsg{connected: false})
	dm := nm.(model)
	if !dm.reconnecting {
		t.Error("disconnect should set reconnecting")
	}
	if len(dm.sessions) != 1 {
		t.Error("disconnect should keep the last-known sessions")
	}

	// Reconnect: flag cleared, a resync command is issued.
	nm2, cmd := dm.Update(connStateMsg{connected: true})
	if nm2.(model).reconnecting {
		t.Error("reconnect should clear reconnecting")
	}
	if cmd == nil {
		t.Error("reconnect should trigger a resync command")
	}
}

func TestReconnectReloadsTheProjectTree(t *testing.T) {
	m := projectsTestModel()
	rc := &recordingClient{}
	m.client = rc
	m.reconnecting = true
	_, cmd := m.Update(connStateMsg{connected: true})
	runCmd(cmd)
	if !slices.Contains(rc.calledMethods(), api.MethodProjectList) {
		t.Errorf("reconnect should reload the project tree: calls=%v", rc.calledMethods())
	}
}

func TestSessionsReplacedDropsStale(t *testing.T) {
	m := modelWith(session.Session{ID: "a"})
	nm, _ := m.Update(sessionsReplacedMsg([]session.Session{{ID: "b"}}))
	got := nm.(model).sessions
	if _, ok := got["b"]; !ok {
		t.Error("resync should add the new session")
	}
	if _, ok := got["a"]; ok {
		t.Error("resync should drop a session no longer present")
	}
}
