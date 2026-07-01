package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

func promptModel(ix *session.Interaction) model {
	m := testModel()
	m.sessions = map[string]session.Session{
		"s1": {
			ID:          "s1",
			Status:      session.StatusAwaitingInput,
			Tmux:        session.TmuxLocation{PaneID: "%1", Server: session.TmuxServerDefault},
			Frontend:    session.FrontendTmux,
			Interaction: ix,
		},
	}
	m.selectedID = "s1"
	m.mode = modeSession
	m.focus = focusDock
	if ix != nil && ix.Kind == session.InteractionQuestion {
		m.ensurePromptState(len(ix.Questions))
	}
	return m
}

// question builds a single-question interaction.
func question(q session.QuestionSpec) *session.Interaction {
	return &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{q}}
}

func TestPromptPermissionComposeThenSubmit(t *testing.T) {
	m := promptModel(&session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Bash",
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true},
		},
	})

	// Navigating to "Deny" only changes the local draft; nothing is sent.
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	if m.prompt.decisionSel != 1 || cmd != nil {
		t.Fatalf("down: sel=%d cmd=%v (nothing should be sent yet)", m.prompt.decisionSel, cmd)
	}
	// Typing fills the deny reason locally.
	res, cmd = m.handlePromptKey(tea.KeyPressMsg{Text: "x", Code: 'x'})
	m = res.(model)
	if m.prompt.reasonText != "x" || cmd != nil {
		t.Fatalf("typing reason: text=%q cmd=%v", m.prompt.reasonText, cmd)
	}
	// Only Enter submits and returns to the history.
	res, cmd = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.focus != focusHistory || cmd == nil {
		t.Errorf("submit: focus=%v cmd=%v", m.focus, cmd)
	}
}

func TestIdleDockPanelessEnterIsSilent(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	// Paneless vscode session: the idle dock is informational, not a composer.
	m.sessions["s1"] = session.Session{
		ID:          "s1",
		Status:      session.StatusAwaitingInput,
		Frontend:    session.FrontendVSCode,
		Interaction: &session.Interaction{Kind: session.InteractionIdle},
	}

	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.flash != "" {
		t.Errorf("enter on the informational idle dock must not flash, got %q", m.flash)
	}
	if cmd != nil {
		t.Errorf("enter on the informational idle dock must be a no-op, got cmd %v", cmd)
	}
}

func TestPromptQuestionSelect(t *testing.T) {
	m := promptModel(question(session.QuestionSpec{Question: "Pick", Options: []string{"A", "B"}}))
	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	if m.qSel(0) != 1 {
		t.Fatalf("sel=%d want 1", m.qSel(0))
	}
	// Single question → Enter submits directly.
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.focus != focusHistory || cmd == nil {
		t.Errorf("submit: focus=%v cmd=%v", m.focus, cmd)
	}
}

func TestPromptMultiSelectToggle(t *testing.T) {
	m := promptModel(question(session.QuestionSpec{
		Question: "Pick many", Options: []string{"A", "B", "C"}, MultiSelect: true,
	}))
	// space toggles the highlighted option without submitting.
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: ' '})
	m = res.(model)
	if !m.qToggles(0)[0] || cmd != nil {
		t.Fatalf("toggle: toggles=%v cmd=%v", m.qToggles(0), cmd)
	}
}

func TestPromptIdleTextComposeThenSubmit(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	for _, r := range "hi" {
		res, _ := m.handlePromptKey(tea.KeyPressMsg{Text: string(r), Code: r})
		m = res.(model)
	}
	if m.prompt.reasonText != "hi" {
		t.Fatalf("reasonText=%q want hi", m.prompt.reasonText)
	}
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if cmd == nil || m.focus != focusHistory || m.prompt.reasonText != "" {
		t.Errorf("idle submit: cmd=%v focus=%v text=%q", cmd, m.focus, m.prompt.reasonText)
	}
}

