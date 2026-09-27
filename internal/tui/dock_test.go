package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// withInteraction delivers the open session's interaction as the registry
// does, through a session event.
func withInteraction(m model, ix *session.Interaction) model {
	s := m.sessions[m.liveSessionID()]
	s.Interaction = ix
	params, _ := json.Marshal(registry.Event{Type: registry.EventUpdated, Session: s})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params}))
	return m
}

var dockPermission = &session.Interaction{
	Kind: session.InteractionPermission, ToolName: "Bash",
	Options: []session.DecisionOption{
		{Label: "Allow", Value: "allow"},
		{Label: "Deny", Value: "deny", Reject: true},
	},
}

func TestDockAppearsAndClosesWithTheInteraction(t *testing.T) {
	m := sessionModel(nil)
	if m.dockShown() {
		t.Fatal("the dock shows with no interaction")
	}
	m = withInteraction(m, dockPermission)
	if !m.dockShown() || !strings.Contains(ansi.Strip(paneView(m)), "Allow Bash?") {
		t.Fatalf("the dock should show the interaction:\n%s", ansi.Strip(paneView(m)))
	}
	m = pressKeys(m, keyMsg("tab"))
	if m.focused != sessionDock {
		t.Fatalf("<Tab>: focus = %v, want the dock", m.focused)
	}
	m = withInteraction(m, nil)
	if m.dockShown() || strings.Contains(ansi.Strip(paneView(m)), "Allow Bash?") {
		t.Error("the dock should close with the interaction")
	}
	if m.focused != mainPane {
		t.Errorf("the dock closed: focus = %v, want the main pane", m.focused)
	}
}

func TestDockStaysBelowAnOpenFile(t *testing.T) {
	m := workspaceSession(dockPermission)
	m = withFile(m, fileComp{ws: "n1:w1", path: "go.mod"})
	if !m.dockShown() {
		t.Error("the dock should stay below a file open over the session")
	}
	m.width = 200
	out := ansi.Strip(paneView(m))
	file, dock := strings.Index(out, "go.mod"), strings.Index(out, "Allow Bash?")
	if file < 0 || dock < file {
		t.Errorf("the view should draw the file, then the dock below it:\n%s", out)
	}
}

func TestDockTabTogglesFocus(t *testing.T) {
	m := sessionModel(dockPermission)
	m = pressKeys(m, keyMsg("tab"))
	if m.focused != sessionDock {
		t.Fatalf("<Tab>: focus = %v, want the dock", m.focused)
	}
	m = pressKeys(m, keyMsg("tab"))
	if m.focused != mainPane || viewOf(m) != viewSession {
		t.Fatalf("<Tab> in the dock: focus = %v view = %v, want the main pane", m.focused, viewOf(m))
	}
	m = pressKeys(sessionModel(nil), keyMsg("tab"))
	if m.focused != mainPane {
		t.Errorf("<Tab> with no interaction: focus = %v, want the main pane", m.focused)
	}
}

func TestDockTakesKeysRaw(t *testing.T) {
	m := withFocus(promptModel(&session.Interaction{Kind: session.InteractionIdle}), mainPane)
	if m.keysRaw() {
		t.Fatal("keys should go through the resolver while the dock has no focus")
	}
	m = pressKeys(m, keyMsg("tab"))
	if !m.keysRaw() {
		t.Fatal("keys should go to the focused dock as typed")
	}
	m = pressKeys(m, keyMsg("h"), tea.KeyPressMsg{Code: ' ', Text: " "}, keyMsg("i"))
	if got := m.dock.reply.Value(); got != "h i" {
		t.Errorf("the dock types the leader key as text: reply = %q, want %q", got, "h i")
	}
}

func TestPasteGoesToTheDockOnlyWithFocus(t *testing.T) {
	m := withFocus(promptModel(&session.Interaction{Kind: session.InteractionIdle}), mainPane)
	m, _ = upd(m, tea.PasteMsg{Content: "hello"})
	if got := m.dock.reply.Value(); got != "" {
		t.Fatalf("a paste with the main pane focused reached the dock: %q", got)
	}
	m = pressKeys(m, keyMsg("tab"))
	m, _ = upd(m, tea.PasteMsg{Content: "hello"})
	if got := m.dock.reply.Value(); got != "hello" {
		t.Errorf("a paste into the focused dock = %q, want hello", got)
	}

	d := withFocus(promptModel(dockPermission), mainPane)
	d = pressKeys(d, keyMsg("tab"), tea.KeyPressMsg{Code: tea.KeyDown})
	d, _ = upd(d, tea.PasteMsg{Content: "why"})
	if got := d.dock.reason.Value(); got != "why" {
		t.Errorf("a paste into the deny reason = %q, want why", got)
	}
}

func TestDockDraftSurvivesLeavingTheSession(t *testing.T) {
	m := withFocus(promptModel(&session.Interaction{Kind: session.InteractionIdle}), mainPane)
	m.sessions["s2"] = session.Session{ID: "s2"}
	m = pressKeys(m, keyMsg("tab"))
	m = typeKeys(m, "draft")
	m = pressKeys(m, keyMsg("tab"), keyMsg("esc"))
	if viewOf(m) == viewSession {
		t.Fatal("<Esc> should leave the session")
	}
	m, _ = m.enterSession("s2")
	if got := m.dock.reply.Value(); got != "" {
		t.Fatalf("another session shows the draft: %q", got)
	}
	m, _ = m.enterSession("s1")
	if got := m.dock.reply.Value(); got != "draft" {
		t.Errorf("reopened session draft = %q, want draft", got)
	}
}

func TestDockKeyAfterTheInteractionEndsKeepsTheSession(t *testing.T) {
	m := promptModel(dockPermission)
	s := m.sessions["s1"]
	s.Interaction = nil
	m.sessions["s1"] = s
	before := append(backStack(nil), m.main...)
	res, _ := m.runKey(keyMsg("x"))
	m = res.(model)
	if len(m.main) != len(before) || viewOf(m) != viewSession {
		t.Errorf("a key in the dock after the interaction ended changed the stack: %v, was %v", m.main, before)
	}
}

func TestDockFocusWaitsUnderTheLiveScreen(t *testing.T) {
	m := workspaceSession(dockPermission)
	s := m.sessions["n1:s1"]
	s.CanOpenTerminal = true
	m.sessions["n1:s1"] = s
	m = pressKeys(m, keyMsg("tab"), ctrlKey('t'))
	assertView(t, "<C-t>", m, viewScreen)
	m = pressKeys(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	assertView(t, "^]", m, viewSession)
	if m.focused != sessionDock {
		t.Errorf("back from the live screen: focus = %v, want the dock", m.focused)
	}
}
