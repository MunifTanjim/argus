package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// -- Key handling -------------------------------------------------------------

// handleIdleKey composes a free-text reply, delivered via pane input on submit.
func (d dockComp) handleIdleKey(c *ctx, msg tea.KeyPressMsg) (dockComp, tea.Cmd) {
	if s := c.m.sessions[c.m.liveSessionID()]; !s.AcceptsInput() {
		return d, nil
	}
	if c.m.matches(msg, promptKeys.Submit) {
		id := c.m.liveSessionID()
		txt := strings.TrimSpace(d.reply.Value())
		d.reply.SetValue("")
		delete(d.drafts, id)
		c.focusOn(mainPane)
		if txt == "" {
			return d, nil
		}
		return d, c.m.sendInputCmd(id, txt)
	}
	var cmd tea.Cmd
	d.reply, cmd = d.reply.Update(msg)
	d.sizeIdleReply(c)
	return d, cmd
}

// handleDecisionKey drives the permission/plan allow/deny choice and deny reason.
func (d dockComp) handleDecisionKey(c *ctx, msg tea.KeyPressMsg, ix *session.Interaction) (dockComp, tea.Cmd) {
	opts := decisionOptions(ix)
	denying := d.decisionRejecting(ix)
	switch {
	case c.m.matches(msg, promptKeys.Up):
		d.decisionSel = max(0, d.decisionSel-1)
		return d, nil
	case c.m.matches(msg, promptKeys.Down):
		d.decisionSel = min(len(opts)-1, d.decisionSel+1)
		return d, nil
	case c.m.matches(msg, promptKeys.Submit):
		return d.submitDecision(c, ix)
	}
	if denying {
		var cmd tea.Cmd
		d.reason, cmd = d.reason.Update(msg)
		return d, cmd
	}
	return d, nil
}

// handleQuestionKey drives the tabbed multi-question panel.
func (d dockComp) handleQuestionKey(c *ctx, msg tea.KeyPressMsg, ix *session.Interaction) (dockComp, tea.Cmd) {
	d.ensurePromptState(len(ix.Questions))
	if d.onSubmitTab(c) {
		return d.handleSubmitTabKey(c, msg, ix)
	}
	tab := d.tab
	q := &ix.Questions[tab]
	opts := questionOptions(q)
	maxTab := len(ix.Questions) - 1
	if c.m.isMultiQuestion() {
		maxTab = len(ix.Questions) // Submit tab
	}
	accepts := d.otherActive(q, tab)

	// "c" = "Chat about this", unless editing a custom answer (then it types).
	if !accepts && msg.String() == "c" {
		return d.chatAboutQuestions(c, ix)
	}

	// While a custom answer is edited, j/k and the question-tab keys go to the
	// text input.
	switch {
	case !accepts && c.m.matches(msg, promptKeys.TabPrev):
		d.tab = max(0, d.tab-1)
		return d, nil
	case !accepts && c.m.matches(msg, promptKeys.TabNext):
		d.tab = min(maxTab, d.tab+1)
		return d, nil
	case c.m.matches(msg, promptKeys.Up) || (!accepts && msg.String() == "k"):
		d.sel[tab] = max(0, d.sel[tab]-1)
		return d, nil
	case c.m.matches(msg, promptKeys.Down) || (!accepts && msg.String() == "j"):
		d.sel[tab] = min(len(opts)-1, d.sel[tab]+1)
		return d, nil
	case c.m.matches(msg, promptKeys.Submit):
		return d.commitQuestion(c, ix)
	case q.MultiSelect && c.m.matches(msg, promptKeys.Select, promptKeys.Unselect):
		sel := d.sel[tab]
		switch on := d.toggles[tab][sel]; {
		case !on && c.m.matches(msg, promptKeys.Select):
			d.toggles[tab][sel] = true
		case on && c.m.matches(msg, promptKeys.Unselect):
			d.toggles[tab][sel] = false
		}
		return d, nil
	}
	if accepts {
		var cmd tea.Cmd
		d.text[tab], cmd = d.text[tab].Update(msg)
		return d, cmd
	}
	return d, nil
}

