package tui

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

// invokeOK runs a built-in action and reads ok from its result.
func invokeOK(m model, name string) (model, bool, error) {
	after, _, err := m.invokeAction(name)
	if err != nil {
		return after, false, err
	}
	return after, actions[name].changed(m, after), nil
}

func TestInvokeFocusActionReportsMove(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('l')...)
	m, ok, err := invokeOK(m, "client.pane.focus.left")
	if err != nil || !ok || m.focused != leftSidebar {
		t.Fatalf("left from main: ok=%v err=%v focus=%v", ok, err, m.focused)
	}
	m, ok, err = invokeOK(m, "client.pane.focus.left")
	if err != nil || ok || m.focused != leftSidebar {
		t.Fatalf("left at the edge: ok=%v err=%v focus=%v", ok, err, m.focused)
	}
}

func TestInvokeFocusUpNeverMoves(t *testing.T) {
	m := withFocus(wideWorkspace(), mainPane)
	m, ok, err := invokeOK(m, "client.pane.focus.up")
	if err != nil || ok || m.focused != mainPane {
		t.Fatalf("up: ok=%v err=%v focus=%v", ok, err, m.focused)
	}
}

func TestInvokeFocusDownMovesToDock(t *testing.T) {
	m := withFocus(workspaceSession(&session.Interaction{Kind: session.InteractionPermission}), mainPane)
	m, ok, err := invokeOK(m, "client.pane.focus.down")
	if err != nil || !ok || m.focused != sessionDock {
		t.Fatalf("down: ok=%v err=%v focus=%v", ok, err, m.focused)
	}
}

func TestInvokeUnavailableActionErrors(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: signerLock()})
	focused := m.focused
	m, _, err := m.invokeAction("client.pane.focus.left")
	if err == nil || !strings.Contains(err.Error(), "not available") || m.focused != focused {
		t.Fatalf("settings: err=%v focus=%v", err, m.focused)
	}
}

func TestInvokeUnknownActionErrors(t *testing.T) {
	if _, _, err := wideWorkspace().invokeAction("client.pane.focus.nowhere"); err == nil {
		t.Fatal("unknown action: err = nil")
	}
}

func TestInvokeDoesNotFlash(t *testing.T) {
	m := pressKeys(wideWorkspace(), cw('l')...)
	m, _, _ = m.invokeAction("client.pane.focus.left")
	m, _, _ = m.invokeAction("client.pane.focus.left")
	if m.flash != "" {
		t.Fatalf("flash = %q", m.flash)
	}
	s := openedSettings(t, &fakeSettings{lock: signerLock()})
	s.flash = ""
	s, _, _ = s.invokeAction("client.pane.focus.left")
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

func TestInvokeTabActionsReportChange(t *testing.T) {
	cases := []struct {
		name string
		m    func() model
	}{
		{"home", homeTestModel},
		{"settings", func() model { return openedSettings(t, &fakeSettings{lock: signerLock()}) }},
		{"left sidebar", func() model { return withFocus(wideWorkspace(), leftSidebar) }},
		{"two questions", func() model {
			return promptModel(&session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
				{Question: "One", Options: []string{"A", "B"}}, {Question: "Two", Options: []string{"A", "B"}},
			}})
		}},
	}
	for _, c := range cases {
		for _, action := range []string{"client.pane.tab.next", "client.pane.tab.previous"} {
			m := c.m()
			if action == "client.pane.tab.previous" && c.name == "two questions" {
				m, _, _ = m.invokeAction("client.pane.tab.next")
			}
			before := tabState(m)
			m, ok, err := invokeOK(m, action)
			if err != nil || !ok || tabState(m) == before {
				t.Errorf("%s %s: ok=%v err=%v tab %v → %v", c.name, action, ok, err, before, tabState(m))
			}
		}
	}
}

func TestInvokeTabActionWithOneTabIsNotDone(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Question: "One", Options: []string{"A", "B"}},
	}})
	for _, action := range []string{"client.pane.tab.next", "client.pane.tab.previous"} {
		if _, ok, err := invokeOK(m, action); err != nil || ok {
			t.Errorf("%s: ok=%v err=%v", action, ok, err)
		}
	}
}
