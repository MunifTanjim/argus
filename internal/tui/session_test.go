package tui

import (
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// recordingClient records Call method names; all calls succeed with zero results.
type recordingClient struct {
	mu    sync.Mutex
	calls []string
}

func (c *recordingClient) Call(method string, _, _ any) error {
	c.mu.Lock()
	c.calls = append(c.calls, method)
	c.mu.Unlock()
	return nil
}
func (c *recordingClient) Events() <-chan api.Notification { return make(chan api.Notification) }
func (c *recordingClient) States() <-chan bool             { return make(chan bool) }
func (c *recordingClient) Reconnect()                      {}
func (c *recordingClient) Close() error                    { return nil }

func (c *recordingClient) calledMethods() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.calls))
	copy(out, c.calls)
	return out
}

// runCmd runs a tea.Cmd synchronously, recursing into tea.BatchMsg, so tests can
// assert on the side-effects (e.g. client Call invocations) it triggers.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmd(c)
		}
	}
}

func sessionModel(ix *session.Interaction) model {
	m := testModel()
	m.sessions = map[string]session.Session{
		"s1": {ID: "s1", Status: session.StatusAwaitingInput, Interaction: ix, Tmux: session.TmuxLocation{PaneID: "%0"}},
	}
	m.selectedID = "s1"
	m.mode = modeSession
	m.focus, m.historyView = focusHistory, histTranscript
	m.transcript.chunks = sampleChunks()
	if ix != nil && ix.Kind == session.InteractionQuestion {
		m.ensurePromptState(len(ix.Questions))
	}
	return m
}

func TestSessionTabTogglesFocusOnlyWhenPending(t *testing.T) {
	m := sessionModel(nil)
	res, _ := m.handleSessionKey(tea.KeyPressMsg{Code: '\t'})
	m = res.(model)
	if m.focus != focusHistory {
		t.Fatalf("no pending: focus=%v want history", m.focus)
	}
	m = sessionModel(&session.Interaction{Kind: session.InteractionPermission})
	res, _ = m.handleSessionKey(tea.KeyPressMsg{Code: '\t'})
	m = res.(model)
	if m.focus != focusDock {
		t.Fatalf("pending: focus=%v want dock", m.focus)
	}
	res, _ = m.handleSessionKey(tea.KeyPressMsg{Code: '\t'})
	m = res.(model)
	if m.focus != focusHistory {
		t.Fatalf("toggle back: focus=%v want history", m.focus)
	}
}

func TestSessionEscIsContextual(t *testing.T) {
	m := sessionModel(&session.Interaction{Kind: session.InteractionPermission})
	m.historyView = histDetail
	res, _ := m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = res.(model)
	if m.historyView != histTranscript || m.mode != modeSession {
		t.Fatalf("detail esc: view=%v mode=%v", m.historyView, m.mode)
	}
	res, _ = m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = res.(model)
	if m.mode != modeList {
		t.Fatalf("transcript esc: mode=%v want list", m.mode)
	}
	m = sessionModel(&session.Interaction{Kind: session.InteractionPermission})
	m.focus = focusDock
	res, _ = m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = res.(model)
	if m.focus != focusHistory || m.mode != modeSession {
		t.Fatalf("dock esc: focus=%v mode=%v", m.focus, m.mode)
	}
}

func TestSessionDockFocusResetsWhenNotPending(t *testing.T) {
	m := sessionModel(nil)
	m.focus = focusDock
	res, _ := m.handleSessionKey(tea.KeyPressMsg{Code: 'j'})
	m = res.(model)
	if m.focus != focusHistory {
		t.Fatalf("focus not reset: %v", m.focus)
	}
}

func TestSessionDockShownWhenPending(t *testing.T) {
	// Unfocused, the dock collapses to a one-line summary ("Allow <tool>?").
	m := sessionModel(&session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"})
	out := ansi.Strip(m.sessionView())
	if !strings.Contains(out, "Allow Bash?") {
		t.Errorf("collapsed dock not rendered:\n%s", out)
	}
	m = sessionModel(nil)
	if strings.Contains(ansi.Strip(m.sessionView()), "Allow Bash?") {
		t.Error("dock shown without a pending interaction")
	}
}

func TestSessionSubmitReturnsFocusToHistory(t *testing.T) {
	m := sessionModel(&session.Interaction{
		Kind: session.InteractionPermission,
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true},
		},
	})
	m.focus = focusDock
	res, cmd := m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.focus != focusHistory || cmd == nil {
		t.Errorf("after submit: focus=%v cmd=%v", m.focus, cmd)
	}
}

