package tui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/charmbracelet/x/vt"
)

func screenModel() (model, *recordingClient) {
	c := &recordingClient{}
	m := testModel()
	m.client = c
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Tmux: session.TmuxLocation{PaneID: "%0"}}}
	m = withScreenOf(withViews(m, viewHome, viewScreen), "s1")
	m = withTerm(m, "s1", vt.NewEmulator(80, 24))
	return m, c
}

func TestHandleScreenKeySendsInputAndLeaves(t *testing.T) {
	m, c := screenModel()

	// A normal key is enqueued for the ordered sender (not a per-key command) and
	// the live screen stays on top.
	res, cmd := m.handleScreenKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = res.(model)
	if viewOf(m) != viewScreen {
		t.Fatalf("key: view=%v want screen", viewOf(m))
	}
	if cmd != nil {
		t.Error("key: unexpected command; input must go through the ordered queue")
	}
	select {
	case k := <-m.termKeyCh:
		if string(k.data) != "x" {
			t.Errorf("queued key = %q want x", string(k.data))
		}
	default:
		t.Error("key was not enqueued for the ordered sender")
	}

	// ctrl+] leaves to the origin (list here) and closes the attach.
	res, cmd = m.handleScreenKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	m = res.(model)
	if viewOf(m) != viewHome {
		t.Errorf("ctrl+]: view=%v want list", viewOf(m))
	}
	if scr(m).term != nil || scr(m).termID != "" {
		t.Errorf("ctrl+]: term not cleared (term=%v id=%q)", scr(m).term, scr(m).termID)
	}
	runCmd(cmd)
	if !slices.Contains(c.calledMethods(), api.MethodTerminalClose) {
		t.Errorf("ctrl+]: calls=%v want terminal.close", c.calledMethods())
	}
}

func TestHandleScreenKeyReturnsToOrigin(t *testing.T) {
	m, _ := screenModel()
	m = withViews(m, viewSession, viewScreen)
	res, _ := m.handleScreenKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	m = res.(model)
	if viewOf(m) != viewSession {
		t.Errorf("ctrl+]: view=%v want session", viewOf(m))
	}
}

// ctrl+] must leave regardless of how the terminal reports it: as {Code:']',
// Mod:Ctrl}, the same with Text set (String() would return "]"), or the raw 0x1d
// control byte (String() would return "\x1d").
func TestHandleScreenKeyLeavesForAllCtrlBracketForms(t *testing.T) {
	forms := []tea.KeyPressMsg{
		{Code: ']', Mod: tea.ModCtrl},
		{Code: ']', Mod: tea.ModCtrl, Text: "]"},
		{Code: 0x1d},
	}
	for _, msg := range forms {
		m, _ := screenModel()
		res, _ := m.handleScreenKey(msg)
		if got := res.(model); viewOf(got) != viewHome || scr(got).term != nil {
			t.Errorf("form %+v: view=%v term=%v, want list + nil term", msg, viewOf(got), scr(got).term)
		}
	}
	// A plain key is NOT a leave: it streams to the PTY and stays on the live screen.
	m, _ := screenModel()
	res, _ := m.handleScreenKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	if got := res.(model); viewOf(got) != viewScreen {
		t.Errorf("plain ]: view=%v want screen", viewOf(got))
	}
}

func TestCtrlCPassesThroughInScreenView(t *testing.T) {
	m, _ := screenModel()
	res, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = res.(model)
	if cmd != nil {
		t.Error("ctrl+c: unexpected command; should stream to the PTY via the queue, not quit")
	}
	// ctrl+c must reach the PTY as ETX (0x03), enqueued for the ordered sender.
	select {
	case k := <-m.termKeyCh:
		if string(k.data) != "\x03" {
			t.Errorf("ctrl+c queued %q, want ETX (0x03)", string(k.data))
		}
	default:
		t.Error("ctrl+c was not enqueued for the PTY (must not quit on the live screen)")
	}
}

func TestEnterScreenOpensAttach(t *testing.T) {
	c := &recordingClient{}
	m := testModel()
	m.client = c
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Tmux: session.TmuxLocation{PaneID: "%0"}}}
	m = withView(m, viewHome)
	m2, cmd := m.enterScreen("s1")
	if viewOf(m2) != viewScreen || scr(m2).sessionID != "s1" || scr(m2).termID == "" || scr(m2).term == nil {
		t.Fatalf("enterScreen: view=%v sel=%q id=%q term=%v", viewOf(m2), scr(m2).sessionID, scr(m2).termID, scr(m2).term)
	}
	if returnView(m2) != viewHome {
		t.Errorf("screenReturn=%v want list", returnView(m2))
	}
	runCmd(cmd)
	if !slices.Contains(c.calledMethods(), api.MethodTerminalOpen) {
		t.Errorf("enterScreen: calls=%v want terminal.open", c.calledMethods())
	}
}

func TestPtyWheelBytes(t *testing.T) {
	cases := []struct {
		name string
		msg  wheelMsg
		want string
	}{
		{"up", wheelMsg{Mouse: tea.Mouse{X: 3, Y: 4}, delta: -1}, "\x1b[<64;4;5M"},
		{"down", wheelMsg{Mouse: tea.Mouse{X: 3, Y: 4}, delta: 1}, "\x1b[<65;4;5M"},
		{"clamped to the terminal", wheelMsg{Mouse: tea.Mouse{X: 500, Y: 500}, delta: -1}, "\x1b[<64;80;24M"},
	}
	for _, tc := range cases {
		if got := string(ptyWheelBytes(tc.msg, 80, 24)); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestLiveScreenForwardsWheelAndCapturesMouse(t *testing.T) {
	m, _ := screenModel()
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("the live screen should capture the mouse")
	}
	m, _ = upd(m, wheelMsg{Mouse: tea.Mouse{X: 1, Y: 1}, delta: -1})
	select {
	case k := <-m.termKeyCh:
		if string(k.data) != "\x1b[<64;2;2M" {
			t.Errorf("queued wheel = %q", k.data)
		}
	default:
		t.Error("the wheel was not sent to the terminal")
	}

	if homeTestModel().View().MouseMode != tea.MouseModeNone {
		t.Error("outside the live screen the mouse must stay with the terminal")
	}
}
