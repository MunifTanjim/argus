package tui

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestInvokeFocusActionReportsMove(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('l')...)
	m, _, r, err := m.invokeAction("client.pane.focus.left")
	if err != nil || !r.ok || m.focused != leftSidebar {
		t.Fatalf("left from main: r=%+v err=%v focus=%v", r, err, m.focused)
	}
	m, _, r, err = m.invokeAction("client.pane.focus.left")
	if err != nil || r.ok || m.focused != leftSidebar {
		t.Fatalf("left at the edge: r=%+v err=%v focus=%v", r, err, m.focused)
	}
}

func TestInvokeFocusUpNeverMoves(t *testing.T) {
	m := withFocus(wideWorkspace(), mainPane)
	m, _, r, err := m.invokeAction("client.pane.focus.up")
	if err != nil || r.ok || m.focused != mainPane {
		t.Fatalf("up: r=%+v err=%v focus=%v", r, err, m.focused)
	}
}

func TestInvokeFocusDownMovesToDock(t *testing.T) {
	m := withFocus(workspaceSession(&session.Interaction{Kind: session.InteractionPermission}), mainPane)
	m, _, r, err := m.invokeAction("client.pane.focus.down")
	if err != nil || !r.ok || m.focused != sessionDock {
		t.Fatalf("down: r=%+v err=%v focus=%v", r, err, m.focused)
	}
}

func TestInvokeUnavailableActionErrors(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: signerLock()})
	focused := m.focused
	m, _, _, err := m.invokeAction("client.pane.focus.left")
	if err == nil || !strings.Contains(err.Error(), "not available") || m.focused != focused {
		t.Fatalf("settings: err=%v focus=%v", err, m.focused)
	}
}

func TestInvokeUnknownActionErrors(t *testing.T) {
	if _, _, _, err := wideWorkspace().invokeAction("client.pane.focus.nowhere"); err == nil {
		t.Fatal("unknown action: err = nil")
	}
}

func TestInvokeDoesNotFlash(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('l')...)
	m, _, _, _ = m.invokeAction("client.pane.focus.left")
	m, _, _, _ = m.invokeAction("client.pane.focus.left")
	if m.flash != "" {
		t.Fatalf("flash = %q", m.flash)
	}
	s := openedSettings(t, &fakeSettings{lock: signerLock()})
	s.flash = ""
	s, _, _, _ = s.invokeAction("client.pane.focus.left")
	if s.flash != "" {
		t.Fatalf("settings: flash = %q", s.flash)
	}
}

func TestEveryActionCommandIsABinding(t *testing.T) {
	for name, b := range actions {
		if b.name == "" || actionFor(b.name) != name {
			t.Errorf("action %q: cmd %q maps back to %q", name, b.name, actionFor(b.name))
		}
		found := false
		for screen := range screenBindings {
			found = found || screenHas(screen, b.name)
		}
		if !found {
			t.Errorf("action %q: no screen has %q", name, b.name)
		}
	}
}

func actionFor(cmd string) string {
	for name, b := range actions {
		if b.name == cmd {
			return name
		}
	}
	return ""
}