func TestSessionFooterReflectsFocus(t *testing.T) {
	// Footers render via the help bubble (per-token ANSI, truncated to width); use a
	// wide viewport and strip ANSI so the hint words are assertable.
	foot := func(m model) string { m.width = 120; return ansi.Strip(m.sessionFooter()) }

	m := sessionModel(&session.Interaction{Kind: session.InteractionPermission})
	if !strings.Contains(foot(m), "answer") {
		t.Errorf("history footer should hint answering: %q", foot(m))
	}
	m.focus = focusDock
	if !strings.Contains(foot(m), "submit") {
		t.Errorf("dock footer should hint submit: %q", foot(m))
	}
	m2 := sessionModel(nil)
	if strings.Contains(foot(m2), "answer") {
		t.Errorf("no-interaction footer should not hint answering: %q", foot(m2))
	}
}

func TestSessionLayoutSumsToViewport(t *testing.T) {
	m := sessionModel(&session.Interaction{Kind: session.InteractionPermission})
	m.height = 30
	h, d := m.sessionLayout()
	if d == 0 {
		t.Fatal("dock height 0 with pending interaction")
	}
	// history + dock + chrome(4) + history/dock join(1) == viewport.
	if h+d != max(1, m.height-5) {
		t.Errorf("history(%d)+dock(%d) != %d", h, d, m.height-5)
	}
	m = sessionModel(nil)
	m.height = 30
	h, d = m.sessionLayout()
	if d != 0 || h != max(1, m.height-4) {
		t.Errorf("no-dock layout: h=%d d=%d want h=%d", h, d, m.height-4)
	}
}

// TestSessionViewFitsViewport guards the composed screen against overflow: the
// rendered height must never exceed the terminal height, with or without a dock.
func TestSessionViewFitsViewport(t *testing.T) {
	for _, ix := range []*session.Interaction{
		nil,
		{Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: strings.Repeat("x\n", 40)},
	} {
		m := sessionModel(ix)
		m.height, m.width = 24, 80
		got := strings.Count(m.sessionView(), "\n") + 1
		if got > m.height {
			t.Errorf("sessionView rendered %d lines > height %d (ix=%v)", got, m.height, ix)
		}
	}
}

// TestTallDockKeepsSelectedControlVisible reproduces the reported bug: many
// options with long descriptions make the dock taller than its cap; the selected
// control at the bottom must still render (it used to get clipped off).
func TestTallDockKeepsSelectedControlVisible(t *testing.T) {
	desc := strings.Repeat("a long description that wraps across lines ", 3)
	m := sessionModel(&session.Interaction{
		Kind: session.InteractionQuestion,
		Questions: []session.QuestionSpec{{
			Question:           "Pick one",
			Options:            []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo"},
			OptionDescriptions: []string{desc, desc, desc, desc, desc},
		}},
	})
	m.height, m.width = 24, 80
	m.focus = focusDock
	m.prompt.sel[0] = 4 // "Echo", the last real option

	out := m.sessionView()
	if !strings.Contains(out, "Echo") {
		t.Errorf("selected option clipped out of tall dock:\n%s", out)
	}
	if strings.Count(out, "\n")+1 > m.height {
		t.Errorf("tall dock overflowed viewport")
	}
}

// TestOptionPreviewRendersSideBySide checks the focused option's preview shows in
// the dock, swaps as the selection moves, and is absent for multi-select.
func TestOptionPreviewRendersSideBySide(t *testing.T) {
	q := &session.Interaction{
		Kind: session.InteractionQuestion,
		Questions: []session.QuestionSpec{{
			Question:       "Pick a layout",
			Options:        []string{"Sidebar", "Topbar"},
			OptionPreviews: []string{"SIDEBAR_MOCKUP", ""},
		}},
	}
	m := sessionModel(q)
	m.height, m.width = 30, 100
	m.focus = focusDock

	// Focused option has a preview → it renders, and the view still fits.
	m.prompt.sel[0] = 0
	out := m.sessionView()
	if !strings.Contains(out, "SIDEBAR_MOCKUP") {
		t.Errorf("focused preview not rendered:\n%s", out)
	}
	if !strings.Contains(out, "Sidebar") {
		t.Errorf("option label missing alongside preview:\n%s", out)
	}
	if got := strings.Count(out, "\n") + 1; got > m.height {
		t.Errorf("side-by-side dock overflowed: %d > %d", got, m.height)
	}

	// Moving to an option without a preview drops the preview pane.
	m.prompt.sel[0] = 1
	if strings.Contains(m.sessionView(), "SIDEBAR_MOCKUP") {
		t.Error("preview should vanish when the focused option has none")
	}

	// Multi-select never shows previews.
	mq := &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{{
		Question:       "Pick a layout",
		MultiSelect:    true,
		Options:        []string{"Sidebar", "Topbar"},
		OptionPreviews: []string{"SIDEBAR_MOCKUP", ""},
	}}}
	m = sessionModel(mq)
	m.height, m.width = 30, 100
	m.prompt.sel[0] = 0
	if strings.Contains(m.sessionView(), "SIDEBAR_MOCKUP") {
		t.Error("multi-select should not show previews")
	}
}

