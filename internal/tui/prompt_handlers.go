package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// -- Key handling -------------------------------------------------------------

func (m model) handlePromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := m.sessions[m.selectedID]
	if s.Interaction == nil {
		m.mode = modeList
		return m, nil
	}

	// Scroll the dock body (plan / message text) independently of the pinned
	// controls. These keys are never text or selection, so intercept them for
	// every interaction kind before the per-kind handlers run.
	switch {
	case key.Matches(msg, promptKeys.HalfUp):
		return m.scrollDock(-1), nil
	case key.Matches(msg, promptKeys.HalfDown):
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
	// Paneless idle dock is informational only: swallow keys silently (the
	// indicator already explains there's no pane to deliver input to).
	if !m.sessions[m.selectedID].Controllable() {
		return m, nil
	}
	switch msg.String() {
	case "enter":
		id := m.selectedID
		txt := strings.TrimSpace(m.prompt.reasonText)
		m.prompt.reasonText = ""
		m.focus = focusHistory
		if txt == "" {
			return m, nil
		}
		return m, m.sendInputCmd(id, txt)
	case "shift+enter":
		// Newline instead of submit (multi-line replies). Only arrives where the
		// Kitty keyboard protocol is honored; pasting is the universal path.
		m.prompt.reasonText += "\n"
	case "backspace":
		if len(m.prompt.reasonText) > 0 {
			m.prompt.reasonText = m.prompt.reasonText[:len(m.prompt.reasonText)-1]
		}
	default:
		if msg.Text != "" {
			m.prompt.reasonText += msg.Text
		}
	}
	return m, nil
}

// handleDecisionKey drives the permission/plan allow/deny choice and deny reason.
func (m model) handleDecisionKey(msg tea.KeyPressMsg, ix *session.Interaction) (tea.Model, tea.Cmd) {
	opts := decisionOptions(ix)
	denying := m.decisionRejecting(ix)
	switch msg.String() {
	case "up", "ctrl+p":
		m.prompt.decisionSel = max(0, m.prompt.decisionSel-1)
	case "down", "ctrl+n":
		m.prompt.decisionSel = min(len(opts)-1, m.prompt.decisionSel+1)
	case " ", "space":
		if denying {
			m.prompt.reasonText += " "
		}
	case "enter":
		return m.submitDecision(ix)
	case "backspace":
		if denying && len(m.prompt.reasonText) > 0 {
			m.prompt.reasonText = m.prompt.reasonText[:len(m.prompt.reasonText)-1]
		}
	default:
		if denying && msg.Text != "" {
			m.prompt.reasonText += msg.Text
		}
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

	// j/k navigate like arrows, unless editing a custom answer (then they type).
	key := msg.String()
	if !accepts {
		switch key {
		case "j":
			key = "down"
		case "k":
			key = "up"
		}
	}

	switch key {
	case "left":
		m.prompt.tab = max(0, m.prompt.tab-1)
	case "right":
		m.prompt.tab = min(maxTab, m.prompt.tab+1)
	case "up", "ctrl+p":
		m.prompt.sel[tab] = max(0, m.prompt.sel[tab]-1)
	case "down", "ctrl+n":
		m.prompt.sel[tab] = min(len(opts)-1, m.prompt.sel[tab]+1)
	case " ", "space":
		if q.MultiSelect {
			sel := m.prompt.sel[tab]
			m.prompt.toggles[tab][sel] = !m.prompt.toggles[tab][sel]
		} else if accepts {
			m.prompt.text[tab] += " "
		}
	case "enter":
		return m.commitQuestion(ix)
	case "backspace":
		if accepts && len(m.prompt.text[tab]) > 0 {
			m.prompt.text[tab] = m.prompt.text[tab][:len(m.prompt.text[tab])-1]
		}
	default:
		if accepts && msg.Text != "" {
			m.prompt.text[tab] += msg.Text
		}
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
		if sel == otherIndex(q) && strings.TrimSpace(m.prompt.text[tab]) == "" {
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
	switch msg.String() {
	case "left":
		m.prompt.tab = len(ix.Questions) - 1
	case "up", "ctrl+p", "k":
		m.prompt.submitSel = max(0, m.prompt.submitSel-1)
	case "down", "ctrl+n", "j":
		m.prompt.submitSel = min(1, m.prompt.submitSel+1)
	case "enter":
		if m.prompt.submitSel == 0 {
			return m.submitAll(ix)
		}
		m.focus = focusHistory // Cancel: leave the prompt pending
	case "c":
		return m.chatAboutQuestions(ix)
	}
	return m, nil
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
		p.Reason = strings.TrimSpace(m.prompt.reasonText)
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
