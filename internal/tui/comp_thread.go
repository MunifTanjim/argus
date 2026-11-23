package tui

import (
	"encoding/json"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// threadComp is a bot thread's timeline: its sessions' transcripts with
// Argus's events in between, drawn by an embedded past-session transcript.
type threadComp struct {
	botID    string
	threadID string
	title    string
	tr       transcriptComp
	sessions []api.BotTimelineSession
	loaded   bool
	reply    chatPrompt
	undo     string // the commit that awaits an undo confirmation
}

// threadTimelineMsg is one load of a thread's timeline.
type threadTimelineMsg struct {
	threadID string
	res      api.BotTimeline
	err      error
}

// threadActionMsg is the reply to a thread action: a send or an undo.
type threadActionMsg struct {
	threadID string
	verb     string
	err      error
}

func newThreadComp(t api.BotThreadInfo) threadComp {
	return threadComp{botID: t.BotID, threadID: t.ID, title: t.Title, tr: newTranscript()}
}

// openThread opens a thread's timeline over the bot view.
func openThread(c *ctx, t api.BotThreadInfo) tea.Cmd {
	c.open(newThreadComp(t))
	c.focusOn(mainPane)
	return c.m.loadTimelineCmd(t.ID)
}

func (m model) loadTimelineCmd(threadID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.BotTimeline
		err := client.Call(api.MethodBotThreadTimeline, api.BotTimelineParams{ThreadID: threadID}, &res)
		return threadTimelineMsg{threadID: threadID, res: res, err: err}
	}
}

func (m model) markThreadReadCmd(threadID string, pos int64) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodBotThreadMarkRead, api.BotMarkReadParams{ThreadID: threadID, Position: pos}, nil)
		return nil
	}
}

func (m model) sendThreadCmd(botID, threadID, text string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodBotThreadSend, api.BotSendParams{BotID: botID, ThreadID: threadID, Text: text}, &api.BotSendResult{})
		return threadActionMsg{threadID: threadID, verb: "send", err: err}
	}
}

func (m model) undoMemoryCmd(botID, threadID, commit string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		p := api.BotMemoryUndoParams{BotID: botID, Commit: commit, ThreadID: threadID}
		err := client.Call(api.MethodBotMemoryUndo, p, &api.BotMemoryUndoResult{})
		return threadActionMsg{threadID: threadID, verb: "undo", err: err}
	}
}

// offerCmd starts or dismisses an offer in thread.
func (m model) offerCmd(threadID, offerID string, start bool) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		p := api.BotOfferParams{ThreadID: threadID, OfferID: offerID}
		if start {
			err := client.Call(api.MethodBotOfferStart, p, &api.BotSendResult{})
			return threadActionMsg{threadID: threadID, verb: "start offer", err: err}
		}
		err := client.Call(api.MethodBotOfferDismiss, p, nil)
		return threadActionMsg{threadID: threadID, verb: "dismiss offer", err: err}
	}
}

// markTopThreadReadCmd marks the thread on top of the main stack read up to
// its last activity, when the node reports it unread. A thread under another
// view is not marked: the user is not looking at it.
func (m model) markTopThreadReadCmd() tea.Cmd {
	t, ok := m.main.top().(threadComp)
	if !ok {
		return nil
	}
	for _, info := range m.botThreads[t.botID] {
		if info.ID == t.threadID && info.Unread {
			return m.markThreadReadCmd(t.threadID, info.LastActiveAt.UnixNano())
		}
	}
	return nil
}

// reloadThreadViewsCmd reloads every open thread's timeline.
func (m model) reloadThreadViewsCmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, comp := range m.main {
		if t, ok := comp.(threadComp); ok {
			cmds = append(cmds, m.loadTimelineCmd(t.threadID))
		}
	}
	return tea.Batch(cmds...)
}

func (m model) updateThreadView(msg tea.Msg, threadID string) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	var cmd tea.Cmd
	for i := len(m.main) - 1; i >= 0; i-- {
		if t, ok := m.main[i].(threadComp); ok && t.threadID == threadID {
			var comp component
			comp, cmd = t.update(c, msg)
			m.main = m.main.replaceAt(i, comp)
			break
		}
	}
	applied := m.apply(c)
	if _, ok := msg.(threadTimelineMsg); ok {
		cmd = tea.Batch(cmd, m.markTopThreadReadCmd())
	}
	return m, tea.Batch(cmd, applied)
}

// undoCommit is the commit of a memory or routine change that an argus entry
// reports, or "".
func undoCommit(e transcript.Entry) string {
	if e.Kind != transcript.EntryArgus || e.Event == nil || (e.Event.Kind != "memory.changed" && e.Event.Kind != "routine.changed") {
		return ""
	}
	var p struct {
		Commit string `json:"commit"`
	}
	_ = json.Unmarshal(e.Event.Payload, &p)
	return p.Commit
}

