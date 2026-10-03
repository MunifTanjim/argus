package tui

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
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

func TestLiveScreenShowsTheProgramCursor(t *testing.T) {
	m, _ := liveSession(t)
	m = openScreen(t, m)
	s, _ := m.liveScreen()
	out := func(m model, raw string) model {
		b, _ := json.Marshal(api.TerminalOutput{TermID: s.termID, Data: base64.StdEncoding.EncodeToString([]byte(raw))})
		m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalOutput, Params: b}))
		return m
	}
	m = out(m, "ab")
	c := m.View().Cursor
	r := m.mainRect()
	if c == nil || c.X != r.Min.X+screenBodyX+2 || c.Y != r.Min.Y+screenBodyY {
		t.Fatalf("cursor = %+v, want after \"ab\" at (%d, %d)", c, r.Min.X+screenBodyX+2, r.Min.Y+screenBodyY)
	}
	if m = out(m, "\x1b[?25l"); m.View().Cursor != nil {
		t.Errorf("cursor = %+v after the program hid it, want none", m.View().Cursor)
	}
}

func TestLiveScreenFillsThePaneFromTheStart(t *testing.T) {
	m, _ := liveSession(t)
	m = openScreen(t, m)
	s, _ := m.liveScreen()
	b, _ := json.Marshal(api.TerminalOutput{TermID: s.termID, Data: base64.StdEncoding.EncodeToString([]byte("$ "))})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalOutput, Params: b}))
	frame := strings.Split(ansi.Strip(m.View().Content), "\n")
	top, bottom := -1, -1
	for i, l := range frame {
		if strings.Contains(l, "╭") && top < 0 {
			top = i
		}
		if strings.Contains(l, "╰") {
			bottom = i
		}
	}
	l := m.layout()
	_, rows := termDimsFor(l.w, l.h-m.dockRows(), l.right == 0)
	if top < 0 || bottom-top-1 != rows {
		t.Fatalf("box interior = %d rows (top %d, bottom %d), want %d:\n%s", bottom-top-1, top, bottom, rows, strings.Join(frame, "\n"))
	}
	if len(frame) > m.height {
		t.Errorf("frame = %d rows, want at most %d", len(frame), m.height)
	}
}

func TestLiveScreenFollowsTheCursorShape(t *testing.T) {
	m, _ := liveSession(t)
	m = openScreen(t, m)
	s, _ := m.liveScreen()
	out := func(m model, raw string) model {
		b, _ := json.Marshal(api.TerminalOutput{TermID: s.termID, Data: base64.StdEncoding.EncodeToString([]byte(raw))})
		m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodTerminalOutput, Params: b}))
		return m
	}
	if c := m.View().Cursor; c == nil || c.Shape != tea.CursorBlock || !c.Blink {
		t.Fatalf("default cursor = %+v, want a blinking block", c)
	}
	for _, tc := range []struct {
		seq   string
		shape tea.CursorShape
		blink bool
	}{
		{"\x1b[6 q", tea.CursorBar, false},
		{"\x1b[3 q", tea.CursorUnderline, true},
		{"\x1b[2 q", tea.CursorBlock, false},
	} {
		m = out(m, tc.seq)
		if c := m.View().Cursor; c == nil || c.Shape != tc.shape || c.Blink != tc.blink {
			t.Errorf("after %q: cursor = %+v, want shape %v blink %v", tc.seq, c, tc.shape, tc.blink)
		}
	}
}

func TestLiveScreenForwardsMouseButtons(t *testing.T) {
	m, _ := liveSession(t)
	m = openScreen(t, m)
	m = withMouse(m)
	m.mouse = false // the live screen takes the mouse with the setting off, like the wheel
	r := m.mainRect()
	x, y := r.Min.X+screenBodyX+3, r.Min.Y+screenBodyY+1
	queued := func(m model) []string {
		var got []string
		for len(m.termKeyCh) > 0 {
			got = append(got, string((<-m.termKeyCh).data))
		}
		return got
	}
	m.View()
	m, _ = upd(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m, _ = upd(m, tea.MouseMotionMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	m, _ = upd(m, tea.MouseReleaseMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	m, _ = upd(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	want := []string{"\x1b[<0;4;2M", "\x1b[<32;5;2M", "\x1b[<0;5;2m", "\x1b[<2;4;2M"}
	if got := queued(m); !slices.Equal(got, want) {
		t.Fatalf("queued %q, want %q", got, want)
	}
	m, _ = upd(m, tea.MouseClickMsg{X: x, Y: r.Min.Y, Button: tea.MouseLeft})
	if got := queued(m); len(got) != 0 {
		t.Errorf("a click on the header queued %q, want nothing", got)
	}
}

func TestLiveScreenBoxHasEvenSideMargins(t *testing.T) {
	m := liveScreenModelWith(func(m *model) { m.left.hidden, m.right.hidden = true, true })
	for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		i := strings.Index(l, "╭")
		if i < 0 {
			continue
		}
		left := len([]rune(l[:i]))
		right := m.width - lipgloss.Width(strings.TrimRight(l, " "))
		if left != right {
			t.Fatalf("box margins = %d left, %d right, want equal:\n%q", left, right, l)
		}
		return
	}
	t.Fatal("no screen box in the frame")
}

func TestLiveScreenFollowsTheLayout(t *testing.T) {
	m := withMouse(liveScreenModelWith(func(m *model) { m.width = 160 }))
	s, _ := m.liveScreen()
	before := s.term.Width()
	x, y := titleIconCell(t, m, treeIcon)
	m, cmd := click(m, x, y)
	cols, rows := m.termDims()
	if s.term.Width() == before || s.term.Width() != cols || s.term.Height() != rows || cmd == nil {
		t.Fatalf("screen %dx%d (was %d wide), layout %dx%d, cmd=%v: hiding the tree must resize the screen",
			s.term.Width(), s.term.Height(), before, cols, rows, cmd != nil)
	}
	m, _ = upd(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	cols, rows = m.termDims()
	if s.term.Width() != cols || s.term.Height() != rows {
		t.Fatalf("screen %dx%d, layout %dx%d: a window resize must resize the screen", s.term.Width(), s.term.Height(), cols, rows)
	}
}

func TestLiveScreenBoxHasEvenGapsBetweenSidebars(t *testing.T) {
	m := liveScreenModelWith(func(m *model) { m.width, m.right.hidden = 160, false })
	for _, ln := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if !strings.Contains(ln, "╭") {
			continue
		}
		before := strings.TrimSuffix(ln[:strings.Index(ln, "╭")], " ")
		after := strings.TrimPrefix(ln[strings.Index(ln, "╮")+len("╮"):], " ")
		if !strings.HasSuffix(before, "│") || !strings.HasPrefix(after, "│") {
			t.Fatalf("the box should sit one space from each divider: %q", ln)
		}
		return
	}
	t.Fatal("no screen box in the frame")
}