func TestPromptIdleShiftEnterInsertsNewline(t *testing.T) {
	// Kitty disambiguation (always on in Bubble Tea v2) makes shift+enter
	// stringify distinctly; guards the key the composer matches on.
	if got := (tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}).String(); got != "shift+enter" {
		t.Fatalf("shift+enter String() = %q, want shift+enter", got)
	}

	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	type step struct {
		msg  tea.KeyPressMsg
		want string
	}
	for _, s := range []step{
		{tea.KeyPressMsg{Text: "a", Code: 'a'}, "a"},
		{tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "a\n"},
		{tea.KeyPressMsg{Text: "b", Code: 'b'}, "a\nb"},
	} {
		res, cmd := m.handlePromptKey(s.msg)
		m = res.(model)
		if cmd != nil {
			t.Fatalf("unexpected submit on %v", s.msg)
		}
		if m.prompt.reasonText != s.want {
			t.Fatalf("reasonText=%q want %q", m.prompt.reasonText, s.want)
		}
	}
	if m.focus != focusDock {
		t.Errorf("focus moved off dock before submit: %v", m.focus)
	}

	// Plain Enter submits the whole multi-line buffer.
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if cmd == nil || m.focus != focusHistory || m.prompt.reasonText != "" {
		t.Errorf("multiline submit: cmd=%v focus=%v text=%q", cmd, m.focus, m.prompt.reasonText)
	}
}

func TestPromptIdlePasteAppendsMultiline(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	res, _ := m.Update(tea.PasteMsg{Content: "x\ny"})
	m = res.(model)
	if m.prompt.reasonText != "x\ny" {
		t.Fatalf("after paste reasonText=%q want %q", m.prompt.reasonText, "x\ny")
	}
}

func TestPromptPasteIgnoredWhenComposerInactive(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	m.focus = focusHistory // dock not focused → composer inactive
	res, _ := m.Update(tea.PasteMsg{Content: "x\ny"})
	m = res.(model)
	if m.prompt.reasonText != "" {
		t.Fatalf("paste leaked into inactive composer: %q", m.prompt.reasonText)
	}
}

func TestPromptViewRenders(t *testing.T) {
	m := promptModel(question(session.QuestionSpec{Question: "Pick one", Options: []string{"Alpha", "Beta"}}))
	out := m.promptBody()
	for _, want := range []string{"Pick one", "Alpha", "Beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("promptBody missing %q in:\n%s", want, out)
		}
	}
}

func TestSingleQuestionHeaderChip(t *testing.T) {
	m := promptModel(question(session.QuestionSpec{
		Header: "Database", Question: "Pick one", Options: []string{"A", "B"},
	}))
	if out := m.promptBody(); !strings.Contains(out, "Database") {
		t.Errorf("single-question heading should show the header chip:\n%s", out)
	}
}

func TestPreviewBoxClips(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	// height 5 → 3 inner rows; the 5-line content must clip with a "more" marker.
	out := previewBox(content, 20, 5)
	if !strings.Contains(out, "line1") {
		t.Errorf("previewBox dropped the first line:\n%s", out)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("previewBox should mark clipped content:\n%s", out)
	}
	if strings.Contains(out, "line5") {
		t.Errorf("previewBox should not render clipped lines:\n%s", out)
	}
	if got := strings.Count(out, "\n") + 1; got > 5 {
		t.Errorf("previewBox rendered %d rows > height 5", got)
	}
	wide := previewBox(strings.Repeat("x", 100), 20, 4)
	for _, line := range strings.Split(wide, "\n") {
		if lipgloss.Width(line) > 20 {
			t.Errorf("previewBox line wider than 20: %q (%d)", line, lipgloss.Width(line))
		}
	}
}

func TestQuestionOptionDescriptionsRender(t *testing.T) {
	m := promptModel(question(session.QuestionSpec{
		Question:           "Pick one",
		Options:            []string{"Alpha", "Beta"},
		OptionDescriptions: []string{"the first one", "the second one"},
	}))
	out := m.promptBody()
	for _, want := range []string{"Alpha", "the first one", "Beta", "the second one"} {
		if !strings.Contains(out, want) {
			t.Errorf("promptBody missing %q in:\n%s", want, out)
		}
	}
}

