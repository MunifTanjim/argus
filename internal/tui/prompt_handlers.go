package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// -- Key handling -------------------------------------------------------------

func (m model) handlePromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := m.sessions[m.selectedID]
	if s.Interaction == nil {
		m.mode = m.sessionReturn
		return m, nil
	}

	// Scroll the dock body (plan / message text) independently of the pinned
	// controls. These keys are never text or selection, so intercept them for
	// every interaction kind before the per-kind handlers run.
	switch {
	case m.matches(msg, promptKeys.HalfUp):
		return m.scrollDock(-1), nil
	case m.matches(msg, promptKeys.HalfDown):
		return m.scrollDock(1), nil
	}

	ix := s.Interaction
	switch ix.Kind {
	case session.InteractionIdle:
		return m.handleIdleKey(msg)
	case session.InteractionQuestion:
		return m.handleQuestionKey(msg, ix)
	default: // permission / plan
		return m.handleDecisionKey(msg, ix)
	}
}

// handleIdleKey composes a free-text reply, delivered via pane input on submit.
func (m model) handleIdleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if s := m.sessions[m.selectedID]; !s.AcceptsInput() {
		return m, nil
	}
	if m.matches(msg, promptKeys.Submit) {
		id := m.selectedID
		txt := strings.TrimSpace(m.prompt.reply.Value())
		m.prompt.reply.SetValue("")
		delete(m.replyDrafts, id)
		m.focus = focusHistory
		if txt == "" {
			return m, nil
		}
		return m, m.sendInputCmd(id, txt)
	}
	var cmd tea.Cmd
	m.prompt.reply, cmd = m.prompt.reply.Update(msg)
	m.sizeIdleReply()
	return m, cmd
}

// handleDecisionKey drives the permission/plan allow/deny choice and deny reason.
func (m model) handleDecisionKey(msg tea.KeyPressMsg, ix *session.Interaction) (tea.Model, tea.Cmd) {
	opts := decisionOptions(ix)
	denying := m.decisionRejecting(ix)
	switch {
	case m.matches(msg, promptKeys.Up):
		m.prompt.decisionSel = max(0, m.prompt.decisionSel-1)
		return m, nil
	case m.matches(msg, promptKeys.Down):
		m.prompt.decisionSel = min(len(opts)-1, m.prompt.decisionSel+1)
		return m, nil
	case m.matches(msg, promptKeys.Submit):
		return m.submitDecision(ix)
	}
	if denying {
		var cmd tea.Cmd
		m.prompt.reason, cmd = m.prompt.reason.Update(msg)
		return m, cmd
	}
	return m, nil
}

// handleQuestionKey drives the tabbed multi-question panel.
func (m model) handleQuestionKey(msg tea.KeyPressMsg, ix *session.Interaction) (tea.Model, tea.Cmd) {
	m.ensurePromptState(len(ix.Questions))
	if m.onSubmitTab() {
		return m.handleSubmitTabKey(msg, ix)
	}
	tab := m.prompt.tab
	q := &ix.Questions[tab]
	opts := questionOptions(q)
	maxTab := len(ix.Questions) - 1
	if m.isMultiQuestion() {
		maxTab = len(ix.Questions) // Submit tab
	}
	accepts := m.otherActive(q, tab)

	// "c" = "Chat about this", unless editing a custom answer (then it types).
	if !accepts && msg.String() == "c" {
		return m.chatAboutQuestions(ix)
	}

	// While a custom answer is edited, j/k and the question-tab keys go to the
	// text input.
	switch {
	case !accepts && m.matches(msg, promptKeys.TabPrev):
		m.prompt.tab = max(0, m.prompt.tab-1)
		return m, nil
	case !accepts && m.matches(msg, promptKeys.TabNext):
		m.prompt.tab = min(maxTab, m.prompt.tab+1)
		return m, nil
	case m.matches(msg, promptKeys.Up) || (!accepts && msg.String() == "k"):
		m.prompt.sel[tab] = max(0, m.prompt.sel[tab]-1)
		return m, nil
	case m.matches(msg, promptKeys.Down) || (!accepts && msg.String() == "j"):
		m.prompt.sel[tab] = min(len(opts)-1, m.prompt.sel[tab]+1)
		return m, nil
	case m.matches(msg, promptKeys.Submit):
		return m.commitQuestion(ix)
	case q.MultiSelect && m.matches(msg, promptKeys.Toggle):
		sel := m.prompt.sel[tab]
		m.prompt.toggles[tab][sel] = !m.prompt.toggles[tab][sel]
		return m, nil
	}
	if accepts {
		var cmd tea.Cmd
		m.prompt.text[tab], cmd = m.prompt.text[tab].Update(msg)
		return m, cmd
	}
	return m, nil
}