// undoneCommit reports whether an entry records the undo of commit.
func undoneCommit(es []transcript.Entry, commit string) bool {
	for _, e := range es {
		if e.Event == nil || e.Event.Kind != "memory.undone" {
			continue
		}
		var p struct {
			Commit string `json:"commit"`
		}
		if json.Unmarshal(e.Event.Payload, &p) == nil && p.Commit != "" &&
			(strings.HasPrefix(p.Commit, commit) || strings.HasPrefix(commit, p.Commit)) {
			return true
		}
	}
	return false
}

// openUndo is the commit of a change an entry reports that is not undone yet,
// or "".
func openUndo(es []transcript.Entry, e transcript.Entry) string {
	if c := undoCommit(e); c != "" && !undoneCommit(es, c) {
		return c
	}
	return ""
}

// undoAt is the commit of the open change under the cursor, or "".
func (t threadComp) undoAt(v tview) string {
	r, ok := v.cursorRow()
	if !ok || r.kind != entryRow {
		return ""
	}
	es := t.tr.transcript.entries
	return openUndo(es, es[r.entry])
}

func (t threadComp) section() string {
	if t.tr.visualActive() {
		return "visual"
	}
	return "thread"
}

func (t threadComp) raw(c *ctx) bool             { return t.reply.on() || t.undo != "" || t.tr.raw(c) }
func (t threadComp) spins(*ctx) bool             { return false }
func (t threadComp) fullScreen(*ctx) fullLevel   { return notFull }
func (t threadComp) close(*ctx) tea.Cmd          { return nil }
func (t threadComp) offers(*ctx) []binding       { return nil }
func (t threadComp) layer() layer                { return baseLayer }
func (t threadComp) pageStep(c *ctx) int         { return c.m.listPageStep() }
func (t threadComp) visualActive() bool          { return t.tr.visualActive() }
func (t *threadComp) bind(c *ctx) tview          { return t.tr.bind(c) }
func (t threadComp) liveSession() (string, bool) { return lastLive(t.sessions) }

func lastLive(ss []api.BotTimelineSession) (string, bool) {
	for i := len(ss) - 1; i >= 0; i-- {
		if ss[i].Live {
			return ss[i].SessionID, true
		}
	}
	return "", false
}

func (t threadComp) commands(c *ctx) []binding {
	if t.tr.visualActive() {
		return bindingsOf(visualKeys)
	}
	if t.tr.historyView == histDetail {
		return bindingsOf(detailKeys)
	}
	return sectionLists["thread"].own
}

func (t threadComp) footer(c *ctx) []binding {
	b := []binding{transcriptKeys.PromptNext, transcriptKeys.Collapse, sessionKeys.FocusPrompt, threadKeys.OpenSession}
	if t.undoAt(t.bind(c)) != "" {
		b = append(b, memoryKeys.Undo)
	}
	return append(b, transcriptKeys.Back)
}

func (t threadComp) footerPrompt(*ctx) string {
	if t.undo != "" {
		return asstStyle.Render("undo change " + shortHash(t.undo) + "? y/n")
	}
	if t.reply.on() {
		return t.reply.view("reply")
	}
	return ""
}

func (t threadComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case threadTimelineMsg:
		v := t.bind(c)
		if msg.err != nil {
			t.tr.transcript.err = msg.err
			return t, nil
		}
		prevID, wasLast := v.currentRowID()
		atBottom := v.streamFollows() || !t.loaded
		v.setEntries(msg.res.Entries)
		v.transcript.err = nil
		v.restoreEntryCursor(prevID, atBottom, wasLast || !t.loaded)
		t.sessions, t.loaded = msg.res.Sessions, true
		a, _ := c.m.bot(t.botID)
		t.tr.sourceAt = timelineSource(a.NodeID, msg.res.Sessions)
	case threadActionMsg:
		if msg.err != nil {
			c.setFlash(msg.verb + ": " + msg.err.Error())
		} else if msg.verb == "undo" {
			c.setFlash("undone")
		}
		return t, c.m.loadTimelineCmd(t.threadID)
	case tea.PasteMsg:
		if t.reply.on() {
			var cmd tea.Cmd
			t.reply, cmd = t.reply.paste(msg)
			return t, cmd
		}
		return t, t.bind(c).updateMsg(msg)
	default:
		return t, t.bind(c).updateMsg(msg)
	}
	return t, nil
}