func TestQuestionCustomAnswerInlineRender(t *testing.T) {
	ix := question(session.QuestionSpec{Question: "Q", Options: []string{"A", "B"}})
	q := &ix.Questions[0]

	// Other row selected with typed text: the row itself shows the text inline.
	m := promptModel(ix)
	m.prompt.sel[0] = otherIndex(q)
	m.prompt.text[0] = "mydb"
	out := m.promptBody()
	if !strings.Contains(out, "mydb") {
		t.Errorf("inline custom: missing typed text in:\n%s", out)
	}
	if strings.Contains(out, "type your own…") {
		t.Errorf("inline custom: stale placeholder still shown in:\n%s", out)
	}

	// Other row selected with empty text: cursor shows so the field reads as active.
	m = promptModel(ix)
	m.prompt.sel[0] = otherIndex(q)
	m.prompt.text[0] = ""
	if out := m.promptBody(); !strings.Contains(out, "▏") {
		t.Errorf("inline custom (empty): missing cursor in:\n%s", out)
	}
}

func TestQuestionAnswers(t *testing.T) {
	ix := question(session.QuestionSpec{Question: "Q", Options: []string{"A", "B"}})
	q := &ix.Questions[0]

	// Committed "type your own" + typed text → custom answer value.
	m := promptModel(ix)
	m.prompt.chosen[0] = otherIndex(q)
	m.prompt.text[0] = "my custom"
	if p := m.questionAnswers(ix); p.Answers["Q"] != "my custom" {
		t.Fatalf("custom: answers=%v", p.Answers)
	}

	// Committed custom with no text → omitted (unanswered).
	m = promptModel(ix)
	m.prompt.chosen[0] = otherIndex(q)
	m.prompt.text[0] = "   "
	if p := m.questionAnswers(ix); len(p.Answers) != 0 {
		t.Errorf("empty custom should be omitted: %v", p.Answers)
	}

	// Committed predefined option.
	m = promptModel(ix)
	m.prompt.chosen[0] = 1
	if p := m.questionAnswers(ix); p.Answers["Q"] != "B" {
		t.Fatalf("predefined: answers=%v", p.Answers)
	}
}

func TestQuestionMultiSelectWithCustom(t *testing.T) {
	ix := question(session.QuestionSpec{Question: "Q", Options: []string{"A", "B"}, MultiSelect: true})
	q := &ix.Questions[0]
	m := promptModel(ix)
	m.prompt.toggles[0][0] = true             // "A"
	m.prompt.toggles[0][otherIndex(q)] = true // custom
	m.prompt.text[0] = "extra"
	p := m.questionAnswers(ix)
	got, _ := p.Answers["Q"].([]string)
	has := func(s string) bool {
		for _, v := range got {
			if v == s {
				return true
			}
		}
		return false
	}
	if !has("A") || !has("extra") {
		t.Fatalf("multi custom answers = %v, want A + extra", got)
	}
}

// -- Multi-question tabbed panel ----------------------------------------------

func multiQuestion() *session.Interaction {
	return &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Header: "Database", Question: "Which DB?", Options: []string{"Postgres", "SQLite"}},
		{Header: "Cache", Question: "Which cache?", Options: []string{"Redis", "Memory"}},
	}}
}

func TestMultiQuestionTabBarRenders(t *testing.T) {
	m := promptModel(multiQuestion())
	out := m.promptBody()
	for _, want := range []string{"Database", "Cache", "Submit"} {
		if !strings.Contains(out, want) {
			t.Errorf("tab bar missing %q in:\n%s", want, out)
		}
	}
}

func TestMultiQuestionTabNavigation(t *testing.T) {
	m := promptModel(multiQuestion())
	// right advances the tab, left goes back, both clamp.
	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyRight})
	m = res.(model)
	if m.prompt.tab != 1 {
		t.Fatalf("right: tab=%d want 1", m.prompt.tab)
	}
	res, _ = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = res.(model)
	if m.prompt.tab != 0 {
		t.Fatalf("left: tab=%d want 0", m.prompt.tab)
	}
}

