package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

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
	notes       []textinput.Model // note draft per question (questions with AllowNotes)
	confirming  bool              // asking whether to submit with unanswered questions
	submitSel   int               // 0=Submit, 1=Cancel on the Submit tab; 0=Proceed, 1=Go back while confirming
	decisionSel int               // permission/plan option index (Allow/Deny)
	reason      textinput.Model   // permission/plan deny reason (single-line)
	reply       textarea.Model    // idle reply composer (multi-line via shift+enter)
	scroll      int               // dock body scroll offset (lines above the pinned controls)
	key         string            // identity of the interaction the draft belongs to
	session     string
	drafts      map[string]string // unsent idle-reply drafts, keyed by session id
}

func newDock() dockComp {
	return dockComp{reason: newTextInput(), reply: newIdleReplyArea(), drafts: map[string]string{}}
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
	case d.noteEditing(c) && c.m.matches(msg, promptKeys.Back):
		d.notes[d.tab].Reset()
		d.notes[d.tab].Blur()
		return d, nil, true
	case c.m.matches(msg, sessionKeys.FocusTranscript):
		c.focusOn(mainPane)
		return d, nil, true
	case d.confirming && c.m.matches(msg, promptKeys.Back):
		d.confirming = false
		return d, nil, true
	case c.m.matches(msg, promptKeys.Back):
		c.focusOn(mainPane)
		return d, nil, true
	case c.m.matches(msg, sessionKeys.Raw):
		return d, openLiveScreen(c), true
	}
	if c.m.composing() {
		d, cmd := d.handleIdleKey(c, msg)
		return d, cmd, true
	}
	ix := c.m.interaction()

	// Scroll keys are never text or selection, so they run for every interaction
	// kind before the per-kind handlers.
	switch {
	case c.m.matches(msg, promptKeys.HalfUp):
		return d.scrollDock(c, -1), nil, true
	case c.m.matches(msg, promptKeys.HalfDown):
		return d.scrollDock(c, 1), nil, true
	case c.m.matches(msg, promptKeys.PageUp):
		return d.scrollDock(c, -2), nil, true
	case c.m.matches(msg, promptKeys.PageDown):
		return d.scrollDock(c, 2), nil, true
	}

	var cmd tea.Cmd
	switch ix.Kind {
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
	switch {
	case c.m.composing():
		d.reply, cmd = d.reply.Update(p)
		d.sizeIdleReply(c)
	case d.noteEditing(c):
		d.notes[d.tab], cmd = d.notes[d.tab].Update(p)
	case d.questionCustomActive(c):
		d.text[d.tab], cmd = d.text[d.tab].Update(p)
	case d.denyReasonActive(c):
		d.reason, cmd = d.reason.Update(p)
	}
	return d, cmd
}

// A prompt's body is inset to align with the transcript; the input spans the
// rules' full width.
func (d dockComp) view(c *ctx, w, h int) string {
	focused := c.m.focused == sessionDock
	ruleColor := ColorBorder
	if focused {
		ruleColor = ColorAccent
	}
	cw := containerWidthOf(w)
	top, bottom := c.m.dockRules(cw, ruleColor)
	bodyH := h - dockChrome
	var body string
	switch {
	case focused:
		body = d.dockBody(c, bodyH)
	case c.m.composing():
		lines := d.composeLines(c)
		if len(lines) > bodyH {
			lines = append(lines[:max(0, bodyH-1)], dimStyle.Render("…"))
		}
		body = strings.Join(lines[:min(len(lines), max(1, bodyH))], "\n")
	default:
		body = c.m.dockSummaryLine(c.m.dockContentWidth())
	}
	if !c.m.inputShown() {
		body = indentBlock(body, strings.Repeat(" ", contentPadX))
	}
	return "\n" + centerBlock(top+"\n"+body+"\n"+bottom, cw, w)
}

func (d dockComp) footer(c *ctx) []binding {
	if c.m.composing() {
		var binds []binding
		if c.m.sessions[c.m.liveSessionID()].AcceptsInput() {
			binds = append(binds, promptKeys.Submit)
		}
		return append(binds, promptKeys.Read, sessionKeys.Raw)
	}
	multi := c.m.isMultiQuestion()
	binds := []binding{promptKeys.Up}
	if multi {
		binds = append(binds, promptKeys.TabPrev, promptKeys.Next)
	} else {
		binds = append(binds, promptKeys.Submit)
	}
	if q := d.activeQuestion(c); q != nil && q.AllowNotes && !d.onSubmitTab(c) {
		binds = append(binds, promptKeys.Note)
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

// dockShown reports whether the session region shows: the main pane shows a
// live session, under an open file or not, and that session still exists.
func (m model) dockShown() bool {
	if !m.inSession() {
		return false
	}
	_, ok := m.sessions[m.liveSessionID()]
	return ok
}

// inputShown reports whether the region's body is the input itself, not a
// prompt or the notice that argus cannot send to the session.
func (m model) inputShown() bool {
	return m.composing() && m.sessions[m.liveSessionID()].AcceptsInput()
}

// composing reports whether the region holds the input: nothing is pending
// but, at most, an idle prompt.
func (m model) composing() bool {
	ix := m.sessionInteraction()
	return ix == nil || ix.Kind == session.InteractionIdle
}

// dockRules are the region's top and bottom rules.
func (m model) dockRules(w int, c color.Color) (top, bottom string) {
	return dockRule("", "", w, c), dockRule("", "", w, c)
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

func (d dockComp) wheel(c *ctx, delta int) (component, tea.Cmd) {
	if c.m.focused != sessionDock {
		return d, nil
	}
	maxScroll, _ := d.dockScrollGeom(c, d.dockHeight(c, c.m.sessionRows())-dockChrome)
	d.scroll = max(0, min(min(d.scroll, maxScroll)+delta, maxScroll))
	return d, nil
}
