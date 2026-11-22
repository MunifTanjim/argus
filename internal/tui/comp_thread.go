package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// threadComp is an agent thread's timeline: its sessions' transcripts with
// Argus's events in between, drawn by an embedded past-session transcript.
type threadComp struct {
	agentID  string
	threadID string
	title    string
	tr       transcriptComp
	sessions []api.PAgentTimelineSession
	loaded   bool
	readPos  int64 // the last read position sent
	reply    chatPrompt
}

// threadTimelineMsg is one load of a thread's timeline.
type threadTimelineMsg struct {
	threadID string
	res      api.PAgentTimeline
	err      error
}

// threadActionMsg is the reply to a thread action: a send or an undo.
type threadActionMsg struct {
	threadID string
	verb     string
	err      error
}

func newThreadComp(t api.PAgentThreadInfo) threadComp {
	return threadComp{agentID: t.AgentID, threadID: t.ID, title: t.Title, tr: newTranscript()}
}

// openThread opens a thread's timeline over the agent view.
func openThread(c *ctx, t api.PAgentThreadInfo) tea.Cmd {
	c.open(newThreadComp(t))
	c.focusOn(mainPane)
	return c.m.loadTimelineCmd(t.ID)
}

func (m model) loadTimelineCmd(threadID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.PAgentTimeline
		err := client.Call(api.MethodPAgentThreadTimeline, api.PAgentTimelineParams{ThreadID: threadID}, &res)
		return threadTimelineMsg{threadID: threadID, res: res, err: err}
	}
}

func (m model) markThreadReadCmd(threadID string, pos int64) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodPAgentThreadMarkRead, api.PAgentMarkReadParams{ThreadID: threadID, Position: pos}, nil)
		return nil
	}
}

func (m model) sendThreadCmd(agentID, threadID, text string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodPAgentThreadSend, api.PAgentSendParams{AgentID: agentID, ThreadID: threadID, Text: text}, &api.PAgentSendResult{})
		return threadActionMsg{threadID: threadID, verb: "send", err: err}
	}
}

func (m model) undoMemoryCmd(agentID, threadID, commit string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodPAgentMemoryUndo, api.PAgentMemoryUndoParams{AgentID: agentID, Commit: commit}, &api.PAgentMemoryUndoResult{})
		return threadActionMsg{threadID: threadID, verb: "undo", err: err}
	}
}

// offerCmd starts or dismisses an offer in thread.
func (m model) offerCmd(threadID, offerID string, start bool) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		p := api.PAgentOfferParams{ThreadID: threadID, OfferID: offerID}
		if start {
			err := client.Call(api.MethodPAgentOfferStart, p, &api.PAgentSendResult{})
			return threadActionMsg{threadID: threadID, verb: "start offer", err: err}
		}
		err := client.Call(api.MethodPAgentOfferDismiss, p, nil)
		return threadActionMsg{threadID: threadID, verb: "dismiss offer", err: err}
	}
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
	return m, tea.Batch(cmd, m.apply(c))
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

func (t threadComp) section() string {
	if t.tr.visualActive() {
		return "visual"
	}
	return "thread"
}

func (t threadComp) raw(c *ctx) bool             { return t.reply.on() || t.tr.raw(c) }
func (t threadComp) spins(*ctx) bool             { return false }
func (t threadComp) fullScreen(*ctx) fullLevel   { return notFull }
func (t threadComp) close(*ctx) tea.Cmd          { return nil }
func (t threadComp) offers(*ctx) []binding       { return nil }
func (t threadComp) layer() layer                { return baseLayer }
func (t threadComp) pageStep(c *ctx) int         { return c.m.listPageStep() }
func (t threadComp) visualActive() bool          { return t.tr.visualActive() }
func (t *threadComp) bind(c *ctx) tview          { return t.tr.bind(c) }
func (t threadComp) liveSession() (string, bool) { return lastLive(t.sessions) }

func lastLive(ss []api.PAgentTimelineSession) (string, bool) {
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

func (t threadComp) footer(*ctx) []binding {
	return []binding{transcriptKeys.PromptNext, transcriptKeys.Collapse, sessionKeys.FocusPrompt, threadKeys.OpenSession,
		memoryKeys.Undo, transcriptKeys.Back}
}

func (t threadComp) footerPrompt(*ctx) string {
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
		a, _ := c.m.pagent(t.agentID)
		t.tr.sourceAt = timelineSource(a.NodeID, msg.res.Sessions)
		if pos := lastEntryTime(msg.res.Entries); pos > t.readPos {
			t.readPos = pos
			return t, c.m.markThreadReadCmd(t.threadID, pos)
		}
	case threadActionMsg:
		if msg.err != nil {
			c.setFlash(msg.verb + ": " + msg.err.Error())
		}
		return t, c.m.loadTimelineCmd(t.threadID)
	default:
		return t, t.bind(c).updateMsg(msg)
	}
	return t, nil
}

// timelineSource maps a timeline entry to the session it was read from: its
// id is prefixed "s<index>:". The live session that writes that transcript, if
// any, serves its tool details; else the file does.
func timelineSource(nodeID string, ss []api.PAgentTimelineSession) func(string) (entrySource, bool) {
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

func lastEntryTime(es []transcript.Entry) int64 {
	for i := len(es) - 1; i >= 0; i-- {
		if ts, err := time.Parse(time.RFC3339Nano, es[i].Timestamp); err == nil {
			return ts.UnixNano()
		}
	}
	return 0
}

func (t threadComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m := c.m
	if t.reply.on() {
		var cmd tea.Cmd
		var text string
		t.reply, cmd, text = t.reply.handleKey(msg)
		if text != "" {
			cmd = m.sendThreadCmd(t.agentID, t.threadID, text)
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
		if a, ok := m.pagent(t.agentID); ok && a.Status == pagentUnreachable {
			c.setFlash(agentOffline)
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
		if commit == "" {
			c.setFlash("not a change to undo")
			return t, nil, true
		}
		return t, m.undoMemoryCmd(t.agentID, t.threadID, commit), true
	case m.matches(msg, offerKeys.Start, offerKeys.Dismiss) && t.draftAt(v) != 0:
		return t, m.draftCmd(t.threadID, t.draftAt(v), m.matches(msg, offerKeys.Start)), true
	case m.matches(msg, offerKeys.Start, offerKeys.Dismiss):
		r, ok := v.cursorRow()
		id := ""
		if ok && r.kind == entryRow {
			id = offerID(t.tr.transcript.entries[r.entry])
		}
		if id == "" || !offerOpen(t.tr.transcript.entries, id) {
			c.setFlash("not an open offer")
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
	a, _ := c.m.pagent(t.agentID)
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
