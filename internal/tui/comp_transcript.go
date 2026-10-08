package tui

import (
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// historyKind is a transcript's inner view: the entry stream, or the entry detail.
type historyKind int

const (
	histTranscript historyKind = iota
	histDetail
)

// transcriptComp is live, streamed from an open session, or a past session read
// from history. A live transcript owns its streams and closes them when it
// leaves the main pane.
type transcriptComp struct {
	live        bool
	sessionID   string // live only: the session it streams
	transcript  transcriptState
	historyView historyKind
	activeSub   subRef // the stream on screen: the session's, or a subagent's while drilled in
	sessionSub  subRef // the session's stream while drilled into a subagent
	toolBodies  map[string]toolBodyEntry

	history       historyState // history only
	pendingExport bool
	redact        redactState
}

func newTranscript() transcriptComp {
	return transcriptComp{
		transcript: transcriptState{expanded: map[string]bool{}, rows: map[rowRef]rowEntry{}, runs: map[string]bool{}},
		toolBodies: map[string]toolBodyEntry{},
		redact:     redactState{input: newRedactInput()},
	}
}

func newLiveTranscript(sessionID string) transcriptComp {
	t := newTranscript()
	t.live, t.sessionID = true, sessionID
	return t
}

func newHistoryTranscript(p session.HistoryProject, s session.HistorySession) transcriptComp {
	t := newTranscript()
	t.history = historyState{
		project:       p,
		title:         historySessionTitle(s),
		openSession:   s,
		openNodeID:    p.NodeID,
		openPath:      s.TranscriptPath,
		openAgent:     s.Agent,
		openSessionID: s.SessionID,
		openResumable: s.Resumable,
	}
	return t
}

func (t transcriptComp) section() string       { return "transcript" }
func (t transcriptComp) spins(*ctx) bool       { return false }
func (t transcriptComp) offers(*ctx) []binding { return nil }
func (t transcriptComp) pageStep(c *ctx) int   { return c.m.listPageStep() }
func (t transcriptComp) layer() layer          { return baseLayer }

func (t transcriptComp) raw(*ctx) bool {
	return t.pendingExport || t.redact.pendingSave || t.redact.inputActive
}

func (t transcriptComp) fullScreen(c *ctx) fullLevel {
	if !t.live && c.m.viewer {
		return fullTerminal
	}
	return notFull
}

func (t transcriptComp) close(c *ctx) tea.Cmd { return t.bind(c).closeStreams() }

// commands follows liveKey and historyKey: the redaction list takes every key
// it does not use, and the card detail reads its own keys.
func (t transcriptComp) commands(c *ctx) []binding {
	v := t.bind(c)
	var out []any
	switch {
	case v.redactListActive():
		return bindingsOf(transcriptKeys.Back, redactListKeys, transcriptKeys.Redact)
	case v.redactActive():
		out = append(out, transcriptKeys.Redact, transcriptKeys.RedactList, transcriptKeys.RedactSave)
	case t.live && c.m.sessionInteraction() != nil:
		out = append(out, sessionKeys.Raw, sessionKeys.FocusPrompt)
	case t.live:
		out = append(out, sessionKeys.Raw)
	}
	if t.historyView == histDetail {
		return bindingsOf(append(out, detailKeys)...)
	}
	out = append(out, transcriptViewKeys...)
	if !t.live && !c.m.viewer {
		out = append(out, transcriptKeys.Resume, transcriptKeys.Export)
	}
	return bindingsOf(out...)
}

func (t transcriptComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	v := t.bind(c)
	var cmd tea.Cmd
	if t.live {
		cmd = v.liveKey(msg)
	} else {
		cmd = v.historyKey(msg)
	}
	return t, cmd, true
}

func (t transcriptComp) click(c *ctx, h hitTarget, focused bool) (component, tea.Cmd) {
	v := t.bind(c)
	var cmd tea.Cmd
	switch {
	case h.kind == hitClose:
		cmd = v.detailBack()
	case t.historyView == histDetail:
		cmd = v.clickItem(h.index, focused)
	case h.kind == hitFold:
		cmd = v.clickFold(h.index)
	default:
		cmd = v.clickEntry(h.index, focused)
	}
	return t, cmd
}

func (t transcriptComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	v := t.bind(c)
	if v.redactListActive() {
		return t, nil
	}
	if t.historyView == histDetail {
		v.wheelDetail(d)
	} else {
		v.wheelEntries(d)
	}
	return t, nil
}

func (t transcriptComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	cmd := t.bind(c).updateMsg(msg)
	return t, cmd
}

// view ignores h: the cards and the scroll math read the frame's layout, which
// holds the same size.
func (t transcriptComp) view(c *ctx, w, _ int) string {
	if !t.live {
		return t.bind(c).historyTranscriptView()
	}
	head := t.fileHeader(c, w)
	if c.m.sessions[t.sessionID].Status == session.StatusStarting {
		return head + "\n\n" + startingNotice(*c.m)
	}
	return head + "\n\n" + t.bind(c.below(lipgloss.Height(head)+1)).historyBody()
}

// fileHeader stays over a file opened on the transcript.
func (t transcriptComp) fileHeader(c *ctx, w int) string {
	if !t.live {
		return ""
	}
	return sessionHeader(c.m.sessions[t.sessionID], w, c.m.paneHeadStyle())
}

func (t transcriptComp) footerPrompt(c *ctx) string {
	if t.live {
		return ""
	}
	if s := t.bind(c).redactPrompt(); s != "" {
		return s
	}
	return exportPrompt(t.pendingExport)
}

func (t transcriptComp) footerText(c *ctx) string {
	base := c.m.footer(t.footer(c)...)
	if t.live {
		return base
	}
	return t.bind(c).redactFooter(base)
}

func (t transcriptComp) footer(c *ctx) []binding {
	v := t.bind(c)
	if !t.live {
		return v.historyTranscriptBinds()
	}
	if t.historyView == histDetail {
		return []binding{detailKeys.Collapse, detailKeys.Drill, sessionKeys.Raw}
	}
	binds := []binding{transcriptKeys.PromptNext, transcriptKeys.Collapse, transcriptKeys.Detail, transcriptKeys.Bottom}
	if v.c.m.sessionInteraction() != nil {
		binds = append(binds, transcriptKeys.Answer)
	}
	if v.c.m.sessions[t.sessionID].Status == session.StatusStarting {
		binds = append(binds, sessionKeys.Raw)
	}
	return binds
}

// tview is a transcript bound to the context of one component call.
type tview struct {
	*transcriptComp
	c *ctx
}

func (t *transcriptComp) bind(c *ctx) tview { return tview{t, c} }

func (m tview) liveKey(msg tea.KeyPressMsg) tea.Cmd {
	m.c.setFlash("")
	switch {
	case m.c.m.matches(msg, sessionKeys.FocusPrompt):
		if m.c.m.sessionInteraction() != nil {
			m.c.focusOn(sessionDock)
		}
		return nil
	case m.c.m.matches(msg, sessionKeys.Raw):
		return openLiveScreen(m.c)
	}
	if m.c.m.matches(msg, transcriptKeys.Back) {
		if m.historyView == histDetail {
			return m.detailBack()
		}
		m.c.back()
		return nil
	}
	if m.historyView == histDetail {
		return m.handleDetailKey(msg)
	}
	return m.handleTranscriptKey(msg)
}

// detailBack pops one detail frame; popping the root returns to the stream.
func (m tview) detailBack() tea.Cmd {
	// A leaf frame above a subagent frame has no subID and pops normally, so
	// the subagent subscription lives until its own frame pops.
	if f := m.topFrame(); f != nil && f.subID != "" {
		cmd := m.c.m.unsubscribeCmd(f.subID)
		m.activeSub = m.sessionSub
		m.sessionSub = subRef{}
		// Re-subscribe to catch deltas missed while drilled in.
		have := len(m.c.m.transcriptCache[m.activeSub.key()].entries)
		if m.popDetail() {
			m.historyView = histTranscript // a live subagent drilled from the stream is the root frame
		}
		return tea.Batch(cmd, m.c.m.subscribeCmd(m.activeSub, have))
	}
	if m.popDetail() {
		m.historyView = histTranscript
	}
	return nil
}

func (m tview) historyKey(msg tea.KeyPressMsg) tea.Cmd {
	m.c.setFlash("") // any key dismisses a transient flash; the action may re-set it
	if cmd, ok := m.takePendingExport(msg); ok {
		return cmd
	}
	if cmd, ok := m.takePendingRedactSave(msg); ok {
		return cmd
	}
	if cmd, ok := m.handleRedactKey(msg); ok {
		return cmd
	}
	if m.c.m.matches(msg, transcriptKeys.Back) {
		if m.historyView == histDetail {
			return m.detailBack()
		}
		if m.c.m.viewer {
			return tea.Quit
		}
		m.c.back()
		return nil
	}
	if m.historyView == histTranscript && !m.c.m.viewer && m.c.m.matches(msg, transcriptKeys.Resume) {
		h := m.history
		return historyResume(m.c, h.openResumable, h.openNodeID, h.openAgent, h.openSessionID, h.project.Cwd)
	}
	if m.historyView == histDetail {
		return m.handleDetailKey(msg)
	}
	if m.c.m.matches(msg, transcriptKeys.Export) && m.historyView == histTranscript {
		if !m.c.m.viewer {
			m.pendingExport = true
		}
		return nil
	}
	return m.handleTranscriptKey(msg)
}

func (m tview) closeStreams() tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range []subRef{m.activeSub, m.sessionSub} {
		if s.subID != "" {
			cmds = append(cmds, m.c.m.unsubscribeCmd(s.subID))
		}
	}
	m.activeSub, m.sessionSub = subRef{}, subRef{}
	return tea.Batch(cmds...)
}