func TestMultiQuestionEnterAdvancesThenSubmit(t *testing.T) {
	m := promptModel(multiQuestion())

	// Enter on Q0 commits it and focuses the next tab (does not submit).
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.prompt.tab != 1 || cmd != nil || !m.qAnswered(0) {
		t.Fatalf("after Q0 enter: tab=%d cmd=%v answered0=%v", m.prompt.tab, cmd, m.qAnswered(0))
	}

	// Pick the second option on Q1, then Enter lands on the Submit tab.
	res, _ = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	res, cmd = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if !m.onSubmitTab() || cmd != nil {
		t.Fatalf("after Q1 enter: tab=%d onSubmit=%v cmd=%v", m.prompt.tab, m.onSubmitTab(), cmd)
	}

	// The review lists both headers; Enter on Submit sends all answers.
	out := m.promptBody()
	if !strings.Contains(out, "Submit") || !strings.Contains(out, "Postgres") || !strings.Contains(out, "Memory") {
		t.Errorf("submit review missing answers:\n%s", out)
	}
	res, cmd = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if cmd == nil || m.focus != focusHistory {
		t.Errorf("submit all: cmd=%v focus=%v", cmd, m.focus)
	}
}

func TestMultiQuestionAnswersIndependent(t *testing.T) {
	ix := multiQuestion()
	m := promptModel(ix)
	m.prompt.chosen[0] = 0 // Postgres
	m.prompt.chosen[1] = 1 // Memory
	p := m.questionAnswers(ix)
	if p.Answers["Which DB?"] != "Postgres" || p.Answers["Which cache?"] != "Memory" {
		t.Fatalf("independent answers: %v", p.Answers)
	}
}

// TestNavigationDoesNotSelect verifies arrows only move the highlight; Enter commits.
func TestNavigationDoesNotSelect(t *testing.T) {
	ix := multiQuestion()
	m := promptModel(ix)

	// Move the highlight on Q0 with ↓; do NOT press Enter.
	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	if m.qSel(0) != 1 {
		t.Fatalf("highlight should move: qSel=%d", m.qSel(0))
	}
	if m.qAnswered(0) {
		t.Error("navigating must not answer the question")
	}
	if p := m.questionAnswers(ix); len(p.Answers) != 0 {
		t.Errorf("navigation produced an answer: %v", p.Answers)
	}
	if got := m.answerSummary(&ix.Questions[0], 0); !strings.Contains(got, "not answered") {
		t.Errorf("review should show (not answered), got %q", got)
	}

	// Enter commits the highlighted option.
	res, _ = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if !m.qAnswered(0) || m.qChosen(0) != 1 {
		t.Fatalf("Enter should select: answered=%v chosen=%d", m.qAnswered(0), m.qChosen(0))
	}
	if p := m.questionAnswers(ix); p.Answers["Which DB?"] != "SQLite" {
		t.Errorf("committed answer: %v", p.Answers)
	}
}

// TestSubmitOmitsUnanswered verifies only confirmed questions are sent.
func TestSubmitOmitsUnanswered(t *testing.T) {
	ix := multiQuestion()
	m := promptModel(ix)
	m.prompt.chosen[0] = 0 // Q0 answered; Q1 left unanswered
	p := m.questionAnswers(ix)
	if _, ok := p.Answers["Which DB?"]; !ok {
		t.Error("answered question missing")
	}
	if _, ok := p.Answers["Which cache?"]; ok {
		t.Error("unanswered question should be omitted")
	}
}