// timelineSource maps a timeline entry to the session it was read from: its
// id is prefixed "s<index>:". The live session that writes that transcript, if
// any, serves its tool details; else the file does.
func timelineSource(nodeID string, ss []api.BotTimelineSession) func(string) (entrySource, bool) {
	return func(id string) (entrySource, bool) {
		n, _, ok := strings.Cut(strings.TrimPrefix(id, "s"), ":")
		i, err := strconv.Atoi(n)
		if !ok || !strings.HasPrefix(id, "s") || err != nil || i < 0 || i >= len(ss) || ss[i].TranscriptPath == "" {
			return entrySource{}, false
		}
		s := ss[i]
		src := entrySource{addr: histAddr{nodeID: nodeID, path: s.TranscriptPath, agent: s.CLI}}
		for j := len(ss) - 1; j >= 0; j-- {
			if ss[j].Live && ss[j].TranscriptPath == s.TranscriptPath {
				src.live, src.sessionID = true, ss[j].SessionID
				break
			}
		}
		return src, true
	}
}

func (t threadComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m := c.m
	if commit := t.undo; commit != "" {
		t.undo = ""
		if msg.String() == "y" {
			return t, m.undoMemoryCmd(t.botID, t.threadID, commit), true
		}
		return t, nil, true
	}
	if t.reply.on() {
		var cmd tea.Cmd
		var text string
		t.reply, cmd, text = t.reply.handleKey(msg)
		if text != "" {
			cmd = m.sendThreadCmd(t.botID, t.threadID, text)
		}
		return t, cmd, true
	}
	v := t.bind(c)
	if t.tr.visualActive() || t.tr.historyView == histDetail {
		var cmd tea.Cmd
		if t.tr.visualActive() {
			cmd = v.visualKey(msg)
		} else if m.matches(msg, transcriptKeys.Back) {
			cmd = v.detailBack()
		} else {
			cmd = v.handleDetailKey(msg)
		}
		v.dropStaleVisual()
		return t, cmd, true
	}
	c.setFlash("")
	switch {
	case m.matches(msg, transcriptKeys.Back):
		c.back()
	case m.matches(msg, sessionKeys.FocusPrompt):
		if a, ok := m.bot(t.botID); ok && a.Status == botUnreachable {
			c.setFlash(botOffline)
			return t, nil, true
		}
		var cmd tea.Cmd
		t.reply, cmd = t.reply.start(t.threadID)
		return t, cmd, true
	case m.matches(msg, threadKeys.OpenSession):
		if id, ok := t.liveSession(); ok {
			c.openSession(id)
		} else {
			c.setFlash("no live session in this thread")
		}
	case m.matches(msg, memoryKeys.Undo):
		r, ok := v.cursorRow()
		commit := ""
		if ok && r.kind == entryRow {
			commit = undoCommit(t.tr.transcript.entries[r.entry])
		}
		switch {
		case commit == "":
			c.setFlash("not a change to undo")
		case undoneCommit(t.tr.transcript.entries, commit):
			c.setFlash("change already undone")
		default:
			t.undo = commit
		}
		return t, nil, true
	case m.matches(msg, offerKeys.Start, offerKeys.Dismiss) && t.draftAt(v) != 0:
		return t, m.draftCmd(t.threadID, t.draftAt(v), m.matches(msg, offerKeys.Start)), true
	case m.matches(msg, offerKeys.Start, offerKeys.Dismiss):
		r, ok := v.cursorRow()
		id := ""
		if ok && r.kind == entryRow {
			id = offerID(t.tr.transcript.entries[r.entry])
		}
		if id == "" || !offerOpen(t.tr.transcript.entries, id) {
			c.setFlash(t.closedEntryFlash(v))
			return t, nil, true
		}
		return t, m.offerCmd(t.threadID, id, m.matches(msg, offerKeys.Start)), true
	case m.matches(msg, listKeys.Refresh):
		return t, m.loadTimelineCmd(t.threadID), true
	default:
		cmd := v.handleTranscriptKey(msg)
		v.dropStaleVisual()
		return t, cmd, true
	}
	return t, nil, true
}

func (t threadComp) click(c *ctx, h hitTarget, focused bool) (component, tea.Cmd) {
	comp, cmd := t.tr.click(c, h, focused)
	t.tr = comp.(transcriptComp)
	return t, cmd
}

func (t threadComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	comp, cmd := t.tr.wheel(c, d)
	t.tr = comp.(transcriptComp)
	return t, cmd
}

func (t threadComp) paneHeader(c *ctx, _ int) (string, int) {
	a, _ := c.m.bot(t.botID)
	return c.m.paneHeadStyle().Render(a.Name) + dimStyle.Render(" · ") + t.headTitle(), c.m.transcriptTextX()
}

func (t threadComp) headTitle() string {
	if t.title == "" {
		return "untitled"
	}
	return t.title
}

func (t threadComp) view(c *ctx, _, _ int) string {
	m := c.m
	head := StyleSecondaryBold.Render(t.headTitle())
	if !t.loaded && t.tr.transcript.err == nil {
		return m.transcriptText(head + "\n\n" + dimStyle.Render("loading thread…"))
	}
	header := m.transcriptText(head)
	return header + "\n\n" + t.bind(c.below(2)).historyBody()
}
