package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
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
	if !m.dockShown() {
		t.Fatal("the region shows with no interaction")
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
	if !m.dockShown() || strings.Contains(ansi.Strip(paneView(m)), "Allow Bash?") {
		t.Error("the prompt should leave the region with the interaction")
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
	if m.focused != sessionDock {
		t.Errorf("<Tab> with no interaction: focus = %v, want the dock", m.focused)
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

func TestRegionShowsWithoutAnInteraction(t *testing.T) {
	m := sessionModel(nil)
	if !m.dockShown() {
		t.Fatal("the region should show in a live session with nothing pending")
	}
	out := ansi.Strip(paneView(m))
	if !strings.Contains(out, "Tab to type") {
		t.Fatalf("the unfocused empty input should hint Tab:\n%s", out)
	}
	if _, h := m.sessionLayout(); h != 4 {
		t.Fatalf("unfocused empty region height = %d, want 4 (margin, rule, input, rule)", h)
	}
}

func TestRegionHasATopMargin(t *testing.T) {
	m := sessionModel(nil)
	lines := strings.Split(ansi.Strip(paneView(m)), "\n")
	for i, l := range lines {
		if strings.Contains(l, "Tab to type") {
			if i < 2 || strings.TrimSpace(lines[i-2]) != "" || !strings.Contains(lines[i-1], "──") {
				t.Fatalf("want a blank line above the top rule:\n%s", strings.Join(lines[max(0, i-3):i+1], "\n"))
			}
			return
		}
	}
	t.Fatal("input not found")
}

func TestTabFocusesTheInputAndTyping(t *testing.T) {
	m := sessionModel(nil)
	m = pressKeys(m, keyMsg("tab"))
	if m.focused != sessionDock {
		t.Fatalf("<Tab>: focus = %v, want the region", m.focused)
	}
	m = pressKeys(m, keyMsg("h"), keyMsg("i"))
	if got := m.dock.reply.Value(); got != "hi" {
		t.Fatalf("typed %q, want %q", got, "hi")
	}
	m = pressKeys(m, keyMsg("esc"))
	if m.focused != mainPane {
		t.Fatalf("<Esc>: focus = %v, want the transcript", m.focused)
	}
	if !strings.Contains(ansi.Strip(paneView(m)), promptMarker+"hi") {
		t.Fatalf("the unfocused input should show the draft:\n%s", ansi.Strip(paneView(m)))
	}
}

func TestRegionHidesWhenTheSessionIsGone(t *testing.T) {
	m := sessionModel(nil)
	m = pressKeys(m, keyMsg("tab"))
	s := m.sessions["s1"]
	params, _ := json.Marshal(registry.Event{Type: registry.EventRemoved, Session: s})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params}))
	if m.dockShown() {
		t.Fatal("the region should hide once the session is gone")
	}
	if m.focused != mainPane {
		t.Fatalf("focus = %v, want the transcript", m.focused)
	}
}

func TestPromptReplacesTheInputAndTheDraftReturns(t *testing.T) {
	m := sessionModel(nil)
	m = pressKeys(m, keyMsg("tab"), keyMsg("h"), keyMsg("i"), keyMsg("esc"))
	m = withInteraction(m, dockPermission)
	out := ansi.Strip(paneView(m))
	if !strings.Contains(out, "Allow Bash?") || strings.Contains(out, promptMarker+"hi") {
		t.Fatalf("the prompt should take the input's place:\n%s", out)
	}
	m = withInteraction(m, nil)
	if !strings.Contains(ansi.Strip(paneView(m)), promptMarker+"hi") {
		t.Fatalf("the draft should return with the input:\n%s", ansi.Strip(paneView(m)))
	}
}

func TestRegionSitsAtTheBottomUnderAShortTranscript(t *testing.T) {
	m := sessionModel(nil)
	m.height = 60
	l := m.layout()
	lines := strings.Split(ansi.Strip(m.mainColumn(&ctx{m: &m}, l)), "\n")
	if len(lines) != m.sessionRows() {
		t.Fatalf("main column is %d lines, want the full %d", len(lines), m.sessionRows())
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "──") {
		t.Fatalf("the bottom rule should be the last line, got %q", last)
	}
}