func (m tview) updateMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case transcriptMsg:
		prevID, wasLast := m.currentRowID()
		// Tail-follow only if the view was already pinned to the bottom.
		atBottom := m.transcript.scroll >= m.maxScroll()
		m.setEntries(msg.entries)
		m.transcript.err = msg.err
		m.restoreEntryCursor(prevID, atBottom, wasLast)
	case transcriptDeltaMsg:
		if msg.ref.agentID != "" {
			// Match by subID, not topFrame(): the user may have drilled into a leaf above it.
			for i := range m.transcript.detailStack {
				if f := &m.transcript.detailStack[i]; f.subID == msg.delta.SubID {
					m.followFrame(f, m.c.m.transcriptCache[msg.ref.key()].entries)
					break
				}
			}
			return m.refetchStaleToolBodies(msg.delta.Entries, msg.ref.agentID)
		}
		prevID, wasLast := m.currentRowID()
		atBottom := m.transcript.scroll >= m.maxScroll()
		cmd := m.applyEntryDelta(msg.delta)
		m.restoreEntryCursor(prevID, atBottom, wasLast)
		return cmd
	case histTranscriptMsg:
		m.setEntries(msg.entries)
		m.transcript.err = msg.err
		m.transcript.cursor, m.transcript.scroll = 0, 0
	case histSubagentMsg:
		// Match by agentID, not topFrame(): the user may have drilled into a leaf above this frame.
		if msg.err == nil {
			for i := len(m.transcript.detailStack) - 1; i >= 0; i-- {
				if m.transcript.detailStack[i].agentID == msg.agentID {
					m.transcript.detailStack[i].items = msg.entries
					break
				}
			}
		}
	case toolDetailMsg:
		// On error, file a done entry with empty body so the placeholder clears and we don't retry.
		e := toolBodyEntry{done: true}
		if msg.err == nil {
			e.toolInput, e.result, e.resultIsError = msg.detail.ToolInput, msg.detail.Result, msg.detail.ResultIsError
		}
		m.toolBodies[msg.toolID] = e
		for _, en := range m.transcript.entries {
			if en.ToolID == msg.toolID {
				delete(m.transcript.rows, rowRef{id: en.ID})
			}
		}
	case tea.PasteMsg:
		var cmd tea.Cmd
		m.redact.input, cmd = m.redact.input.Update(msg)
		return cmd
	case redactPreparedMsg:
		if msg.err != nil {
			m.c.setFlash("redact failed: " + msg.err.Error())
			return nil
		}
		r := msg.report
		m.redact.report = &r
		m.redact.tempPath = msg.tempPath
		m.redact.outPath = msg.outPath
		m.redact.pendingSave = true
		m.redact.warnConfirm = len(r.Warnings) > 0 // extra ack when content can't be scrubbed
	case redactDoneMsg:
		switch {
		case msg.err != nil:
			m.c.setFlash("redact failed: " + msg.err.Error())
		case msg.sidecar != "":
			m.c.setFlash(fmt.Sprintf("redacted with %d warning(s) (secrets remain); see %s",
				len(msg.warnings), filepath.Base(msg.sidecar)))
		case len(msg.warnings) > 0:
			m.c.setFlash(fmt.Sprintf("redacted with %d warning(s) (secrets remain): %s", len(msg.warnings), msg.path))
		default:
			m.c.setFlash("redacted: " + msg.path)
		}
		m.redact.report = nil
		m.redact.warnConfirm = false
		m.redact.tempPath = ""
	}
	return nil
}

