package tui

import (
	"cmp"
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
	// routines are the agent's routines; the cursor runs over the threads,
	// then these.
	routines []api.PAgentRoutineInfo
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
	// routines is nil when the node has no routines to list.
	routines []api.PAgentRoutineInfo
	err      error
}

// agentRoutineMsg is the reply to a routine action.
type agentRoutineMsg struct {
	agentID string
	verb    string
	slug    string
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
		var rl api.PAgentRoutineListResult
		if err := client.Call(api.MethodPAgentRoutineList, api.PAgentRef{AgentID: id}, &rl); err == nil {
			msg.routines = rl.Routines
		} else if !api.IsMethodNotFound(err) && msg.err == nil {
			msg.err = err
		}
		return msg
	}
}

// routineCmd runs, pauses or resumes a routine: verb is run, pause or resume.
func (m model) routineCmd(agentID, slug, verb string) tea.Cmd {
	method := map[string]string{"run": api.MethodPAgentRoutineRun, "pause": api.MethodPAgentRoutinePause,
		"resume": api.MethodPAgentRoutineResume}[verb]
	client := m.client
	return func() tea.Msg {
		err := client.Call(method, api.PAgentRoutineRef{AgentID: agentID, Slug: slug}, nil)
		return agentRoutineMsg{agentID: agentID, verb: verb, slug: slug, err: err}
	}
}

// reloadAgentViewsCmd reloads every open agent view.
func (m model) reloadAgentViewsCmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, comp := range m.main {
		if a, ok := comp.(agentComp); ok {
			cmds = append(cmds, m.loadAgentViewCmd(a.agentID))
		}
	}
	return tea.Batch(cmds...)
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
		return tea.Batch(openThread(c, api.PAgentThreadInfo{ID: msg.res.ThreadID, AgentID: msg.agentID, SessionID: msg.res.SessionID}),
			c.m.loadPAgentsCmd())
	case agentPrimaryMsg:
		if msg.err != nil {
			c.setFlash("primary: " + msg.err.Error())
		}
		return c.m.loadPAgentsCmd()
	case agentRoutineMsg:
		if msg.err != nil {
			c.setFlash("routine " + msg.verb + ": " + msg.err.Error())
		} else {
			c.setFlash(map[string]string{"run": "started ", "pause": "paused ", "resume": "resumed "}[msg.verb] + msg.slug)
		}
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

// commands leaves out the primary change that would not change anything, and
// the routine actions off a routine.
func (a agentComp) commands(c *ctx) []binding {
	drop := []string{agentKeys.MakePrimary.name}
	if !c.m.pagentPrimaryOf(a.info(c)) {
		drop = []string{agentKeys.UnsetPrimary.name}
	}
	if r, ok := a.selectedRoutine(c); !ok {
		drop = append(drop, routineKeys.Run.name, routineKeys.Pause.name, routineKeys.Resume.name, routineKeys.TogglePause.name)
	} else if r.State == "paused" {
		drop = append(drop, routineKeys.Pause.name)
	} else {
		drop = append(drop, routineKeys.Resume.name)
	}
	return slices.DeleteFunc(slices.Clone(sectionLists[a.section()].own), func(b binding) bool { return slices.Contains(drop, b.name) })
}

func (a agentComp) footer(c *ctx) []binding {
	if _, ok := a.selectedRoutine(c); ok {
		return []binding{routineKeys.Run, routineKeys.TogglePause, agentKeys.NewChat, agentKeys.Memory, listKeys.Back, listKeys.Refresh}
	}
	return []binding{listKeys.Open, agentKeys.NewChat, agentKeys.Memory, listKeys.Back, listKeys.Refresh}
}

// items is how many rows the cursor runs over: the threads, then the routines.
func (a agentComp) items(c *ctx) int { return len(a.threads(c)) + len(a.routines) }

func (a agentComp) selectedRoutine(c *ctx) (api.PAgentRoutineInfo, bool) {
	i := min(a.cursor, cursorBottom(a.items(c))) - len(a.threads(c))
	if i < 0 || i >= len(a.routines) {
		return api.PAgentRoutineInfo{}, false
	}
	return a.routines[i], true
}

func (a agentComp) footerPrompt(*ctx) string {
	if a.chat.on() {
		return a.chat.view("new chat")
	}
	return ""
}

