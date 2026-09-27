package tui

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

var screenLeaveKey = tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl}

func liveSession(t *testing.T) (model, *recordingClient) {
	t.Helper()
	c := &recordingClient{}
	m := homeTestModel()
	m.client = c
	s := m.sessions["n1:s1"]
	s.CanOpenTerminal = true
	m.sessions["n1:s1"] = s
	m = pressKeys(m, keyMsg("enter"))
	assertView(t, "<CR> on a session", m, viewSession)
	return m, c
}

func openScreen(t *testing.T, m model) model {
	t.Helper()
	n := len(m.main)
	m, cmd := upd(m, ctrlKey('t'))
	runCmd(cmd)
	if _, ok := m.liveScreen(); !ok || len(m.main) != n+1 {
		t.Fatalf("<C-t>: top = %#v, stack %d, want the live screen pushed on %d", m.main.top(), len(m.main), n)
	}
	return m
}

func called(c *recordingClient, method string) int {
	n := 0
	for _, m := range c.calledMethods() {
		if m == method {
			n++
		}
	}
	return n
}

func TestLiveScreenRoundTrip(t *testing.T) {
	m, c := liveSession(t)
	n := len(m.main)
	m = openScreen(t, m)
	if called(c, api.MethodTerminalOpen) != 1 {
		t.Fatalf("<C-t>: calls = %v, want one terminal.open", c.calledMethods())
	}
	s, _ := m.liveScreen()
	if s.termID == "" || s.term == nil {
		t.Fatalf("<C-t>: screen = %+v, want an attach", s)
	}

	m = pressKeys(m, keyMsg("x"))
	if k := <-m.termKeyCh; k.termID != s.termID || string(k.data) != "x" {
		t.Errorf("a key on the live screen: queued %+v, want x for %q", k, s.termID)
	}

	m, cmd := upd(m, screenLeaveKey)
	runCmd(cmd)
	assertView(t, "^]", m, viewSession)
	if len(m.main) != n {
		t.Errorf("^]: stack %d, want %d", len(m.main), n)
	}
	if called(c, api.MethodTerminalClose) != 1 {
		t.Errorf("^]: calls = %v, want one terminal.close", c.calledMethods())
	}
	if called(c, api.MethodTranscriptUnsubscribe) != 0 {
		t.Errorf("^]: calls = %v, want the transcript stream left open", c.calledMethods())
	}
}

func TestLiveScreenConnectionLostPopsWithoutClose(t *testing.T) {
	m, c := liveSession(t)
	n := len(m.main)
	m = openScreen(t, m)

	m, cmd := upd(m, connStateMsg{connected: false})
	runCmd(cmd)
	assertView(t, "connection lost", m, viewSession)
	if len(m.main) != n || m.flash != "terminal detached" {
		t.Errorf("connection lost: stack %d flash %q, want %d and terminal detached", len(m.main), m.flash, n)
	}
	if called(c, api.MethodTerminalClose) != 0 {
		t.Errorf("connection lost: calls = %v, want no terminal.close", c.calledMethods())
	}
}

func TestLiveScreenTerminalExitedPopsWithoutClose(t *testing.T) {
	for _, tc := range []struct {
		reason string
		flash  string
	}{
		{"", "terminal exited"},
		{api.TermExitedEvicted, "terminal opened elsewhere"},
	} {
		m, c := liveSession(t)
		n := len(m.main)
		m = openScreen(t, m)
		s, _ := m.liveScreen()

		other, _ := json.Marshal(api.TerminalExited{TermID: "other"})
		m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalExited, Params: other}))
		if _, ok := m.liveScreen(); !ok {
			t.Fatalf("%q: another terminal's exit closed the live screen", tc.reason)
		}

		params, _ := json.Marshal(api.TerminalExited{TermID: s.termID, Reason: tc.reason})
		m, cmd := upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalExited, Params: params}))
		runCmd(cmd)
		assertView(t, "terminal exited", m, viewSession)
		if len(m.main) != n || m.flash != tc.flash {
			t.Errorf("%q: stack %d flash %q, want %d and %q", tc.reason, len(m.main), m.flash, n, tc.flash)
		}
		if called(c, api.MethodTerminalClose) != 0 {
			t.Errorf("%q: calls = %v, want no terminal.close", tc.reason, c.calledMethods())
		}
	}
}