func isHistory(t transcriptComp) bool { return !t.live }
func isLive(t transcriptComp) bool    { return t.live }

// transcriptOwner names the transcript a reply belongs to: a live session, or
// the address a past session's reads go to.
type transcriptOwner struct {
	live      bool
	sessionID string
	addr      histAddr
}

func (t transcriptComp) owner() transcriptOwner {
	if t.live {
		return transcriptOwner{live: true, sessionID: t.sessionID}
	}
	return transcriptOwner{addr: t.history.addr()}
}

func streams(subID string) func(transcriptComp) bool {
	return func(t transcriptComp) bool { return t.live && t.activeSub.subID == subID }
}

func (m model) transcriptAt(match func(transcriptComp) bool) int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if t, ok := m.main[i].(transcriptComp); ok && match(t) {
			return i
		}
	}
	return -1
}

func (m model) updateTranscript(msg tea.Msg, match func(transcriptComp) bool) (tea.Model, tea.Cmd) {
	i := m.transcriptAt(match)
	if i < 0 {
		return m, nil
	}
	c := &ctx{m: &m}
	comp, cmd := m.main[i].update(c, msg)
	m.main = m.main.replaceAt(i, comp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (m model) updateDelta(msg transcriptDeltaMsg) (tea.Model, tea.Cmd) {
	i := m.transcriptAt(streams(msg.ref.subID))
	if i < 0 {
		return m, nil
	}
	key := msg.ref.key()
	if msg.ref.agentID != "" {
		m.transcriptCache[key] = cachedTranscript{entries: applyDelta(m.transcriptCache[key].entries, msg.delta)}
		return m.updateTranscript(msg, streams(msg.ref.subID))
	}
	res, cmd := m.updateTranscript(msg, streams(msg.ref.subID))
	m = res.(model)
	m.transcriptCache[key] = cachedTranscript{entries: m.main[i].(transcriptComp).transcript.entries}
	return m, cmd
}

func (m *model) editTranscript(i int, f func(v tview) tea.Cmd) tea.Cmd {
	t, ok := m.main[i].(transcriptComp)
	if !ok {
		return nil
	}
	c := &ctx{m: m}
	cmd := f(t.bind(c))
	m.main = m.main.replaceAt(i, t)
	return tea.Batch(cmd, m.apply(c))
}

// toggleVerboseTranscript flips the default run expansion, keeping each cursor
// (stream and frames) on the same row.
func (m *model) toggleVerboseTranscript() tea.Cmd {
	type cursors struct {
		stream rowRef
		frames []rowRef
	}
	var saved []cursors
	for i := range m.main {
		if t, ok := m.main[i].(transcriptComp); ok {
			v := t.bind(&ctx{m: m})
			ref, _ := v.currentRowID()
			c := cursors{stream: ref}
			for j := range t.transcript.detailStack {
				c.frames = append(c.frames, v.frameRowID(&t.transcript.detailStack[j]))
			}
			saved = append(saved, c)
		}
	}
	m.verboseTranscript = !m.verboseTranscript
	m.flash = "verbose transcript off"
	if m.verboseTranscript {
		m.flash = "verbose transcript on"
	}
	var cmds []tea.Cmd
	n := 0
	for i := range m.main {
		if _, ok := m.main[i].(transcriptComp); !ok {
			continue
		}
		c := saved[n]
		n++
		cmds = append(cmds, m.editTranscript(i, func(v tview) tea.Cmd {
			clear(v.transcript.rows)
			v.transcript.cursor = restoreRowCursor(v.transcript.entries, v.displayRows(), c.stream, 0, false)
			for j := range v.transcript.detailStack {
				f := &v.transcript.detailStack[j]
				f.cursor = restoreRowCursor(f.items, v.frameRows(f), c.frames[j], f.cursor, false)
			}
			if v.historyView == histDetail {
				v.ensureDetailVisible()
			} else {
				v.ensureEntryVisible()
			}
			return nil
		}))
	}
	return tea.Batch(cmds...)
}

// redactTyping reports whether the main pane's transcript takes a secret as
// typed.
func (m model) redactTyping() bool {
	t, ok := m.baseComp().(transcriptComp)
	return ok && t.redact.inputActive
}

func (t transcriptComp) workspace(c *ctx) string {
	if !t.live {
		return ""
	}
	return c.m.sessions[t.sessionID].WorkspaceID
}