// TestSingleSelectRadioRender verifies the filled radio shows only after commit.
func TestSingleSelectRadioRender(t *testing.T) {
	ix := question(session.QuestionSpec{Question: "Q", Options: []string{"A", "B"}})
	m := promptModel(ix)
	if out := m.promptBody(); !strings.Contains(out, "○") {
		t.Errorf("single-select should render empty radios:\n%s", out)
	}
	if strings.Contains(m.promptBody(), "◉") {
		t.Errorf("no option should be filled before selection")
	}
	m.prompt.chosen[0] = 1
	if out := m.promptBody(); !strings.Contains(out, "◉") {
		t.Errorf("committed option should render a filled radio:\n%s", out)
	}
}

func TestPermissionBodyFormatsPerTool(t *testing.T) {
	m := testModel()

	// Bash: shows "$ command" + description, not raw JSON.
	bash := &session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Bash",
		ToolInput: `{"command":"ls -la","description":"list files"}`,
	}
	out := interactionBody(m, bash, 60)
	if !strings.Contains(out, "$ ls -la") || !strings.Contains(out, "list files") {
		t.Errorf("bash permission should show a formatted command:\n%s", out)
	}
	if strings.Contains(out, `"command"`) {
		t.Errorf("bash permission should not show raw JSON:\n%s", out)
	}

	// Edit: shows the old/new strings (diff), not raw JSON.
	edit := &session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Edit",
		ToolInput: `{"file_path":"a.go","old_string":"foo","new_string":"bar"}`,
	}
	out = interactionBody(m, edit, 60)
	if !strings.Contains(out, "foo") || !strings.Contains(out, "bar") {
		t.Errorf("edit permission should show a diff:\n%s", out)
	}

	// Unknown/MCP tool: generic fallback, no panic.
	other := &session.Interaction{
		Kind: session.InteractionPermission, ToolName: "mcp__x__do",
		ToolInput: `{"k":"v"}`,
	}
	if out := interactionBody(m, other, 60); !strings.Contains(out, "v") {
		t.Errorf("unknown tool should still render its input:\n%s", out)
	}
}

func TestInteractionKey(t *testing.T) {
	if interactionKey(nil) != "" {
		t.Error("nil interaction should have empty key")
	}
	a := &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: `{"command":"ls"}`}
	b := &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: `{"command":"rm -rf x"}`}
	aSame := &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: `{"command":"ls"}`}
	if interactionKey(a) == interactionKey(b) {
		t.Error("different tool input should yield different keys")
	}
	if interactionKey(a) != interactionKey(aSame) {
		t.Error("identical content should yield equal keys")
	}
}

func TestSyncPromptDraftResetsOnChange(t *testing.T) {
	a := &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: `{"command":"ls"}`}
	m := promptModel(a)
	m.prompt.key = interactionKey(a)
	m.prompt.decisionSel = 1
	m.prompt.reasonText = "use rg"

	// Same interaction re-published → draft preserved.
	m.syncPromptDraft()
	if m.prompt.reasonText != "use rg" {
		t.Errorf("same-interaction sync should preserve draft, got %q", m.prompt.reasonText)
	}

	// A different prompt → draft reset and key updated.
	b := &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: `{"command":"rm x"}`}
	m.sessions["s1"] = session.Session{ID: "s1", Status: session.StatusAwaitingInput, Interaction: b}
	m.syncPromptDraft()
	if m.prompt.reasonText != "" || m.prompt.decisionSel != 0 {
		t.Errorf("changed prompt should reset draft: reason=%q sel=%d", m.prompt.reasonText, m.prompt.decisionSel)
	}
	if m.prompt.key != interactionKey(b) {
		t.Error("promptKey should track the new interaction")
	}

	// Dismissal (interaction → nil) also resets.
	m.prompt.reasonText = "typing"
	m.sessions["s1"] = session.Session{ID: "s1", Status: session.StatusWorking, Interaction: nil}
	m.syncPromptDraft()
	if m.prompt.reasonText != "" || m.prompt.key != "" {
		t.Errorf("dismissal should reset draft: reason=%q key=%q", m.prompt.reasonText, m.prompt.key)
	}
}

