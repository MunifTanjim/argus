package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// agentComp is the agent view: one persistent agent's threads, routines and
// memory. The agent and its threads are read from the model each time.
type agentComp struct {
	agentID string
	cursor  int
	loaded  bool
	err     error
	memory  *agentMemoryUsage // nil: the node has no memory to show
	chat    chatPrompt
}

// agentMemoryUsage is the size of the agent's core memory, its budget, and
// when it last changed (zero when unknown).
type agentMemoryUsage struct {
	usage, budget int64
	last          time.Time
}

const agentOffline = "agent's node is offline"

// agentViewMsg is one load of an agent view.
type agentViewMsg struct {
	agentID string
	threads []api.PAgentThreadInfo
	memory  *agentMemoryUsage
	err     error
}

// agentSentMsg is the reply to a new chat.
type agentSentMsg struct {
	agentID string
	res     api.PAgentSendResult
	err     error
}

// agentPrimaryMsg is the reply to a primary change.
type agentPrimaryMsg struct {
	agentID string
	err     error
}

func openAgent(c *ctx, id string) tea.Cmd {
	c.replaceBase(agentComp{agentID: id})
	c.focusOn(mainPane)
	return c.m.loadAgentViewCmd(id)
}

// openThread opens a thread: its live session for now.
func openThread(c *ctx, t api.PAgentThreadInfo) {
	if t.SessionID != "" {
		c.openSession(t.SessionID)
		return
	}
	c.setFlash("thread view comes next")
}

// openAgentMemory opens the agent's memory view.
func openAgentMemory(c *ctx, _ string) { c.setFlash("memory view comes next") }

// loadAgentViewCmd reads the agent's threads and memory usage. An unreachable
// agent's node is not asked.
func (m model) loadAgentViewCmd(id string) tea.Cmd {
	if a, ok := m.pagent(id); ok && a.Status == pagentUnreachable {
		return nil
	}
	client := m.client
	return func() tea.Msg {
		msg := agentViewMsg{agentID: id}
		var tr api.PAgentThreadsResult
		if err := client.Call(api.MethodPAgentThreadList, api.PAgentRef{AgentID: id}, &tr); err != nil && !api.IsMethodNotFound(err) {
			msg.err = err
		}
		msg.threads = tr.Threads
		var mem api.PAgentMemoryShowResult
		if client.Call(api.MethodPAgentMemoryShow, api.PAgentRef{AgentID: id}, &mem) == nil {
			msg.memory = &agentMemoryUsage{usage: mem.Usage, budget: mem.Budget}
			var log api.PAgentMemoryLogResult
			if client.Call(api.MethodPAgentMemoryLog, api.PAgentMemoryLogParams{AgentID: id, Limit: 1}, &log) == nil && len(log.Commits) > 0 {
				msg.memory.last = log.Commits[0].When
			}
		}
		return msg
	}
}

func (m model) sendAgentChatCmd(id, text string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.PAgentSendResult
		err := client.Call(api.MethodPAgentThreadSend, api.PAgentSendParams{AgentID: id, Text: text, New: true}, &res)
		return agentSentMsg{agentID: id, res: res, err: err}
	}
}

// setAgentPrimaryCmd makes the agent primary, or unsets the primary agent
// when id is empty.
func (m model) setAgentPrimaryCmd(agentID, id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodPAgentSetPrimary, api.PAgentPrimaryParams{AgentID: id}, nil)
		return agentPrimaryMsg{agentID: agentID, err: err}
	}
}

// updateAgentView gives an agent view's reply to the open view of that agent.
func (m model) updateAgentView(msg tea.Msg, agentID string) (tea.Model, tea.Cmd) {
	if v, ok := msg.(agentViewMsg); ok && v.err == nil {
		if m.pagentThreads == nil {
			m.pagentThreads = map[string][]api.PAgentThreadInfo{}
		}
		m.pagentThreads[agentID] = v.threads
	}
	c := &ctx{m: &m}
	var cmd tea.Cmd
	if i := m.agentViewAt(agentID); i >= 0 {
		var comp component
		comp, cmd = m.main[i].update(c, msg)
		m.main = m.main.replaceAt(i, comp)
	} else {
		cmd = agentReply(c, msg)
	}
	return m, tea.Batch(cmd, m.apply(c))
}

func (m model) agentViewAt(id string) int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if a, ok := m.main[i].(agentComp); ok && a.agentID == id {
			return i
		}
	}
	return -1
}

// agentReply handles the replies that do not need the view open.
func agentReply(c *ctx, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case agentSentMsg:
		if msg.err != nil {
			c.setFlash("send failed: " + msg.err.Error())
			return nil
		}
		openThread(c, api.PAgentThreadInfo{ID: msg.res.ThreadID, AgentID: msg.agentID, SessionID: msg.res.SessionID})
		return c.m.loadPAgentsCmd()
	case agentPrimaryMsg:
		if msg.err != nil {
			c.setFlash("primary: " + msg.err.Error())
		}
		return c.m.loadPAgentsCmd()
	}
	return nil
}