// commitQuestion commits the highlighted single-select option (multi-select uses
// space toggles) and advances: single question submits, multi moves to next tab.
func (m model) commitQuestion(ix *session.Interaction) (tea.Model, tea.Cmd) {
	tab := m.prompt.tab
	q := &ix.Questions[tab]
	if !q.MultiSelect {
		sel := m.prompt.sel[tab]
		if sel == otherIndex(q) && strings.TrimSpace(m.qText(tab)) == "" {
			return m, nil // can't select an empty custom answer
		}
		m.prompt.chosen[tab] = sel
	}
	if !m.isMultiQuestion() {
		return m.submitAll(ix)
	}
	m.prompt.tab = min(len(ix.Questions), m.prompt.tab+1)
	return m, nil
}

// handleSubmitTabKey drives the Submit/Cancel review tab.
func (m model) handleSubmitTabKey(msg tea.KeyPressMsg, ix *session.Interaction) (tea.Model, tea.Cmd) {
	switch {
	case m.matches(msg, promptKeys.TabPrev):
		m.prompt.tab = len(ix.Questions) - 1
	case m.matches(msg, promptKeys.Up) || msg.String() == "k":
		m.prompt.submitSel = max(0, m.prompt.submitSel-1)
	case m.matches(msg, promptKeys.Down) || msg.String() == "j":
		m.prompt.submitSel = min(1, m.prompt.submitSel+1)
	case m.matches(msg, promptKeys.Submit):
		if m.prompt.submitSel == 0 {
			return m.submitAll(ix)
		}
		return m.cancelQuestions(ix)
	case msg.String() == "c":
		return m.chatAboutQuestions(ix)
	}
	return m, nil
}

// cancelQuestions declines the AskUserQuestion prompt the way native Claude Code
// cancel does: the tool is rejected so the session stops waiting for an answer.
func (m model) cancelQuestions(ix *session.Interaction) (tea.Model, tea.Cmd) {
	id := m.selectedID
	m.focus = focusHistory
	m.resetPromptState()
	return m, m.respondCmd(id, api.RespondParams{Kind: string(ix.Kind), QuestionAction: "cancel"})
}

// submitDecision sends a permission/plan decision by echoing the chosen option's
// Value. An out-of-range selection is a defensive no-op, never a silent allow.
func (m model) submitDecision(ix *session.Interaction) (tea.Model, tea.Cmd) {
	sel := m.prompt.decisionSel
	if sel < 0 || sel >= len(ix.Options) {
		return m, nil
	}
	o := ix.Options[sel]
	p := api.RespondParams{Kind: string(ix.Kind), OptionValue: o.Value}
	if o.Reject {
		p.Reason = strings.TrimSpace(m.prompt.reason.Value())
	}
	id := m.selectedID
	m.focus = focusHistory
	m.resetPromptState() // clear the draft so the next prompt starts fresh
	return m, m.respondCmd(id, p)
}

// submitAll sends every answered question's answer; unanswered questions are
// omitted. A fully-unanswered prompt is a no-op (nothing is sent).
func (m model) submitAll(ix *session.Interaction) (tea.Model, tea.Cmd) {
	p := m.questionAnswers(ix)
	if len(p.Answers) == 0 {
		return m, nil
	}
	id := m.selectedID
	m.focus = focusHistory
	m.resetPromptState() // clear the draft so the next prompt starts fresh
	return m, m.respondCmd(id, p)
}

// questionAnswers builds the answers map (keyed by question text) over the
// answered questions; unanswered ones are omitted.
func (m model) questionAnswers(ix *session.Interaction) api.RespondParams {
	p := api.RespondParams{Kind: string(ix.Kind), Behavior: "allow", Answers: map[string]any{}}
	for tab := range ix.Questions {
		if v, ok := m.questionAnswer(&ix.Questions[tab], tab); ok {
			p.Answers[ix.Questions[tab].Question] = v
		}
	}
	return p
}

// chatAboutQuestions rejects the question prompt with a clarify request,
// carrying whatever partial answers exist. Unlike submitAll it always sends,
// even with no answers.
func (m model) chatAboutQuestions(ix *session.Interaction) (tea.Model, tea.Cmd) {
	p := m.questionAnswers(ix)
	p.QuestionAction = "chat"
	id := m.selectedID
	m.focus = focusHistory
	m.resetPromptState()
	return m, m.respondCmd(id, p)
}