// commitQuestion commits the highlighted single-select option (multi-select uses
// space toggles) and advances: single question submits, multi moves to next tab.
func (d dockComp) commitQuestion(c *ctx, ix *session.Interaction) (dockComp, tea.Cmd) {
	tab := d.tab
	q := &ix.Questions[tab]
	if !q.MultiSelect {
		sel := d.sel[tab]
		if sel == otherIndex(q) && strings.TrimSpace(d.qText(tab)) == "" {
			return d, nil // can't select an empty custom answer
		}
		d.chosen[tab] = sel
	}
	if !c.m.isMultiQuestion() {
		return d.submitAll(c, ix)
	}
	d.tab = min(len(ix.Questions), d.tab+1)
	return d, nil
}

// handleSubmitTabKey drives the Submit/Cancel review tab.
func (d dockComp) handleSubmitTabKey(c *ctx, msg tea.KeyPressMsg, ix *session.Interaction) (dockComp, tea.Cmd) {
	switch {
	case c.m.matches(msg, promptKeys.TabPrev):
		d.tab = len(ix.Questions) - 1
	case c.m.matches(msg, promptKeys.Up) || msg.String() == "k":
		d.submitSel = max(0, d.submitSel-1)
	case c.m.matches(msg, promptKeys.Down) || msg.String() == "j":
		d.submitSel = min(1, d.submitSel+1)
	case c.m.matches(msg, promptKeys.Submit):
		if d.submitSel == 0 {
			return d.submitAll(c, ix)
		}
		return d.cancelQuestions(c, ix)
	case msg.String() == "c":
		return d.chatAboutQuestions(c, ix)
	}
	return d, nil
}

// cancelQuestions declines the AskUserQuestion prompt the way native Claude Code
// cancel does: the tool is rejected so the session stops waiting for an answer.
func (d dockComp) cancelQuestions(c *ctx, ix *session.Interaction) (dockComp, tea.Cmd) {
	id := c.m.liveSessionID()
	c.focusOn(mainPane)
	d.resetPromptState()
	return d, c.m.respondCmd(id, api.RespondParams{Kind: string(ix.Kind), QuestionAction: "cancel"})
}

// submitDecision sends a permission/plan decision by echoing the chosen option's
// Value. An out-of-range selection is a defensive no-op, never a silent allow.
func (d dockComp) submitDecision(c *ctx, ix *session.Interaction) (dockComp, tea.Cmd) {
	sel := d.decisionSel
	if sel < 0 || sel >= len(ix.Options) {
		return d, nil
	}
	o := ix.Options[sel]
	p := api.RespondParams{Kind: string(ix.Kind), OptionValue: o.Value}
	if o.Reject {
		p.Reason = strings.TrimSpace(d.reason.Value())
	}
	id := c.m.liveSessionID()
	c.focusOn(mainPane)
	d.resetPromptState()
	return d, c.m.respondCmd(id, p)
}

// submitAll sends every answered question's answer; unanswered questions are
// omitted. A fully-unanswered prompt is a no-op (nothing is sent).
func (d dockComp) submitAll(c *ctx, ix *session.Interaction) (dockComp, tea.Cmd) {
	p := d.questionAnswers(ix)
	if len(p.Answers) == 0 {
		return d, nil
	}
	id := c.m.liveSessionID()
	c.focusOn(mainPane)
	d.resetPromptState()
	return d, c.m.respondCmd(id, p)
}

// questionAnswers builds the answers map (keyed by question text) over the
// answered questions; unanswered ones are omitted.
func (d dockComp) questionAnswers(ix *session.Interaction) api.RespondParams {
	p := api.RespondParams{Kind: string(ix.Kind), Behavior: "allow", Answers: map[string]any{}}
	for tab := range ix.Questions {
		if v, ok := d.questionAnswer(&ix.Questions[tab], tab); ok {
			p.Answers[ix.Questions[tab].Question] = v
		}
	}
	return p
}

// chatAboutQuestions rejects the question prompt with a clarify request,
// carrying whatever partial answers exist. Unlike submitAll it always sends,
// even with no answers.
func (d dockComp) chatAboutQuestions(c *ctx, ix *session.Interaction) (dockComp, tea.Cmd) {
	p := d.questionAnswers(ix)
	p.QuestionAction = "chat"
	id := c.m.liveSessionID()
	c.focusOn(mainPane)
	d.resetPromptState()
	return d, c.m.respondCmd(id, p)
}
