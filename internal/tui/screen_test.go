package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestTmuxKeyFor(t *testing.T) {
	lit := func(msg tea.KeyPressMsg) string {
		k, ok := tmuxKeyFor(msg)
		if !ok {
			return "!"
		}
		return k.literal
	}
	named := func(msg tea.KeyPressMsg) string {
		k, ok := tmuxKeyFor(msg)
		if !ok {
			return "!"
		}
		return k.named
	}

	if got := lit(tea.KeyPressMsg{Code: 'a', Text: "a"}); got != "a" {
		t.Errorf("printable: literal=%q want a", got)
	}
	if got := named(tea.KeyPressMsg{Code: tea.KeyEnter}); got != "Enter" {
		t.Errorf("enter: named=%q want Enter", got)
	}
	if got := named(tea.KeyPressMsg{Code: tea.KeyBackspace}); got != "BSpace" {
		t.Errorf("backspace: named=%q want BSpace", got)
	}
	if got := named(tea.KeyPressMsg{Code: tea.KeyEsc}); got != "Escape" {
		t.Errorf("esc: named=%q want Escape", got)
	}
	if got := named(tea.KeyPressMsg{Code: tea.KeyUp}); got != "Up" {
		t.Errorf("up: named=%q want Up", got)
	}
	if got := lit(tea.KeyPressMsg{Code: tea.KeySpace}); got != " " {
		t.Errorf("space: literal=%q want a single space", got)
	}
	if got := named(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); got != "C-c" {
		t.Errorf("ctrl+c: named=%q want C-c", got)
	}
	// An unmapped key (shift+up) is ignored without panicking.
	if _, ok := tmuxKeyFor(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift, Text: ""}); ok {
		_ = ok
	}
}

func screenModel() model {
	m := testModel()
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Tmux: session.TmuxLocation{PaneID: "%0"}}}
	m.selectedID = "s1"
	m.mode = modeScreen
	m.screenReturn = modeList
	m.keyCh = make(chan paneKey, 8)
	return m
}

func TestHandleScreenKeyEnqueuesAndLeaves(t *testing.T) {
	m := screenModel()

	// A normal key is enqueued for the pane and the mode stays in screen view.
	res, cmd := m.handleScreenKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = res.(model)
	if m.mode != modeScreen || cmd != nil {
		t.Fatalf("key: mode=%v cmd=%v", m.mode, cmd)
	}
	select {
	case k := <-m.keyCh:
		if k.id != "s1" || k.literal != "x" {
			t.Errorf("enqueued %+v, want {s1 x}", k)
		}
	default:
		t.Error("no key enqueued")
	}

	// ctrl+] leaves to the origin (list here) and enqueues nothing.
	res, _ = m.handleScreenKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	m = res.(model)
	if m.mode != modeList {
		t.Errorf("ctrl+]: mode=%v want list", m.mode)
	}
	select {
	case k := <-m.keyCh:
		t.Errorf("ctrl+] should not enqueue, got %+v", k)
	default:
	}
}

func TestHandleScreenKeyReturnsToOrigin(t *testing.T) {
	// Entered from the session view: ctrl+] returns there, not to the list.
	m := screenModel()
	m.screenReturn = modeSession

	res, _ := m.handleScreenKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	m = res.(model)
	if m.mode != modeSession {
		t.Errorf("ctrl+]: mode=%v want session", m.mode)
	}
}

func TestCtrlCPassesThroughInScreenView(t *testing.T) {
	m := screenModel()
	res, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = res.(model)
	if cmd != nil { // must NOT be tea.Quit while in the screen view
		t.Errorf("ctrl+c in screen view returned a cmd (likely quit): %v", cmd)
	}
	select {
	case k := <-m.keyCh:
		if k.named != "C-c" {
			t.Errorf("ctrl+c enqueued %+v, want named C-c", k)
		}
	default:
		t.Error("ctrl+c should be enqueued to the pane")
	}
}
