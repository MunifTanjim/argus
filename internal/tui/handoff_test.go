package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func handoffInteraction() *session.Interaction {
	return &session.Interaction{
		Kind: session.InteractionHandoff, RequestID: "handoff",
		Message: "the login needs your password", Steps: "1. type the password\n2. press enter",
		Options: []session.DecisionOption{{Label: "Take over", Value: "takeover"}, {Label: "Done", Value: "done"}},
	}
}

func handoffModel(canOpen bool) (model, *failingRespondClient) {
	m := promptModel(handoffInteraction())
	s := m.sessions["s1"]
	s.CanOpenTerminal = canOpen
	m.sessions["s1"] = s
	cl := &failingRespondClient{}
	m.client = cl
	return m, cl
}

func runDock(m model, msg tea.KeyPressMsg) (model, tea.Cmd) {
	res, cmd := m.runKey(msg)
	return res.(model), cmd
}

func TestHandoffDockOptions(t *testing.T) {
	m, cl := handoffModel(true)
	body := ansi.Strip(m.dock.promptBody(&ctx{m: &m}))
	for _, want := range []string{"Your turn", "the login needs your password", "type the password", "Take over", "Done"} {
		if !strings.Contains(body, want) {
			t.Errorf("dock lacks %q:\n%s", want, body)
		}
	}
	m, _ = runDock(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, cmd := runDock(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Done responds")
	}
	cl.err = errors.New("answer the agent's pending request first")
	msg := cmd()
	if p := cl.params[len(cl.params)-1].(api.RespondParams); p.Kind != "handoff" || p.RequestID != "handoff" || p.OptionValue != "done" || p.SessionID != "s1" {
		t.Fatalf("respond params = %+v", p)
	}
	m, _ = upd(m, msg)
	if !strings.Contains(m.flash, "answer the agent's pending request first") {
		t.Errorf("the node's refusal is not shown: %q", m.flash)
	}
}

func TestHandoffTakeOverOpensScreen(t *testing.T) {
	m, cl := handoffModel(true)
	m, cmd := runDock(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Take over opens the live screen")
	}
	if _, ok := m.main.top().(screenComp); !ok {
		t.Fatalf("top = %T, want the live screen", m.main.top())
	}
	for _, call := range cl.calls {
		if call == api.MethodSessionRespond {
			t.Fatal("Take over answers the node")
		}
	}
	if m.dock.decisionSel != 1 {
		t.Errorf("Done is not highlighted for the return: sel %d", m.dock.decisionSel)
	}
	m = pressKeys(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	if _, ok := m.main.top().(screenComp); ok {
		t.Fatal("^] leaves the screen")
	}
	if body := ansi.Strip(m.dock.promptBody(&ctx{m: &m})); !strings.Contains(body, "Done") {
		t.Errorf("after the screen the dock lacks Done:\n%s", body)
	}
}

func TestHandoffTakeOverUnavailableFlash(t *testing.T) {
	m, _ := handoffModel(false)
	m, cmd := runDock(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || !strings.Contains(m.flash, "terminal control unavailable") {
		t.Fatalf("flash = %q", m.flash)
	}
	if m.dock.decisionSel != 0 {
		t.Errorf("sel moved: %d", m.dock.decisionSel)
	}
}
