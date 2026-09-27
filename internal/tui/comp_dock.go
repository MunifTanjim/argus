package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// dockComp is the session dock: the draft answer to the open session's pending
// interaction, and the unsent idle replies of every session. Questions use the
// per-question slices; permission, plan, and idle use the scalar drafts.
type dockComp struct {
	tab         int               // active tab: 0..len-1 question, ==len → Submit tab
	sel         []int             // highlighted option index per question (navigation only)
	chosen      []int             // committed single-select option per question (-1 = unanswered)
	toggles     []map[int]bool    // multi-select toggles per question
	text        []textinput.Model // "type your own" draft per question
	submitSel   int               // 0=Submit, 1=Cancel on the Submit tab
	decisionSel int               // permission/plan option index (Allow/Deny)
	reason      textinput.Model   // permission/plan deny reason (single-line)
	reply       textarea.Model    // idle reply composer (multi-line via shift+enter)
	scroll      int               // dock body scroll offset (lines above the pinned controls)
	key         string            // identity of the interaction the draft belongs to
	session     string
	drafts      map[string]string // unsent idle-reply drafts, keyed by session id
}

func newDock() dockComp {
	return dockComp{reason: newDenyReasonInput(), reply: newIdleReplyArea(), drafts: map[string]string{}}
}

func (d dockComp) section() string { return "session-dock" }

// raw is always true: every key in the dock is text or a choice.
func (d dockComp) raw(*ctx) bool             { return true }
func (d dockComp) spins(*ctx) bool           { return false }
func (d dockComp) fullScreen(*ctx) fullLevel { return notFull }
func (d dockComp) close(*ctx) tea.Cmd        { return nil }
func (d dockComp) offers(*ctx) []binding     { return nil }
func (d dockComp) commands(*ctx) []binding   { return nil }
func (d dockComp) pageStep(c *ctx) int       { return c.m.listPageStep() }
func (d dockComp) layer() layer              { return baseLayer }

func (d dockComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	c.setFlash("")
	switch {
	case c.m.matches(msg, sessionKeys.FocusTranscript):
		if c.m.sessionInteraction() != nil {
			c.focusOn(mainPane)
		}
		return d, nil, true
	case c.m.matches(msg, promptKeys.Back):
		c.focusOn(mainPane)
		return d, nil, true
	case c.m.matches(msg, sessionKeys.Raw):
		return d, openLiveScreen(c), true
	}
	ix := c.m.interaction()
	if ix == nil {
		return d, nil, true
	}

	// Scroll keys are never text or selection, so they run for every interaction
	// kind before the per-kind handlers.
	switch {
	case c.m.matches(msg, promptKeys.HalfUp):
		return d.scrollDock(c, -1), nil, true
	case c.m.matches(msg, promptKeys.HalfDown):
		return d.scrollDock(c, 1), nil, true
	}

	var cmd tea.Cmd
	switch ix.Kind {
	case session.InteractionIdle:
		d, cmd = d.handleIdleKey(c, msg)
	case session.InteractionQuestion:
		d, cmd = d.handleQuestionKey(c, msg, ix)
	default: // permission / plan
		d, cmd = d.handleDecisionKey(c, msg, ix)
	}
	return d, cmd, true
}

func (d dockComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	p, ok := msg.(tea.PasteMsg)
	if !ok {
		return d, nil
	}
	var cmd tea.Cmd
	switch ix := c.m.interaction(); {
	case ix != nil && ix.Kind == session.InteractionIdle:
		d.reply, cmd = d.reply.Update(p)
		d.sizeIdleReply(c)
	case d.questionCustomActive(c):
		d.text[d.tab], cmd = d.text[d.tab].Update(p)
	case d.denyReasonActive(c):
		d.reason, cmd = d.reason.Update(p)
	}
	return d, cmd
}

// The body is inset by contentPadX to align with the transcript above.
func (d dockComp) view(c *ctx, w, h int) string {
	focused := c.m.focused == sessionDock
	ruleColor := ColorBorder
	if focused {
		ruleColor = ColorAccent
	}
	cw := containerWidthOf(w)
	rule := lipgloss.NewStyle().Foreground(ruleColor).Render(strings.Repeat("─", cw))
	body := c.m.dockSummaryLine(c.m.dockContentWidth())
	if focused {
		body = d.dockBody(c, h-1)
	}
	return centerBlock(rule+"\n"+indentBlock(body, strings.Repeat(" ", contentPadX)), cw, w)
}

func (d dockComp) footer(c *ctx) []binding {
	multi := c.m.isMultiQuestion()
	binds := []binding{promptKeys.Up}
	if multi {
		binds = append(binds, promptKeys.TabPrev, promptKeys.Next)
	} else {
		binds = append(binds, promptKeys.Submit)
	}
	if d.dockScrolls(c) {
		binds = append(binds, promptKeys.HalfUp)
	}
	binds = append(binds, promptKeys.Read)
	if !multi {
		binds = append(binds, sessionKeys.Raw)
	}
	return binds
}

// dockShown reports whether the session dock shows: the main pane shows a live
// session, under an open file or not, and that session waits for input.
func (m model) dockShown() bool {
	return m.inSession() && m.sessionInteraction() != nil
}

func (m model) dockFocused() bool {
	return m.inSession() && m.focused == sessionDock
}

func (m model) pasteDock(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	d, cmd := m.dock.update(c, msg)
	m.dock = d.(dockComp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (m *model) sizeIdleReply() { m.dock.sizeIdleReply(&ctx{m: m}) }

func (d dockComp) follow(c *ctx, id string) dockComp {
	d.saveReplyDraft(d.session)
	d.session = id
	d.resetPromptState()
	d.loadReplyDraft(c, id)
	d.key = interactionKey(c.m.sessions[id].Interaction)
	return d
}

// syncDock moves the draft to the live session when a pop brings back a live
// transcript of another session.
func (m model) syncDock() model {
	if id := m.liveSessionID(); id != "" && id != m.dock.session {
		m.dock = m.dock.follow(&ctx{m: &m}, id)
	}
	return m
}

// syncPromptDraft resets the compose draft when the pending interaction changes
// identity (new or cleared) so a stale draft never carries over. A re-publish of
// the same interaction keeps the in-progress draft.
func (m *model) syncPromptDraft() {
	k := interactionKey(m.sessions[m.dock.session].Interaction)
	if k != m.dock.key {
		m.dock.resetPromptState()
		m.dock.key = k
	}
}