func TestSubmitDecisionClearsDraft(t *testing.T) {
	m := promptModel(&session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Bash",
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true},
		},
	})
	m.prompt.decisionSel = 1 // Deny
	m.prompt.reasonText = "use rg instead"

	res, _ := m.submitDecision(m.sessions["s1"].Interaction)
	m = res.(model)
	if m.prompt.reasonText != "" {
		t.Errorf("deny reason should be cleared after submit, got %q", m.prompt.reasonText)
	}
	if m.prompt.decisionSel != 0 {
		t.Errorf("decisionSel should reset to 0, got %d", m.prompt.decisionSel)
	}
}

func TestSubmitAllClearsDraft(t *testing.T) {
	ix := question(session.QuestionSpec{Question: "Q", Options: []string{"A", "B"}})
	m := promptModel(ix)
	m.prompt.chosen[0] = 1 // committed "B"

	res, _ := m.submitAll(ix)
	m = res.(model)
	if m.prompt.chosen != nil || m.qChosen(0) != -1 {
		t.Errorf("question draft should be reset after submit: chosen=%v", m.prompt.chosen)
	}
}

func TestQuestionJKNavigation(t *testing.T) {
	ix := question(session.QuestionSpec{Question: "Q", Options: []string{"A", "B", "C"}})
	m := promptModel(ix)

	// j moves the highlight down, k moves it up (like the arrows).
	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = res.(model)
	if m.qSel(0) != 1 {
		t.Fatalf("j: qSel=%d want 1", m.qSel(0))
	}
	res, _ = m.handlePromptKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = res.(model)
	if m.qSel(0) != 0 {
		t.Fatalf("k: qSel=%d want 0", m.qSel(0))
	}
}

func TestQuestionJKTypesIntoCustomAnswer(t *testing.T) {
	ix := question(session.QuestionSpec{Question: "Q", Options: []string{"A", "B"}})
	q := &ix.Questions[0]
	m := promptModel(ix)
	m.prompt.sel[0] = otherIndex(q) // highlight the "type your own" row → accepts text

	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = res.(model)
	if m.qText(0) != "j" {
		t.Errorf("j should type into the custom field: text=%q", m.qText(0))
	}
	if m.qSel(0) != otherIndex(q) {
		t.Errorf("j should not move the highlight while editing custom: qSel=%d", m.qSel(0))
	}
}

func TestSubmitTabJKMovesSelection(t *testing.T) {
	m := promptModel(multiQuestion())
	m.prompt.tab = m.numQuestions() // Submit tab
	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = res.(model)
	if m.prompt.submitSel != 1 {
		t.Fatalf("j on submit tab: submitSel=%d want 1", m.prompt.submitSel)
	}
	res, _ = m.handlePromptKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = res.(model)
	if m.prompt.submitSel != 0 {
		t.Fatalf("k on submit tab: submitSel=%d want 0", m.prompt.submitSel)
	}
}

func TestSubmitTabCancelKeepsPending(t *testing.T) {
	m := promptModel(multiQuestion())
	m.prompt.tab = m.numQuestions() // Submit tab
	m.prompt.submitSel = 1          // Cancel
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if cmd != nil || m.focus != focusHistory {
		t.Errorf("cancel: cmd=%v focus=%v (should not send, return to history)", cmd, m.focus)
	}
}

func TestPromptQuestionChatAboutThis(t *testing.T) {
	// Pressing "c" on an unanswered question still sends (chat is valid empty).
	m := promptModel(question(session.QuestionSpec{Question: "Pick", Options: []string{"A", "B"}}))
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Text: "c", Code: 'c'})
	m = res.(model)
	if m.focus != focusHistory || cmd == nil {
		t.Fatalf("chat: focus=%v cmd=%v (want history + sent)", m.focus, cmd)
	}
}