func (a agentComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case agentViewMsg:
		a.loaded, a.err, a.memory, a.routines = true, msg.err, msg.memory, msg.routines
	case agentSentMsg, agentPrimaryMsg, agentRoutineMsg:
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
	n := a.items(c)
	a.cursor = min(a.cursor, cursorBottom(n))
	offline := a.info(c).Status == pagentUnreachable
	routine, onRoutine := a.selectedRoutine(c)
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
		if a.cursor < len(threads) {
			return a, openThread(c, threads[a.cursor]), true
		}
	case m.matches(msg, agentKeys.NewChat, agentKeys.Memory, agentKeys.MakePrimary, agentKeys.UnsetPrimary,
		routineKeys.Run, routineKeys.Pause, routineKeys.Resume, routineKeys.TogglePause) && offline:
		c.setFlash(agentOffline)
	case m.matches(msg, routineKeys.Run, routineKeys.Pause, routineKeys.Resume, routineKeys.TogglePause) && !onRoutine:
		c.setFlash("pick a routine")
	case m.matches(msg, routineKeys.Run):
		return a, m.routineCmd(a.agentID, routine.Slug, "run"), true
	case m.matches(msg, routineKeys.Pause):
		return a, m.routineCmd(a.agentID, routine.Slug, "pause"), true
	case m.matches(msg, routineKeys.Resume):
		return a, m.routineCmd(a.agentID, routine.Slug, "resume"), true
	case m.matches(msg, routineKeys.TogglePause):
		verb := "pause"
		if routine.State == "paused" {
			verb = "resume"
		}
		return a, m.routineCmd(a.agentID, routine.Slug, verb), true
	case m.matches(msg, agentKeys.NewChat):
		var cmd tea.Cmd
		a.chat, cmd = a.chat.start(a.agentID)
		return a, cmd, true
	case m.matches(msg, agentKeys.Memory):
		return a, openAgentMemory(c, a.agentID), true
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
	case t.index >= a.items(c):
	case focused && t.index == a.cursor && t.index < len(threads):
		return a, openThread(c, threads[t.index])
	default:
		a.cursor = t.index
	}
	return a, nil
}

func (a agentComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	a.cursor = cursorBy(a.cursor, d, a.items(c))
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
	cursor := min(a.cursor, cursorBottom(a.items(c)))
	focused := m.focused == mainPane && len(m.popups) == 0
	l := itemLines{key: "agent-view"}
	l.text(StyleSecondaryBold.Render("Threads"))
	switch {
	case ag.Status == pagentUnreachable:
		l.text(dimStyle.Render(agentOffline))
	case !a.loaded:
		l.text(dimStyle.Render("loading threads…"))
	case a.err != nil && len(threads) == 0:
		l.text(dimStyle.Render("error: " + a.err.Error()))
	case len(threads) == 0:
		l.text(dimStyle.Render("no threads · " + m.keyTextOn(a.section(), agentKeys.NewChat) + " starts a chat"))
	}
	for i, t := range threads {
		l.add(i, m.threadRow(t, i == cursor && focused, cardW))
	}
	l.text("")
	l.text(StyleSecondaryBold.Render("Routines"))
	if len(a.routines) == 0 {
		l.text(dimStyle.Render("no routines"))
	}
	for i, r := range a.routines {
		at := len(threads) + i
		l.add(at, routineRow(r, at == cursor && focused, cardW))
	}
	if u := a.memory; u != nil {
		line := "Memory · " + kbSize(u.usage)
		if u.budget > 0 {
			line += " / " + kbSize(u.budget)
		}
		if !u.last.IsZero() {
			line += " · last change " + agoText(u.last)
		}
		l.text("")
		l.text(StyleSecondaryBold.Render(line))
	}
	if a.items(c) == 0 {
		return centerBlock(strings.Join(l.lines, "\n"), cardW, w)
	}
	return centerBlock(strings.Join(l.window(c, cursor, max(1, h)), "\n"), cardW, w)
}

// routineRow is a routine: slug, when it starts, the next run, its progress
// against its budget, and its state when not active.
func routineRow(r api.PAgentRoutineInfo, sel bool, w int) string {
	marker := "  "
	if sel {
		marker = asstStyle.Render("❯ ")
	}
	var parts []string
	if d := cmp.Or(strings.Join(r.Starts, ", "), r.What); d != "" {
		parts = append(parts, d)
	}
	if r.NextDue != nil && r.State == "active" {
		parts = append(parts, "next "+untilText(*r.NextDue))
	}
	if p := routineProgress(r); p != "" {
		parts = append(parts, p)
	}
	switch {
	case r.State == "paused" && r.PausedReason != "":
		parts = append(parts, "paused: "+r.PausedReason)
	case r.State == "invalid" && r.Error != "":
		parts = append(parts, "invalid: "+r.Error)
	case r.State != "active" && r.State != "":
		parts = append(parts, r.State)
	}
	return truncateLine(marker+headlineStyle(sel).Render(r.Slug)+"  "+dimStyle.Render(strings.Join(parts, " · ")), w)
}

// routineProgress is the sessions and cost used, against the budget where it
// has one.
func routineProgress(r api.PAgentRoutineInfo) string {
	var parts []string
	if r.Budget.Sessions > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d sessions", r.Used.Sessions, r.Budget.Sessions))
	} else if r.Used.Sessions > 0 {
		parts = append(parts, plural(int(r.Used.Sessions), "session"))
	}
	switch {
	case r.Budget.Cost > 0:
		parts = append(parts, fmt.Sprintf("%s/%s", dollars(r.Used.Cost), dollars(r.Budget.Cost)))
	case r.Used.Cost > 0:
		parts = append(parts, dollars(r.Used.Cost))
	}
	return strings.Join(parts, " · ")
}

func dollars(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("$%d", int64(v))
	}
	return fmt.Sprintf("$%.2f", v)
}

// untilText is how long until t: "now" or "in 5m".
func untilText(t time.Time) string {
	d := time.Until(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	}
	return fmt.Sprintf("in %dd", int(d.Hours())/24)
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

// chatPrompt is the one-line prompt that sends an agent a message.
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

func (p chatPrompt) view(label string) string {
	return asstStyle.Render(label + ": " + p.input.View() + "  enter send · esc cancel")
}