func TestLiveScreenTakesEveryKeyButTheLeaveKey(t *testing.T) {
	m, _ := liveSession(t)
	m = openScreen(t, m)
	m, cmd := upd(m, ctrlKey('c'))
	if quits(cmd) {
		t.Fatal("ctrl+c on the live screen quit argus")
	}
	m = pressKeys(m, keyMsg("esc"), keyMsg("q"), keyMsg(" "))
	var got []string
	for len(m.termKeyCh) > 0 {
		got = append(got, string((<-m.termKeyCh).data))
	}
	if !slices.Equal(got, []string{"\x03", "\x1b", "q", " "}) {
		t.Errorf("keys on the live screen: queued %q, want ^C, esc, q, the leader", got)
	}
	if _, ok := m.liveScreen(); !ok {
		t.Errorf("keys other than ^] left the live screen: top = %#v", m.main.top())
	}
}

func TestTerminalOutputGoesToItsScreen(t *testing.T) {
	m, _ := liveSession(t)
	m = openScreen(t, m)
	s, _ := m.liveScreen()
	m = writeTerm(m, s.termID, "hello")
	writeTerm(m, "other", "zzz")
	if out := s.term.Render(); !strings.Contains(out, "hello") || strings.Contains(out, "zzz") {
		t.Errorf("render = %q, want its own output only", out)
	}
}

func writeTerm(m model, termID, data string) model {
	params, _ := json.Marshal(api.TerminalOutput{TermID: termID, Data: base64.StdEncoding.EncodeToString([]byte(data))})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalOutput, Params: params}))
	return m
}

type openSessionState struct {
	header string // the name the main pane's header shows
	dock   string // the tool the drawn dock asks about; "" with no dock drawn
	answer string // the session a dock answer goes to; "" with no dock
	attach string // the session the live screen attaches, or is attached, to
	ws     string // the workspace the right sidebar shows
}

func openSessionOf(t *testing.T, m model) openSessionState {
	t.Helper()
	frame := ansi.Strip(m.View().Content)
	var st openSessionState
	for _, name := range []string{"alpha", "bravo"} {
		if strings.Contains(frame, name) {
			if st.header != "" {
				t.Fatalf("the frame shows both sessions:\n%s", frame)
			}
			st.header = name
		}
	}
	for _, tool := range []string{"Read", "Bash"} {
		if m.dockDrawn() && strings.Contains(frame, "Allow "+tool+"?") {
			st.dock = tool
		}
	}
	lastParam := func(c *recordingClient, method string) any {
		for i := len(c.calls) - 1; i >= 0; i-- {
			if c.calls[i] == method {
				return c.params[i]
			}
		}
		return nil
	}
	if s, ok := m.liveScreen(); ok {
		st.attach = s.sessionID
	} else {
		c := &recordingClient{}
		a := m
		a.client = c
		a, cmd := upd(a, ctrlKey('t'))
		runCmd(cmd)
		if p, ok := lastParam(c, api.MethodTerminalOpen).(api.TerminalOpenParams); ok {
			st.attach = p.SessionID
		}
		if s, ok := a.liveScreen(); ok && s.sessionID != st.attach {
			t.Fatalf("the screen reads %q but attached to %q", s.sessionID, st.attach)
		}
	}
	if m.dockDrawn() {
		c := &recordingClient{}
		a := m
		a.client = c
		a = pressKeys(a, keyMsg("tab"))
		_, cmd := upd(a, keyMsg("enter"))
		runCmd(cmd)
		if p, ok := lastParam(c, api.MethodSessionRespond).(api.RespondParams); ok {
			st.answer = p.SessionID
		}
	}
	st.ws = m.right.ws
	return st
}