func TestPromptQuestionCTypesIntoCustomAnswer(t *testing.T) {
	// While editing a custom answer, "c" must type, not trigger chat.
	q := session.QuestionSpec{Question: "Pick", Options: []string{"A"}}
	m := promptModel(question(q))
	// With one option the "type your own" row is index 1; arrow-down lands on it,
	// activating the free-text field.
	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	if m.qSel(0) != otherIndex(&q) {
		t.Fatalf("expected cursor on custom row, sel=%d", m.qSel(0))
	}
	// Now "c" must append to the custom text, not send a chat request.
	res, cmd := m.handlePromptKey(tea.KeyPressMsg{Text: "c", Code: 'c'})
	m = res.(model)
	if cmd != nil {
		t.Fatalf("editing custom: 'c' must not send, cmd=%v", cmd)
	}
	if m.qText(0) != "c" {
		t.Fatalf("editing custom: 'c' should type, text=%q", m.qText(0))
	}
}

func TestSubmitDecisionOptionlessIsNoOp(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionPermission})
	// No Options must be a defensive no-op, never a silent allow.
	ix := &session.Interaction{Kind: session.InteractionPermission}
	m.prompt.decisionSel = 0
	_, cmd := m.submitDecision(ix)
	if cmd != nil {
		t.Fatalf("optionless submitDecision should be a no-op, got a command")
	}
}

func TestDecisionOptionsUsesServerOptionsOnly(t *testing.T) {
	ix := &session.Interaction{
		Kind: session.InteractionPermission,
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true},
		},
	}
	got := decisionOptions(ix)
	if len(got) != 2 || got[0] != "Allow" || got[1] != "Deny" {
		t.Fatalf("got %v", got)
	}

	// With no server options there is no hardcoded fallback anymore.
	if l := decisionOptions(&session.Interaction{Kind: session.InteractionPermission}); l != nil {
		t.Fatalf("want nil for optionless interaction, got %v", l)
	}
}

func TestRespondElsewhereLabel(t *testing.T) {
	if got := respondElsewhereLabel(session.FrontendVSCode); got != "Respond in VSCode" {
		t.Errorf("vscode label = %q", got)
	}
	if got := respondElsewhereLabel(session.FrontendExternal); got != "Respond in your terminal" {
		t.Errorf("external label = %q", got)
	}
}

func TestIdleDockInformationalForPaneless(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	// Make the selected session a paneless vscode session.
	m.sessions["s1"] = session.Session{
		ID:          "s1",
		Status:      session.StatusAwaitingInput,
		Frontend:    session.FrontendVSCode, // no Tmux pane → not controllable
		Interaction: &session.Interaction{Kind: session.InteractionIdle},
	}

	lines, _, _ := m.promptLinesWidth(80)
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "Respond in VSCode") {
		t.Errorf("paneless idle dock should show the indicator, got:\n%s", out)
	}
	if !strings.Contains(out, "argus can't send input to this session") {
		t.Errorf("paneless idle dock should show the sub-line, got:\n%s", out)
	}
	if strings.Contains(out, "> ") {
		t.Errorf("paneless idle dock must NOT show the editable composer, got:\n%s", out)
	}
}

func TestIdleDockComposerForControllable(t *testing.T) {
	// Default promptModel session is tmux/controllable with a pane.
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	lines, _, _ := m.promptLinesWidth(80)
	out := strings.Join(lines, "\n")
	if strings.Contains(out, "Respond in VSCode") {
		t.Errorf("controllable idle dock must not show the indicator, got:\n%s", out)
	}
	if !strings.Contains(out, "> ") {
		t.Errorf("controllable idle dock should show the composer, got:\n%s", out)
	}
}