func TestInputHasNoLineHighlight(t *testing.T) {
	m := sessionModel(nil)
	m = pressKeys(m, keyMsg("tab"))
	m = typeKeys(m, "hi")
	if strings.Contains(ansi.Strip(m.dock.reply.View()), "❯") {
		t.Fatalf("the marker should not be inside the textarea: %q", ansi.Strip(m.dock.reply.View()))
	}
	lines, _, _ := m.dock.idleLines()
	if got := ansi.Strip(lines[0]); !strings.HasPrefix(got, promptMarker+"hi") {
		t.Fatalf("first input line = %q, want it to start with %q", got, promptMarker+"hi")
	}
	if w, want := lipgloss.Width(lines[0]), m.dockWidth(); w != want {
		t.Fatalf("input line is %d wide, want the region's full %d", w, want)
	}
	st := m.dock.reply.Styles()
	if st.Focused.CursorLine.GetBackground() != st.Focused.Text.GetBackground() {
		t.Fatal("the current line should not be highlighted")
	}
}

func TestInputMarkerSitsAtTheRegionsLeftEdge(t *testing.T) {
	ruleCol := func(view string) int {
		for _, l := range strings.Split(ansi.Strip(view), "\n") {
			if i := strings.Index(l, "──"); i >= 0 {
				return lipgloss.Width(l[:i])
			}
		}
		return -1
	}
	markCol := func(view string) int {
		for _, l := range strings.Split(ansi.Strip(view), "\n") {
			if i := strings.Index(l, "❯"); i >= 0 {
				return lipgloss.Width(l[:i])
			}
		}
		return -2
	}
	m := sessionModel(nil)
	m.height = 30
	m = pressKeys(m, keyMsg("tab"))
	m = typeKeys(m, "hi")
	region := func(m model) string {
		l := m.layout()
		out := m.mainColumn(&ctx{m: &m}, l)
		lines := strings.Split(out, "\n")
		_, h := m.sessionLayout()
		return strings.Join(lines[len(lines)-h:], "\n")
	}
	if r := region(m); markCol(r) != ruleCol(r) {
		t.Fatalf("focused: marker at column %d, rules at %d:\n%s", markCol(r), ruleCol(r), ansi.Strip(r))
	}
	m = pressKeys(m, keyMsg("esc"))
	if r := region(m); markCol(r) != ruleCol(r) || !strings.Contains(ansi.Strip(r), promptMarker+"hi") {
		t.Fatalf("unfocused: marker at column %d, rules at %d:\n%s", markCol(r), ruleCol(r), ansi.Strip(r))
	}
}

func TestTabHintSitsAtTheRightOfTheEmptyInput(t *testing.T) {
	m := sessionModel(nil)
	lines := m.dock.composeLines(&ctx{m: &m})
	if len(lines) != 1 {
		t.Fatalf("empty input is %d lines, want 1", len(lines))
	}
	got := ansi.Strip(lines[0])
	if !strings.HasPrefix(got, promptMarker) || !strings.HasSuffix(got, "⇥ Tab to type") || lipgloss.Width(got) != m.dockWidth() {
		t.Fatalf("empty input = %q (width %d), want the marker left and the hint at the right edge of %d", got, lipgloss.Width(got), m.dockWidth())
	}
}