// TestDockRuleSpansContentWidth guards against the dock separator regressing to a
// fixed short width: it should span the full content column.
func TestDockRuleSpansContentWidth(t *testing.T) {
	m := sessionModel(&session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"})
	m.height, m.width = 24, 80
	want := m.containerWidth()

	var ruleWidth int
	for _, line := range strings.Split(m.sessionView(), "\n") {
		if n := strings.Count(line, "─"); n > ruleWidth {
			ruleWidth = n
		}
	}
	if ruleWidth != want {
		t.Errorf("dock rule spans %d cols, want %d (containerWidth)", ruleWidth, want)
	}
}

func TestWindowLinesKeepsAnchorVisible(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4", "5", "6", "7"}
	s := strings.Join(lines, "\n")

	// Anchor near the top: window starts at the top (heading stays visible).
	if got := windowLines(s, 3, 1); got != "0\n1\n2" {
		t.Errorf("top anchor: got %q", got)
	}
	// Anchor past the window: slides down so the anchor is the last visible line.
	if got := windowLines(s, 3, 6); got != "4\n5\n6" {
		t.Errorf("mid anchor: got %q", got)
	}
	// Anchor at the very end: clamps to the last full window.
	if got := windowLines(s, 3, 7); got != "5\n6\n7" {
		t.Errorf("end anchor: got %q", got)
	}
	// Fits entirely: returned unchanged.
	if got := windowLines(s, 10, 7); got != s {
		t.Errorf("fits: got %q", got)
	}
}

func TestFocusedDockExpandsToShowSubmitTab(t *testing.T) {
	ix := &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Header: "Database", Question: "Which DB?", Options: []string{"Postgres", "SQLite"}},
		{Header: "Cache", Question: "Which cache?", Options: []string{"Redis", "Memory"}},
		{Header: "Auth", Question: "Which auth?", Options: []string{"OAuth", "JWT"}},
	}}
	m := sessionModel(ix)
	m.height, m.width = 24, 80
	m.focus = focusDock
	m.prompt.tab = m.numQuestions() // Submit tab

	out := m.sessionView()
	if !strings.Contains(out, "Submit") || !strings.Contains(out, "Cancel") {
		t.Errorf("focused submit tab should show both actions:\n%s", out)
	}
	if got := strings.Count(out, "\n") + 1; got > m.height {
		t.Errorf("view overflowed: %d > %d", got, m.height)
	}

	// The dock is larger when focused than when reading (compact cap).
	_, dockFocused := m.sessionLayout()
	reading := m
	reading.focus = focusHistory
	if _, dockReading := reading.sessionLayout(); dockReading >= dockFocused {
		t.Errorf("dock should be smaller when reading (%d) than focused (%d)", dockReading, dockFocused)
	}
}

func TestDetailEscPopsThenLeaves(t *testing.T) {
	sub := claudecode.Item{Kind: claudecode.ItemSubagent, SubagentType: "explorer", HasTrace: true,
		Trace: []claudecode.Chunk{{Kind: claudecode.ChunkAI, Items: []claudecode.Item{
			{Kind: claudecode.ItemTool, ToolName: "Read"}}}}}
	m := sessionModel(nil)
	m.transcript.chunks = []claudecode.Chunk{{ID: "a", Kind: claudecode.ChunkAI,
		Items: []claudecode.Item{sub}}}
	m.transcript.cursor = 0
	m.historyView = histDetail
	m.enterDetail()
	m.topFrame().cursor = 0
	m.drillDetail() // now 2 frames deep (inline history trace → subagent frame)

	// First esc pops to the root frame, staying in detail.
	res, _ := m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = res.(model)
	if m.historyView != histDetail || len(m.transcript.detailStack) != 1 {
		t.Fatalf("first esc: view=%v frames=%d", m.historyView, len(m.transcript.detailStack))
	}
	// Second esc leaves detail for the transcript.
	res, _ = m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = res.(model)
	if m.historyView != histTranscript {
		t.Fatalf("second esc: view=%v want transcript", m.historyView)
	}
}

