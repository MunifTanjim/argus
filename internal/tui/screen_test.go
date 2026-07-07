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
	m.selectedID = "s1"
	m.mode = modeScreen
	m.screenReturn = modeList
	m.termID = "s1"
	m.term = vt.NewEmulator(80, 24)
	return m, c
}

func TestHandleScreenKeySendsInputAndLeaves(t *testing.T) {
	m, c := screenModel()

	// A normal key is enqueued for the ordered sender (not a per-key command) and
	// the mode stays in screen view.
	res, cmd := m.handleScreenKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = res.(model)
	if m.mode != modeScreen {
		t.Fatalf("key: mode=%v want screen", m.mode)
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
	if m.mode != modeList {
		t.Errorf("ctrl+]: mode=%v want list", m.mode)
	}
	if m.term != nil || m.termID != "" {
		t.Errorf("ctrl+]: term not cleared (term=%v id=%q)", m.term, m.termID)
	}
	runCmd(cmd)
	if !slices.Contains(c.calledMethods(), api.MethodTerminalClose) {
		t.Errorf("ctrl+]: calls=%v want terminal.close", c.calledMethods())
	}
}

func TestHandleScreenKeyReturnsToOrigin(t *testing.T) {
	m, _ := screenModel()
	m.screenReturn = modeSession
	res, _ := m.handleScreenKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	m = res.(model)
	if m.mode != modeSession {
		t.Errorf("ctrl+]: mode=%v want session", m.mode)
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
		if got := res.(model); got.mode != modeList || got.term != nil {
			t.Errorf("form %+v: mode=%v term=%v, want list + nil term", msg, got.mode, got.term)
		}
	}
	// A plain key is NOT a leave: it streams to the PTY and stays in screen mode.
	m, _ := screenModel()
	res, _ := m.handleScreenKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	if got := res.(model); got.mode != modeScreen {
		t.Errorf("plain ]: mode=%v want screen", got.mode)
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
		t.Error("ctrl+c was not enqueued for the PTY (must not quit in screen view)")
	}
}

func TestEnterScreenOpensAttach(t *testing.T) {
	c := &recordingClient{}
	m := testModel()
	m.client = c
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Tmux: session.TmuxLocation{PaneID: "%0"}}}
	m.mode = modeList
	m2, cmd := m.enterScreen("s1")
	if m2.mode != modeScreen || m2.selectedID != "s1" || m2.termID == "" || m2.term == nil {
		t.Fatalf("enterScreen: mode=%v sel=%q id=%q term=%v", m2.mode, m2.selectedID, m2.termID, m2.term)
	}
	if m2.screenReturn != modeList {
		t.Errorf("screenReturn=%v want list", m2.screenReturn)
	}
	runCmd(cmd)
	if !slices.Contains(c.calledMethods(), api.MethodTerminalOpen) {
		t.Errorf("enterScreen: calls=%v want terminal.open", c.calledMethods())
	}
}