func TestEnterWhileWorkingKeepsTheDraft(t *testing.T) {
	m := sessionModel(nil)
	rc := &recordingClient{}
	m.client = rc
	s := m.sessions["s1"]
	s.Status = session.StatusWorking
	m.sessions["s1"] = s
	m = pressKeys(m, keyMsg("tab"), keyMsg("h"), keyMsg("i"), keyMsg("enter"))
	if m.dock.reply.Value() != "hi" || m.focused != sessionDock {
		t.Fatalf("Enter while working: draft %q focus %v, want the draft kept in focus", m.dock.reply.Value(), m.focused)
	}
	if rc.count(api.MethodSessionInput) != 0 {
		t.Fatal("a prompt was sent while working")
	}
	if m.flash != "wait for idle to send" {
		t.Fatalf("flash = %q", m.flash)
	}
	m.width = 120
	if strings.Contains(ansi.Strip(m.currentFooter()), "submit") {
		t.Fatalf("the footer offers submit while sending is blocked: %q", ansi.Strip(m.currentFooter()))
	}
}

func TestAPromptArrivingMidDraftTakesNoKeys(t *testing.T) {
	m := sessionModel(nil)
	rc := &recordingClient{}
	m.client = rc
	m = pressKeys(m, keyMsg("tab"), keyMsg("h"), keyMsg("i"))
	m = withInteraction(m, dockPermission)
	if m.focused != mainPane {
		t.Fatalf("focus = %v, want the transcript once a prompt takes the input's place", m.focused)
	}
	m = pressKeys(m, keyMsg("enter"))
	if rc.count(api.MethodSessionRespond) != 0 {
		t.Fatal("Enter typed for the draft answered the prompt")
	}
	m = withInteraction(m, nil)
	if m.dock.reply.Value() != "hi" {
		t.Fatalf("draft = %q, want it kept", m.dock.reply.Value())
	}
}

func TestTranscriptOffersAnswerOnlyForAPendingPrompt(t *testing.T) {
	has := func(m model) bool {
		for _, b := range m.focusedComp().footer(&ctx{m: &m}) {
			if b.name == transcriptKeys.Answer.name {
				return true
			}
		}
		return false
	}
	m := withFocus(sessionModel(nil), mainPane)
	if has(withInteraction(m, &session.Interaction{Kind: session.InteractionIdle})) {
		t.Error("an idle interaction shows the input; the transcript must not offer answer")
	}
	if !has(withInteraction(m, dockPermission)) {
		t.Error("a permission prompt must offer answer")
	}
}

func TestCtrlArrowsMoveByWordInTheInput(t *testing.T) {
	m := sessionModel(nil)
	m = pressKeys(m, keyMsg("tab"))
	m = typeKeys(m, "foo bar")
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl}, keyMsg("X"))
	if got := m.dock.reply.Value(); got != "foo Xbar" {
		t.Fatalf("<C-Left> then X: %q, want %q", got, "foo Xbar")
	}
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}, keyMsg("Y"))
	if got := m.dock.reply.Value(); got != "foo XbarY" {
		t.Fatalf("<C-Right> then Y: %q, want %q", got, "foo XbarY")
	}
}

func TestCtrlArrowsMoveByWordInTheSpawnPrompt(t *testing.T) {
	ta := newSpawnPromptArea()
	ta.Focus()
	ta.SetValue("foo bar")
	ta, _ = ta.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl})
	ta, _ = ta.Update(keyMsg("X"))
	if got := ta.Value(); got != "foo Xbar" {
		t.Fatalf("<C-Left> then X: %q, want %q", got, "foo Xbar")
	}
}

func TestTranscriptAndInputSpanAWideTerminal(t *testing.T) {
	m := sessionModel(nil)
	m.width = 260
	if got, want := m.containerWidth(), m.bodyWidth(); got != want {
		t.Fatalf("content column = %d, want the full body width %d", got, want)
	}
	// The input's bottom rule is the view's last rule; it spans the content column.
	lines := strings.Split(ansi.Strip(paneView(m)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if j := strings.Index(l, "──"); j >= 0 {
			if w := lipgloss.Width(strings.TrimRight(l, " ")) - lipgloss.Width(l[:j]); w != m.dockWidth() {
				t.Fatalf("input rule spans %d columns, want %d", w, m.dockWidth())
			}
			return
		}
	}
	t.Fatal("no rule found")
}