// A resume that lands while the live screen shows opens its transcript over
// the screen; each back restores the session the screen shows, and the header,
// the dock, a dock answer, the attach, and the right sidebar follow it.
func TestResumeOverTheLiveScreenFollowsTheShownSession(t *testing.T) {
	m := homeTestModel()
	m.width, m.right.hidden = 160, false
	m.client = &recordingClient{}
	for id, tool := range map[string]string{"n1:s1": "Read", "n1:s2": "Bash"} {
		s := m.sessions[id]
		s.Name = map[string]string{"n1:s1": "alpha", "n1:s2": "bravo"}[id]
		s.Tmux = session.TmuxLocation{SessionName: s.Name, PaneID: "%1"}
		s.Status, s.CanOpenTerminal = session.StatusAwaitingInput, true
		s.Interaction = &session.Interaction{Kind: session.InteractionPermission, ToolName: tool,
			Options: []session.DecisionOption{{Label: "Allow", Value: "allow"}, {Label: "Deny", Value: "deny", Reject: true}}}
		m.sessions[id] = s
	}
	alpha := openSessionState{header: "alpha", dock: "Read", answer: "n1:s1", attach: "n1:s1", ws: "n1:w1"}
	alphaScreen := openSessionState{header: "alpha", attach: "n1:s1", ws: "n1:w1"}
	bravo := openSessionState{header: "bravo", dock: "Bash", answer: "n1:s2", attach: "n1:s2", ws: "n1:w2"}

	m = withHistorySessions(m, histProj, historyPage())
	m = typeKeys(m, "R")
	m = pressKeys(m, keyMsg("esc"), keyMsg("esc"))
	assertView(t, "back to Home", m, viewHome)
	m = pressKeys(m, keyMsg("enter"))
	if got := openSessionOf(t, m); got != alpha {
		t.Fatalf("<CR> on alpha: %+v, want %+v", got, alpha)
	}
	m = openScreen(t, m)
	if got := openSessionOf(t, m); got != alphaScreen {
		t.Fatalf("<C-t>: %+v, want %+v", got, alphaScreen)
	}
	m, _ = upd(m, resumeResultMsg{sessionID: "n1:s2"})
	if got := openSessionOf(t, m); got != bravo {
		t.Fatalf("the resume reply: %+v, want %+v", got, bravo)
	}
	m = pressKeys(m, keyMsg("esc"))
	if got := openSessionOf(t, m); got != alphaScreen {
		t.Fatalf("<Esc>: %+v, want %+v", got, alphaScreen)
	}
	m = pressKeys(m, screenLeaveKey)
	if got := openSessionOf(t, m); got != alpha {
		t.Fatalf("^]: %+v, want %+v", got, alpha)
	}
}

// A live screen that a resumed transcript covers still closes when its
// terminal ends, and back from the transcript skips it.
func TestABuriedLiveScreenClosesWhenItsTerminalEnds(t *testing.T) {
	ends := map[string]func(m model, termID string) model{
		"exited": func(m model, termID string) model {
			params, _ := json.Marshal(api.TerminalExited{TermID: termID})
			m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalExited, Params: params}))
			return m
		},
		"detached": func(m model, _ string) model {
			m, _ = upd(m, connStateMsg{})
			return m
		},
	}
	for name, end := range ends {
		m, c := liveSession(t)
		m = openScreen(t, m)
		s, _ := m.liveScreen()
		m, _ = upd(m, resumeResultMsg{sessionID: "n1:s2"})
		if tr, ok := m.main.top().(transcriptComp); !ok || tr.sessionID != "n1:s2" {
			t.Fatalf("%s: the resume reply: top = %#v, want n1:s2's transcript", name, m.main.top())
		}
		m = end(m, s.termID)
		if m.screenAt(s.termID) >= 0 {
			t.Errorf("%s: the buried screen stayed on the stack", name)
		}
		if tr, ok := m.main.top().(transcriptComp); !ok || tr.sessionID != "n1:s2" {
			t.Errorf("%s: top = %#v, want n1:s2's transcript to stay", name, m.main.top())
		}
		if called(c, api.MethodTerminalClose) != 0 {
			t.Errorf("%s: closed an attach the node already ended", name)
		}
		m = pressKeys(m, keyMsg("esc"))
		if tr, ok := m.main.top().(transcriptComp); !ok || tr.sessionID != "n1:s1" {
			t.Errorf("%s: <Esc>: top = %#v, want n1:s1's transcript", name, m.main.top())
		}
	}
}
