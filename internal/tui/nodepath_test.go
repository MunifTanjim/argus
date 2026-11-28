package tui

import (
	"encoding/json"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func livePathModel(sessionID string, entries int) (model, *recordingClient) {
	m := testModel()
	rc := &recordingClient{}
	m.client = rc
	m = withLive(m, sessionID)
	m.sessions = map[string]session.Session{sessionID: {ID: sessionID}}
	m = withTr(m, func(t *transcriptComp) {
		t.activeSub = subRef{subID: "x", sessionID: sessionID, cacheKey: sessionID}
		t.transcript.entries = userEntries(entries)
	})
	m.transcriptCache = map[string]cachedTranscript{sessionID: {entries: userEntries(entries)}}
	m.nodeVias = map[string]string{"n1": "gateway", "n2": "gateway"}
	return m, rc
}

func TestNodePathResubscribesLive(t *testing.T) {
	m, rc := livePathModel("n1:s1", 3)
	_, cmd := upd(m, nodePathMsg{NodeID: "n1", Via: "tcp"})
	runCmd(cmd)
	subs := subscribeCalls(rc)
	if len(subs) != 1 || subs[0].SubID != "x" || subs[0].HaveEntries != 3 {
		t.Fatalf("want one re-subscribe of x with have_entries 3, got %+v", subs)
	}
	if rc.count(api.MethodTerminalList) != 1 {
		t.Errorf("terminal.list calls = %d, want 1", rc.count(api.MethodTerminalList))
	}
}

func TestNodePathOtherNodeNoop(t *testing.T) {
	m, rc := livePathModel("n1:s1", 3)
	_, cmd := upd(m, nodePathMsg{NodeID: "n2", Via: "tcp"})
	runCmd(cmd)
	if subs := subscribeCalls(rc); len(subs) != 0 {
		t.Fatalf("other node's path must not re-subscribe: %+v", subs)
	}
}

func TestNodePathReopensTerminal(t *testing.T) {
	m := testModel()
	rc := &recordingClient{}
	m.client = rc
	emu, cur, frame := newScreenEmulator(80, 24)
	s := screenComp{terminalID: "t1", nodeID: "n1", termID: "old", term: emu, cursorState: cur, frame: frame, stop: make(chan struct{})}
	go drainEmulator(s.term, s.stop)
	defer close(s.stop)
	m.main = append(m.main, s)
	m.nodeVias = map[string]string{"n1": "tcp"}

	m, cmd := upd(m, nodePathMsg{NodeID: "n1", Via: "ssh"})
	runCmd(cmd)
	opens := rc.paramsOf(api.MethodTerminalOpen)
	if len(opens) != 1 {
		t.Fatalf("terminal.open calls = %d, want 1", len(opens))
	}
	p := opens[0].(api.TerminalOpenParams)
	got, ok := m.main.top().(screenComp)
	if !ok || got.gone {
		t.Fatalf("screen must stay open, top = %T", m.main.top())
	}
	if p.TerminalID != "t1" || p.TermID != got.termID || got.termID == "old" {
		t.Fatalf("reopen params %+v, screen termID %q", p, got.termID)
	}
	// The evicted exit of the pre-switch attach must not close the new one.
	m, _ = upd(m, notificationMsg(exitedNote("old", api.TermExitedEvicted)))
	if s, ok := m.main.top().(screenComp); !ok || s.gone {
		t.Fatal("stale evicted exit closed the reopened screen")
	}
}

func TestNodePathTracksVia(t *testing.T) {
	m := testModel()
	m, _ = upd(m, nodePathMsg{NodeID: "n1", Via: "tcp"})
	if m.nodeVias["n1"] != "tcp" {
		t.Fatalf("via = %q, want tcp", m.nodeVias["n1"])
	}
	m, _ = upd(m, nodePathMsg{NodeID: "n1", Via: ""})
	if _, ok := m.nodeVias["n1"]; ok {
		t.Fatal("empty via must forget the node")
	}
}

// Only a switch between two links reopens anything: a node's first link
// (startup, reconnect) and a repeated link do not.
func TestNodePathActsOnlyOnSwitch(t *testing.T) {
	m, rc := livePathModel("n1:s1", 3)
	m.nodeVias = map[string]string{}
	for _, via := range []string{"gateway", "gateway", "", "gateway"} {
		var cmd tea.Cmd
		m, cmd = upd(m, nodePathMsg{NodeID: "n1", Via: via})
		runCmd(cmd)
	}
	if subs := subscribeCalls(rc); len(subs) != 0 {
		t.Fatalf("first or repeated link re-subscribed: %+v", subs)
	}
	if n := rc.count(api.MethodTerminalList); n != 0 {
		t.Fatalf("terminal.list calls = %d, want 0", n)
	}
	if m.nodeVias["n1"] != "gateway" {
		t.Fatalf("via = %q, want gateway", m.nodeVias["n1"])
	}
	_, cmd := upd(m, nodePathMsg{NodeID: "n1", Via: "tcp"})
	runCmd(cmd)
	if subs := subscribeCalls(rc); len(subs) != 1 {
		t.Fatalf("switch re-subscribes = %d, want 1", len(subs))
	}
}

func exitedNote(termID, reason string) api.Notification {
	params, _ := json.Marshal(api.TerminalExited{TermID: termID, Reason: reason})
	return api.Notification{Method: api.MethodTerminalExited, Params: params}
}

// A drop delivers a state change and the no-link path resets on separate
// channels, so either can arrive first.
func TestReconnectPathAndStateOrders(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []tea.Msg
	}{
		{"path before state", []tea.Msg{nodePathMsg{NodeID: "n1"}, connStateMsg{connected: false}}},
		{"state before path", []tea.Msg{connStateMsg{connected: false}, nodePathMsg{NodeID: "n1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testModel()
			rc := &recordingClient{}
			m.client = rc
			emu, cur, frame := newScreenEmulator(80, 24)
			s := screenComp{terminalID: "t1", nodeID: "n1", termID: "old", term: emu, cursorState: cur, frame: frame, stop: make(chan struct{})}
			go drainEmulator(s.term, s.stop)
			m.main = append(m.main, s)
			m.nodeVias = map[string]string{"n1": "tcp"}

			for _, msg := range tc.order {
				var cmd tea.Cmd
				m, cmd = upd(m, msg)
				runCmd(cmd)
			}
			if got, ok := m.nodeVias["n1"]; ok {
				t.Errorf("via = %q, want none while disconnected", got)
			}
			if _, ok := m.main.top().(screenComp); ok {
				t.Error("the screen of a dropped connection must close")
			}
			if n := rc.count(api.MethodTerminalClose); n != 0 {
				t.Errorf("terminal.close calls = %d, want 0", n)
			}
			if n := rc.count(api.MethodTerminalOpen); n != 0 {
				t.Errorf("terminal.open sent on a dead client: %d", n)
			}
		})
	}
}