// TestSubagentLeafBackDoesNotTearDownSubscription (regression, "Finding 1"): from
// 3 frames deep (root → subagent → leaf), the first Back must pop only the leaf
// without touching the subagent subscription; only the second Back unsubscribes
// the subagent and restores the session stream.
func TestSubagentLeafBackDoesNotTearDownSubscription(t *testing.T) {
	rc := &recordingClient{}

	// Build a session model with a recording client.
	m := sessionModel(nil)
	m.client = rc
	m.selectedID = "s1"
	m.sessions = map[string]session.Session{
		"s1": {ID: "s1", Status: session.StatusIdle, Tmux: session.TmuxLocation{PaneID: "%0"}},
	}
	m.transcriptCache = map[string]cachedTranscript{}

	// Populate a transcript chunk with a live subagent item (no inlined trace: it
	// will be streamed).
	agentItem := claudecode.Item{
		Kind: claudecode.ItemSubagent, SubagentType: "explorer",
		HasTrace: true, AgentID: "agent42",
	}
	m.transcript.chunks = []claudecode.Chunk{
		{ID: "a", Kind: claudecode.ChunkAI, Items: []claudecode.Item{agentItem}},
	}
	m.transcript.cursor = 0
	m.historyView = histDetail
	m.mode = modeSession
	m.focus = focusHistory
	m.enterDetail()

	// Stash a session subRef (as if we had opened the session stream).
	sessSubID := "sess-sub-1"
	m.activeSub = subRef{subID: sessSubID, sessionID: "s1"}

	// Simulate drilling into the live subagent: actDetailDrill stashes sessionSub,
	// sets activeSub to the new subagent ref, and pushes a frame with subID set.
	subAgentSubID := "sub-agent-sub-1"
	m.sessionSub = m.activeSub
	m.activeSub = subRef{subID: subAgentSubID, sessionID: "s1", agentID: "agent42"}
	m.transcript.detailStack = append(m.transcript.detailStack, detailFrame{
		label:    "explorer",
		subID:    subAgentSubID, // this is what Finding 1 requires to be set
		expanded: map[int]bool{},
		items: []claudecode.Item{
			{Kind: claudecode.ItemTool, ToolName: "Read"},
		},
	})
	// Stack is now 2 deep: root + subagent.
	if len(m.transcript.detailStack) != 2 {
		t.Fatalf("setup: want 2 frames, got %d", len(m.transcript.detailStack))
	}

	// Drill into a leaf item inside the subagent frame (drillDetail pushes a focus frame).
	m.drillDetail()
	if len(m.transcript.detailStack) != 3 {
		t.Fatalf("after leaf drill: want 3 frames, got %d", len(m.transcript.detailStack))
	}
	if m.topFrame().subID != "" {
		t.Fatalf("leaf frame should have empty subID, got %q", m.topFrame().subID)
	}

	// === First Back (from the leaf) ===
	// Must pop only the leaf. The subagent subscription must NOT be torn down.
	res, cmd := m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = res.(model)
	runCmd(cmd)

	if len(m.transcript.detailStack) != 2 {
		t.Fatalf("first Back: want 2 frames, got %d", len(m.transcript.detailStack))
	}
	if m.topFrame().subID != subAgentSubID {
		t.Fatalf("first Back: subagent frame subID should still be %q, got %q",
			subAgentSubID, m.topFrame().subID)
	}
	if m.activeSub.subID != subAgentSubID {
		t.Fatalf("first Back: activeSub should still be the subagent %q, got %q",
			subAgentSubID, m.activeSub.subID)
	}
	// No unsubscribe or subscribe calls should have been made yet.
	for _, method := range rc.calledMethods() {
		if method == api.MethodTranscriptUnsubscribe || method == api.MethodTranscriptSubscribe {
			t.Fatalf("first Back: unexpected RPC call %q — leaf Back must not touch subscriptions", method)
		}
	}
	rc.calls = nil // reset for second Back

	// === Second Back (from the subagent frame) ===
	// Must unsubscribe the subagent and re-subscribe the session.
	res, cmd = m.handleSessionKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = res.(model)
	runCmd(cmd)

	if len(m.transcript.detailStack) != 1 {
		t.Fatalf("second Back: want 1 frame, got %d", len(m.transcript.detailStack))
	}
	if m.activeSub.subID != sessSubID {
		t.Fatalf("second Back: activeSub should be restored to session %q, got %q",
			sessSubID, m.activeSub.subID)
	}
	if m.sessionSub.subID != "" {
		t.Fatalf("second Back: sessionSub stash should be cleared, got %q", m.sessionSub.subID)
	}

	// The second Back must issue both an unsubscribe and a re-subscribe.
	methods := rc.calledMethods()
	var sawUnsub, sawSub bool
	for _, method := range methods {
		if method == api.MethodTranscriptUnsubscribe {
			sawUnsub = true
		}
		if method == api.MethodTranscriptSubscribe {
			sawSub = true
		}
	}
	if !sawUnsub {
		t.Errorf("second Back: expected transcript.unsubscribe call, got %v", methods)
	}
	if !sawSub {
		t.Errorf("second Back: expected transcript.subscribe call, got %v", methods)
	}
}