func (a agentComp) section() string           { return "agent" }
func (a agentComp) raw(*ctx) bool             { return a.chat.on() }
func (a agentComp) fullScreen(*ctx) fullLevel { return notFull }
func (a agentComp) close(*ctx) tea.Cmd        { return nil }
func (a agentComp) offers(*ctx) []binding     { return sectionOffers[a.section()].keys }
func (a agentComp) layer() layer              { return baseLayer }
func (a agentComp) pageStep(c *ctx) int       { return c.m.listPageStep() }

func (a agentComp) threads(c *ctx) []api.PAgentThreadInfo { return c.m.pagentThreads[a.agentID] }

func (a agentComp) spins(c *ctx) bool {
	return slices.ContainsFunc(a.threads(c), func(t api.PAgentThreadInfo) bool {
		return t.SessionID != "" && session.Status(t.SessionStatus) == session.StatusWorking
	})
}

func (a agentComp) info(c *ctx) api.PAgentInfo {
	ag, _ := c.m.pagent(a.agentID)
	return ag
}

// commands leaves out the primary change that would not change anything.
func (a agentComp) commands(c *ctx) []binding {
	drop := agentKeys.MakePrimary
	if !c.m.pagentPrimaryOf(a.info(c)) {
		drop = agentKeys.UnsetPrimary
	}
	return slices.DeleteFunc(slices.Clone(sectionLists[a.section()].own), func(b binding) bool { return b.name == drop.name })
}

func (a agentComp) footer(c *ctx) []binding {
	return []binding{listKeys.Open, agentKeys.NewChat, agentKeys.Memory, listKeys.Back, listKeys.Refresh}
}

func (a agentComp) footerPrompt(*ctx) string {
	if a.chat.on() {
		return a.chat.view()
	}
	return ""
}

func (a agentComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case agentViewMsg:
		a.loaded, a.err, a.memory = true, msg.err, msg.memory
	case agentSentMsg, agentPrimaryMsg:
		cmd := agentReply(c, msg)
		return a, tea.Batch(cmd, c.m.loadAgentViewCmd(a.agentID))
	}
	return a, nil
}

func (a agentComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k := c.m, listKeys
	if a.chat.on() {
		var cmd tea.Cmd
		var text string
		a.chat, cmd, text = a.chat.handleKey(msg)
		if text != "" {
			cmd = m.sendAgentChatCmd(a.agentID, text)
		}
		return a, cmd, true
	}
	threads := a.threads(c)
	n := len(threads)
	a.cursor = min(a.cursor, cursorBottom(n))
	offline := a.info(c).Status == pagentUnreachable
	switch {
	case m.matches(msg, k.Up):
		a.cursor = cursorUp(a.cursor)
	case m.matches(msg, k.Down):
		a.cursor = cursorDown(a.cursor, n)
	case m.matches(msg, k.Top):
		a.cursor = 0
	case m.matches(msg, k.Bottom):
		a.cursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		a.cursor = cursorBy(a.cursor, -a.pageStep(c), n)
	case m.matches(msg, k.HalfDown):
		a.cursor = cursorBy(a.cursor, a.pageStep(c), n)
	case m.matches(msg, k.Open):
		if a.cursor < n {
			openThread(c, threads[a.cursor])
		}
	case m.matches(msg, agentKeys.NewChat, agentKeys.Memory, agentKeys.MakePrimary, agentKeys.UnsetPrimary) && offline:
		c.setFlash(agentOffline)
	case m.matches(msg, agentKeys.NewChat):
		var cmd tea.Cmd
		a.chat, cmd = a.chat.start(a.agentID)
		return a, cmd, true
	case m.matches(msg, agentKeys.Memory):
		openAgentMemory(c, a.agentID)
	case m.matches(msg, agentKeys.MakePrimary):
		return a, m.setAgentPrimaryCmd(a.agentID, a.agentID), true
	case m.matches(msg, agentKeys.UnsetPrimary):
		return a, m.setAgentPrimaryCmd(a.agentID, ""), true
	case m.matches(msg, k.Refresh):
		return a, tea.Batch(m.loadAgentViewCmd(a.agentID), m.loadPAgentsCmd()), true
	case m.matches(msg, k.Back):
		if m.sidebarVisible() {
			c.focusTree()
		} else {
			c.home()
		}
	default:
		return a, nil, false
	}
	return a, nil, true
}

func (a agentComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	threads := a.threads(c)
	switch {
	case t.index >= len(threads):
	case focused && t.index == a.cursor:
		openThread(c, threads[t.index])
	default:
		a.cursor = t.index
	}
	return a, nil
}

func (a agentComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	a.cursor = cursorBy(a.cursor, d, len(a.threads(c)))
	return a, nil
}