// TestDockScrollBodyPinsControls exercises the dock body windowing directly with
// synthetic lines: the control block pins to the bottom while the body scrolls.
func TestDockScrollBodyPinsControls(t *testing.T) {
	m := testModel()
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("body-%02d", i))
	}
	lines = append(lines, "▸ Allow", "  Deny")
	ctrlStart := 20 // options start
	anchor := 20
	height := 10 // bodyRegion=8 → ch=7 (one row reserved for the hint)
	width := 40

	// scroll=0: top of body + pinned controls; bottom of body hidden.
	m.prompt.scroll = 0
	out := m.dockScrollBody(lines, height, anchor, ctrlStart, width)
	if got := len(strings.Split(out, "\n")); got != height {
		t.Fatalf("rendered %d rows, want height %d:\n%s", got, height, out)
	}
	for _, want := range []string{"body-00", "Allow", "Deny"} {
		if !strings.Contains(out, want) {
			t.Errorf("scroll=0 output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "body-19") {
		t.Errorf("scroll=0 should hide the bottom of the body:\n%s", out)
	}

	// Max scroll: bottom of body visible, controls still pinned.
	m.prompt.scroll = 13 // maxScroll = len(body)-ch = 20-7
	out = m.dockScrollBody(lines, height, anchor, ctrlStart, width)
	for _, want := range []string{"body-19", "Allow", "Deny"} {
		if !strings.Contains(out, want) {
			t.Errorf("max scroll output missing %q:\n%s", want, out)
		}
	}

	// Over-scroll clamps to the bottom (still shows the last body line).
	m.prompt.scroll = 999
	out = m.dockScrollBody(lines, height, anchor, ctrlStart, width)
	if !strings.Contains(out, "body-19") || !strings.Contains(out, "Deny") {
		t.Errorf("over-scroll should clamp to the bottom:\n%s", out)
	}
	if got := len(strings.Split(out, "\n")); got != height {
		t.Errorf("over-scroll rendered %d rows, want %d:\n%s", got, height, out)
	}

	// When everything fits, no scrolling/hint: all lines returned verbatim.
	out = m.dockScrollBody(lines, 40, anchor, ctrlStart, width)
	if out != strings.Join(lines, "\n") {
		t.Errorf("fitting content should render whole:\n%s", out)
	}
}

// TestDockScrollKeysRevealTallBody drives the real render + key path: ctrl+d/ctrl+u
// scroll a too-tall prompt body while Allow/Deny stay pinned and reachable, and
// up/down still move the selection.
func TestDockScrollKeysRevealTallBody(t *testing.T) {
	var msg []string
	for i := 0; i < 40; i++ {
		msg = append(msg, fmt.Sprintf("MK%02d", i))
	}
	m := promptModel(&session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Bash",
		Message: strings.Join(msg, "\n"),
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true},
		},
	})

	dockH := func() int { _, h := m.sessionLayout(); return h - 1 }
	render := func() string { return m.dockBody(dockH()) }

	out := render()
	for _, want := range []string{"Allow", "Deny", "MK00"} {
		if !strings.Contains(out, want) {
			t.Fatalf("initial dock missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "MK39") {
		t.Fatalf("bottom of a tall body should be hidden initially:\n%s", out)
	}

	// Scroll down until the bottom is revealed.
	for i := 0; i < 20; i++ {
		res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
		m = res.(model)
	}
	out = render()
	if !strings.Contains(out, "MK39") {
		t.Errorf("bottom should be visible after scrolling down:\n%s", out)
	}
	if !strings.Contains(out, "Allow") || !strings.Contains(out, "Deny") {
		t.Errorf("controls must stay pinned while scrolled:\n%s", out)
	}
	maxScroll, _ := m.dockScrollGeom(dockH())
	if maxScroll == 0 {
		t.Fatal("expected the tall body to be scrollable")
	}
	if m.prompt.scroll != maxScroll {
		t.Errorf("scroll = %d, want clamped max %d", m.prompt.scroll, maxScroll)
	}

	// Scroll back up past the top clamps to 0.
	for i := 0; i < 30; i++ {
		res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		m = res.(model)
	}
	if m.prompt.scroll != 0 {
		t.Errorf("scroll = %d, want 0 after scrolling up past the top", m.prompt.scroll)
	}

	// Up/down still moves the decision selection (not the scroll).
	res, _ := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	if m.prompt.decisionSel != 1 {
		t.Errorf("down should select Deny; decisionSel=%d", m.prompt.decisionSel)
	}
	if m.prompt.scroll != 0 {
		t.Errorf("selection should not change scroll; scroll=%d", m.prompt.scroll)
	}
}