func TestScreenViewHasBorderAndFits(t *testing.T) {
	m := testModel()
	m.sessions = map[string]session.Session{
		"s1": {ID: "s1", Status: session.StatusWorking, Tmux: session.TmuxLocation{PaneID: "%0", SessionName: "demo"}},
	}
	m.selectedID = "s1"
	m.mode = modeScreen
	m.width, m.height = 80, 24
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("\x1b[31msome colored pane line\x1b[0m\n")
	}
	m.screen = sb.String()

	out := m.screenView()
	if !strings.Contains(out, "╮") || !strings.Contains(out, "╰") {
		t.Errorf("screen view should be framed by a rounded border:\n%s", out)
	}
	if got := strings.Count(out, "\n") + 1; got > m.height {
		t.Errorf("screen view rendered %d lines > height %d", got, m.height)
	}
}

func TestDockSummary(t *testing.T) {
	cases := []struct {
		name string
		ix   *session.Interaction
		want string
	}{
		{"single question", &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{{Question: "Which option?"}}}, "Which option?"},
		{"multi question", &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{{Question: "A?"}, {Question: "B?"}}}, "2 questions"},
		{"question fallback", &session.Interaction{Kind: session.InteractionQuestion}, "Question"},
		{"permission with tool", &session.Interaction{Kind: session.InteractionPermission, ToolName: "Read"}, "Allow Read?"},
		{"permission fallback", &session.Interaction{Kind: session.InteractionPermission}, "Permission request"},
		{"plan", &session.Interaction{Kind: session.InteractionPlan}, "Review plan"},
		{"idle with message", &session.Interaction{Kind: session.InteractionIdle, Message: "done"}, "done"},
		{"idle fallback", &session.Interaction{Kind: session.InteractionIdle}, "Waiting for input"},
	}
	for _, c := range cases {
		if got := dockSummary(c.ix); got != c.want {
			t.Errorf("%s: dockSummary = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSessionLayoutCollapsesWhenUnfocused(t *testing.T) {
	m := sessionModel(&session.Interaction{Kind: session.InteractionPermission, ToolName: "Read"})

	// Unfocused (focusHistory): dock collapses to rule + one line.
	uh, ud := m.sessionLayout()
	if ud != 2 {
		t.Fatalf("unfocused dockH = %d, want 2", ud)
	}

	// Focused: dock expands to the full panel.
	m.focus = focusDock
	fh, fd := m.sessionLayout()
	if fd <= 2 {
		t.Fatalf("focused dockH = %d, want > 2 (full panel)", fd)
	}
	if uh <= fh {
		t.Fatalf("collapsed historyH (%d) should exceed focused historyH (%d)", uh, fh)
	}
}

func TestDockSummaryLineIsSingleLine(t *testing.T) {
	m := sessionModel(&session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{{Question: "Ship it?"}}})
	line := m.dockSummaryLine(m.containerWidth())
	if strings.Contains(line, "\n") {
		t.Fatalf("dockSummaryLine must be one line, got:\n%s", line)
	}
	if !strings.Contains(ansi.Strip(line), "Ship it?") {
		t.Fatalf("summary line missing question text: %q", ansi.Strip(line))
	}
}