func (a agentComp) paneHeader(c *ctx, w int) (string, int) {
	ag := a.info(c)
	parts := []string{StylePrimaryBold.Render(ag.Name)}
	if name, _ := agentLabel(ag.Agent); name != "" {
		parts = append(parts, name)
	} else if ag.Agent != "" {
		parts = append(parts, ag.Agent)
	}
	if ag.NodeLabel != "" {
		parts = append(parts, "home: "+ag.NodeLabel)
	}
	if c.m.pagentPrimaryOf(ag) {
		parts = append(parts, "primary")
	}
	return strings.Join(parts, dimStyle.Render(" · ")), centerGutter(min(w, maxCardWidth), w)
}

func (a agentComp) view(c *ctx, w, h int) string {
	m := c.m
	cardW := min(w, maxCardWidth)
	ag := a.info(c)
	threads := a.threads(c)
	head := StyleSecondaryBold.Render("Threads")
	var body []string
	switch {
	case ag.Status == pagentUnreachable:
		body = append(body, dimStyle.Render(agentOffline))
	case !a.loaded:
		body = append(body, dimStyle.Render("loading threads…"))
	case a.err != nil && len(threads) == 0:
		body = append(body, dimStyle.Render("error: "+a.err.Error()))
	case len(threads) == 0:
		body = append(body, dimStyle.Render("no threads · "+m.keyTextOn(a.section(), agentKeys.NewChat)+" starts a chat"))
	}
	tail := []string{"", StyleSecondaryBold.Render("Routines"), dimStyle.Render("no routines")}
	if u := a.memory; u != nil {
		line := "Memory · " + kbSize(u.usage)
		if u.budget > 0 {
			line += " / " + kbSize(u.budget)
		}
		if !u.last.IsZero() {
			line += " · last change " + agoText(u.last)
		}
		tail = append(tail, "", StyleSecondaryBold.Render(line))
	}
	top := head + "\n"
	if len(body) > 0 {
		return centerBlock(top+strings.Join(append(body, tail...), "\n"), cardW, w)
	}
	// The thread rows take what the other lines leave.
	avail := max(1, h-1-len(tail))
	rows := a.threadRows(c.below(1), threads, cardW, avail)
	return centerBlock(top+rows+"\n"+strings.Join(tail, "\n"), cardW, w)
}

func (a agentComp) threadRows(c *ctx, threads []api.PAgentThreadInfo, w, avail int) string {
	m := c.m
	cursor := min(a.cursor, len(threads)-1)
	focused := m.focused == mainPane && len(m.popups) == 0
	l := itemLines{key: "agent-threads"}
	for i, t := range threads {
		l.add(i, m.threadRow(t, i == cursor && focused, w))
	}
	return strings.Join(l.window(c, cursor, avail), "\n")
}

func (m model) threadRow(t api.PAgentThreadInfo, sel bool, w int) string {
	marker := "  "
	if sel {
		marker = asstStyle.Render("❯ ")
	}
	glyph, gcolor := "○", ColorBorder
	meta := t.Kind + " · " + agoText(t.LastActiveAt)
	if t.SessionID != "" {
		st := session.Status(t.SessionStatus)
		glyph, gcolor = statusGlyph(st), statusColor(st)
		if st == session.StatusWorking {
			glyph, gcolor, meta = SpinnerFrames[m.spin%len(SpinnerFrames)], ColorOngoing, t.Kind+" · working"
		}
	}
	title := t.Title
	if title == "" {
		title = "untitled"
	}
	left := marker + lipgloss.NewStyle().Foreground(gcolor).Render(glyph) + " "
	right := dimStyle.Render(meta)
	mark := ""
	if t.Unread {
		mark = unreadMark
	}
	room := max(1, w-lipgloss.Width(left)-lipgloss.Width(right)-lipgloss.Width(mark)-2)
	name := headlineStyle(sel).Render(truncate(title, room)) + mark
	gap := max(2, w-lipgloss.Width(left)-lipgloss.Width(name)-lipgloss.Width(right))
	return truncateLine(left+name+strings.Repeat(" ", gap)+right, w)
}

// kbSize is a byte count in whole kilobytes, or bytes under one.
func kbSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%d KB", (n+512)/1024)
}

// agoText is how long ago t was: "now" or "5m ago".
func agoText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	r := relTime(t.Format(time.RFC3339))
	if r == "now" || r == "" {
		return r
	}
	return r + " ago"
}

// chatPrompt is the one-line prompt that starts a chat with an agent.
type chatPrompt struct {
	id    string
	input textinput.Model
}

func (p chatPrompt) on() bool { return p.id != "" }

func (p chatPrompt) start(id string) (chatPrompt, tea.Cmd) {
	p.id = id
	p.input = newProjectsInput()
	return p, p.input.Focus()
}

// handleKey edits the prompt and returns the trimmed text once enter submits a
// non-empty one.
func (p chatPrompt) handleKey(msg tea.KeyPressMsg) (chatPrompt, tea.Cmd, string) {
	switch {
	case msg.Code == tea.KeyEscape:
		p.id = ""
		return p, nil, ""
	case msg.String() == "enter":
		p.id = ""
		return p, nil, strings.TrimSpace(p.input.Value())
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd, ""
}

func (p chatPrompt) view() string {
	return asstStyle.Render("new chat: " + p.input.View() + "  enter send · esc cancel")
}
