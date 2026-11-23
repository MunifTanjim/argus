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

// botComp is the bot view: one bot's threads, routines and
// memory. The bot and its threads are read from the model each time.
type botComp struct {
	botID string
	// sel is the selected row's key (see rowKeys), so the selection follows
	// its thread, routine or setting when the rows reorder; cursor is its
	// index, kept for when sel leaves the list.
	sel    string
	cursor int
	loaded bool
	err    error
	memory *botMemoryUsage // nil: the node has no memory to show
	chat   chatPrompt
	// routines are the bot's routines; the cursor runs over the threads,
	// then these.
	routines []api.BotRoutineInfo
	usage    *api.BotUsageRow // the last 7 days; nil when the node has no usage
}

// botMemoryUsage is the size of the bot's core memory, its budget, and
// when it last changed (zero when unknown).
type botMemoryUsage struct {
	usage, budget int64
	last          time.Time
}

const botOffline = "bot's node is offline"

// botViewMsg is one load of a bot view.
type botViewMsg struct {
	botID   string
	threads []api.BotThreadInfo
	memory  *botMemoryUsage
	// routines is nil when the node has no routines to list.
	routines []api.BotRoutineInfo
	usage    *api.BotUsageRow
	err      error
}

// botSetMsg is the reply to a setting change.
type botSetMsg struct {
	botID       string
	name, value string
	err         error
}

// botRoutineMsg is the reply to a routine action.
type botRoutineMsg struct {
	botID string
	verb  string
	slug  string
	err   error
}

// botSentMsg is the reply to a new chat.
type botSentMsg struct {
	botID string
	res   api.BotSendResult
	err   error
}

// botPrimaryMsg is the reply to a primary change.
type botPrimaryMsg struct {
	botID string
	err   error
}

func openBot(c *ctx, id string) tea.Cmd {
	c.replaceBase(botComp{botID: id})
	c.focusOn(mainPane)
	return c.m.loadBotViewCmd(id)
}

// loadBotViewCmd reads the bot's threads and memory usage. An unreachable
// bot's node is not asked.
func (m model) loadBotViewCmd(id string) tea.Cmd {
	if a, ok := m.bot(id); ok && a.Status == botUnreachable {
		return nil
	}
	client := m.client
	return func() tea.Msg {
		msg := botViewMsg{botID: id}
		var tr api.BotThreadsResult
		if err := client.Call(api.MethodBotThreadList, api.BotRef{BotID: id}, &tr); err != nil && !api.IsMethodNotFound(err) {
			msg.err = err
		}
		msg.threads = tr.Threads
		var mem api.BotMemoryShowResult
		if client.Call(api.MethodBotMemoryShow, api.BotRef{BotID: id}, &mem) == nil {
			msg.memory = &botMemoryUsage{usage: mem.Usage, budget: mem.Budget}
			var log api.BotMemoryLogResult
			if client.Call(api.MethodBotMemoryLog, api.BotMemoryLogParams{BotID: id, Limit: 1}, &log) == nil && len(log.Commits) > 0 {
				msg.memory.last = log.Commits[0].When
			}
		}
		var rl api.BotRoutineListResult
		if err := client.Call(api.MethodBotRoutineList, api.BotRef{BotID: id}, &rl); err == nil {
			msg.routines = rl.Routines
			if msg.routines == nil {
				msg.routines = []api.BotRoutineInfo{}
			}
		} else if !api.IsMethodNotFound(err) && msg.err == nil {
			msg.err = err
		}
		var us api.BotUsageResult
		if client.Call(api.MethodBotUsage, api.BotUsageParams{BotID: id, Since: time.Now().Add(-usageWindow)}, &us) == nil {
			msg.usage = &us.Total
		}
		return msg
	}
}

// routineCmd runs, pauses or resumes a routine: verb is run, pause or resume.
func (m model) routineCmd(botID, slug, verb string) tea.Cmd {
	method := map[string]string{"run": api.MethodBotRoutineRun, "pause": api.MethodBotRoutinePause,
		"resume": api.MethodBotRoutineResume}[verb]
	client := m.client
	return func() tea.Msg {
		err := client.Call(method, api.BotRoutineRef{BotID: botID, Slug: slug}, nil)
		return botRoutineMsg{botID: botID, verb: verb, slug: slug, err: err}
	}
}

// setBotSettingCmd sets one setting of the bot; an empty value removes it.
func (m model) setBotSettingCmd(botID string, spec botSettingSpec, value string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodBotSet, api.BotSetParams{BotID: botID, Key: spec.key, Value: value}, &api.BotSetResult{})
		return botSetMsg{botID: botID, name: spec.cmd, value: value, err: err}
	}
}

// reloadBotViewsCmd reloads every open bot view.
func (m model) reloadBotViewsCmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, comp := range m.main {
		if a, ok := comp.(botComp); ok {
			cmds = append(cmds, m.loadBotViewCmd(a.botID))
		}
	}
	return tea.Batch(cmds...)
}

func (m model) sendBotChatCmd(id, text string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.BotSendResult
		err := client.Call(api.MethodBotThreadSend, api.BotSendParams{BotID: id, Text: text, New: true}, &res)
		return botSentMsg{botID: id, res: res, err: err}
	}
}

// setBotPrimaryCmd makes the bot primary, or unsets the primary bot
// when id is empty.
func (m model) setBotPrimaryCmd(botID, id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodBotSetPrimary, api.BotPrimaryParams{BotID: id}, nil)
		return botPrimaryMsg{botID: botID, err: err}
	}
}

// updateBotView gives a bot view's reply to the open view of that bot.
func (m model) updateBotView(msg tea.Msg, botID string) (tea.Model, tea.Cmd) {
	if v, ok := msg.(botViewMsg); ok && v.err == nil {
		if m.botThreads == nil {
			m.botThreads = map[string][]api.BotThreadInfo{}
		}
		m.botThreads[botID] = v.threads
	}
	c := &ctx{m: &m}
	var cmd tea.Cmd
	if i := m.botViewAt(botID); i >= 0 {
		var comp component
		comp, cmd = m.main[i].update(c, msg)
		m.main = m.main.replaceAt(i, comp)
	} else {
		cmd = botReply(c, msg)
	}
	return m, tea.Batch(cmd, m.apply(c), m.markTopThreadReadCmd())
}

func (m model) botViewAt(id string) int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if a, ok := m.main[i].(botComp); ok && a.botID == id {
			return i
		}
	}
	return -1
}

// botReply handles the replies that do not need the view open.
func botReply(c *ctx, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case botSentMsg:
		if msg.err != nil {
			c.setFlash("send failed: " + msg.err.Error())
			return nil
		}
		return tea.Batch(openThread(c, api.BotThreadInfo{ID: msg.res.ThreadID, BotID: msg.botID, SessionID: msg.res.SessionID}),
			c.m.loadBotsCmd())
	case botPrimaryMsg:
		if msg.err != nil {
			c.setFlash("primary: " + msg.err.Error())
		}
		return c.m.loadBotsCmd()
	case botSetMsg:
		switch {
		case msg.err != nil:
			c.setFlash(msg.name + ": " + msg.err.Error())
		case msg.value == "":
			c.setFlash(msg.name + ": default")
		default:
			c.setFlash(msg.name + ": " + msg.value)
		}
		return c.m.loadBotsCmd()
	case botRoutineMsg:
		if msg.err != nil {
			c.setFlash("routine " + msg.verb + ": " + msg.err.Error())
		} else {
			c.setFlash(map[string]string{"run": "started ", "pause": "paused ", "resume": "resumed "}[msg.verb] + msg.slug)
		}
	}
	return nil
}

func (a botComp) section() string           { return "bot" }
func (a botComp) raw(*ctx) bool             { return a.chat.on() }
func (a botComp) fullScreen(*ctx) fullLevel { return notFull }
func (a botComp) close(*ctx) tea.Cmd        { return nil }
func (a botComp) offers(*ctx) []binding     { return sectionOffers[a.section()].keys }
func (a botComp) layer() layer              { return baseLayer }
func (a botComp) pageStep(c *ctx) int       { return c.m.listPageStep() }

func (a botComp) threads(c *ctx) []api.BotThreadInfo { return c.m.botThreads[a.botID] }

func (a botComp) spins(c *ctx) bool {
	return slices.ContainsFunc(a.threads(c), func(t api.BotThreadInfo) bool {
		return t.SessionID != "" && session.Status(t.SessionStatus) == session.StatusWorking
	})
}

func (a botComp) info(c *ctx) api.BotInfo {
	ag, _ := c.m.bot(a.botID)
	return ag
}

// commands leaves out the primary change that would not change anything, and
// the routine actions off a routine.
func (a botComp) commands(c *ctx) []binding {
	drop := []string{botKeys.MakePrimary.name}
	if !c.m.botPrimaryOf(a.info(c)) {
		drop = []string{botKeys.UnsetPrimary.name}
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

func (a botComp) footer(c *ctx) []binding {
	if _, ok := a.selectedRoutine(c); ok {
		return []binding{routineKeys.Run, routineKeys.TogglePause, botKeys.NewChat, botKeys.Memory, listKeys.Back, listKeys.Refresh}
	}
	return []binding{listKeys.Open, botKeys.NewChat, botKeys.Memory, listKeys.Back, listKeys.Refresh}
}

// items is how many rows the cursor runs over: the threads, the routines,
// then the settings.
func (a botComp) items(c *ctx) int {
	return len(a.threads(c)) + len(a.routines) + len(a.settingRows(c))
}

// rowKeys names each row the cursor runs over, in order.
func (a botComp) rowKeys(c *ctx) []string {
	keys := make([]string, 0, a.items(c))
	for _, t := range a.threads(c) {
		keys = append(keys, "thread:"+t.ID)
	}
	for _, r := range a.routines {
		keys = append(keys, "routine:"+r.Slug)
	}
	for _, s := range a.settingRows(c) {
		keys = append(keys, "setting:"+s.key)
	}
	return keys
}

// at is the selected row's index: sel's row, else the last index clamped.
func (a botComp) at(c *ctx) int {
	keys := a.rowKeys(c)
	if i := slices.Index(keys, a.sel); i >= 0 {
		return i
	}
	return max(0, min(a.cursor, len(keys)-1))
}

// moveTo selects row i.
func (a botComp) moveTo(c *ctx, i int) botComp {
	keys := a.rowKeys(c)
	a.cursor, a.sel = i, ""
	if i >= 0 && i < len(keys) {
		a.sel = keys[i]
	}
	return a
}

// settingRows are the settings shown; none when the node does not say them.
func (a botComp) settingRows(c *ctx) []botSettingSpec {
	if a.info(c).Settings == nil {
		return nil
	}
	return botSettingSpecs
}

func (a botComp) selectedSetting(c *ctx) (botSettingSpec, bool) {
	i := a.at(c) - len(a.threads(c)) - len(a.routines)
	rows := a.settingRows(c)
	if i < 0 || i >= len(rows) {
		return botSettingSpec{}, false
	}
	return rows[i], true
}

func (a botComp) selectedRoutine(c *ctx) (api.BotRoutineInfo, bool) {
	i := a.at(c) - len(a.threads(c))
	if i < 0 || i >= len(a.routines) {
		return api.BotRoutineInfo{}, false
	}
	return a.routines[i], true
}

func (a botComp) footerPrompt(*ctx) string {
	if a.chat.on() {
		return a.chat.view("new chat")
	}
	return ""
}

func (a botComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case botViewMsg:
		a.loaded, a.err, a.memory, a.routines, a.usage = true, msg.err, msg.memory, msg.routines, msg.usage
	case botSentMsg, botPrimaryMsg, botRoutineMsg, botSetMsg:
		cmd := botReply(c, msg)
		return a, tea.Batch(cmd, c.m.loadBotViewCmd(a.botID))
	case tea.PasteMsg:
		var cmd tea.Cmd
		a.chat, cmd = a.chat.paste(msg)
		return a, cmd
	}
	return a, nil
}

func (a botComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k := c.m, listKeys
	if a.chat.on() {
		var cmd tea.Cmd
		var text string
		a.chat, cmd, text = a.chat.handleKey(msg)
		if text != "" {
			cmd = m.sendBotChatCmd(a.botID, text)
		}
		return a, cmd, true
	}
	threads := a.threads(c)
	n := a.items(c)
	cur := a.at(c)
	offline := a.info(c).Status == botUnreachable
	routine, onRoutine := a.selectedRoutine(c)
	switch {
	case m.matches(msg, k.Up):
		a = a.moveTo(c, cursorUp(cur))
	case m.matches(msg, k.Down):
		a = a.moveTo(c, cursorDown(cur, n))
	case m.matches(msg, k.Top):
		a = a.moveTo(c, 0)
	case m.matches(msg, k.Bottom):
		a = a.moveTo(c, cursorBottom(n))
	case m.matches(msg, k.HalfUp):
		a = a.moveTo(c, cursorBy(cur, -a.pageStep(c), n))
	case m.matches(msg, k.HalfDown):
		a = a.moveTo(c, cursorBy(cur, a.pageStep(c), n))
	case m.matches(msg, k.Open):
		if cur < len(threads) {
			return a, openThread(c, threads[cur]), true
		}
	case m.matches(msg, botKeys.NewChat, botKeys.Memory, botKeys.MakePrimary, botKeys.UnsetPrimary,
		routineKeys.Run, routineKeys.Pause, routineKeys.Resume, routineKeys.TogglePause) && offline:
		c.setFlash(botOffline)
	case m.matches(msg, botKeys.CycleSetting) && offline, m.matches(msg, settingKeys()...) && offline:
		c.setFlash(botOffline)
	case m.matches(msg, botKeys.CycleSetting):
		spec, ok := a.selectedSetting(c)
		if !ok {
			c.setFlash("pick a setting")
			return a, nil, true
		}
		return a, m.setBotSettingCmd(a.botID, spec, spec.next(a.info(c).Settings[spec.field])), true
	case m.matches(msg, settingKeys()...):
		for _, s := range botSettingSpecs {
			for i, b := range s.bindings {
				if m.matches(msg, b) {
					return a, m.setBotSettingCmd(a.botID, s, s.value(i)), true
				}
			}
		}
	case m.matches(msg, routineKeys.Run, routineKeys.Pause, routineKeys.Resume, routineKeys.TogglePause) && !onRoutine:
		c.setFlash("pick a routine")
	case m.matches(msg, routineKeys.Run):
		return a, m.routineCmd(a.botID, routine.Slug, "run"), true
	case m.matches(msg, routineKeys.Pause):
		return a, m.routineCmd(a.botID, routine.Slug, "pause"), true
	case m.matches(msg, routineKeys.Resume):
		return a, m.routineCmd(a.botID, routine.Slug, "resume"), true
	case m.matches(msg, routineKeys.TogglePause):
		verb := "pause"
		if routine.State == "paused" {
			verb = "resume"
		}
		return a, m.routineCmd(a.botID, routine.Slug, verb), true
	case m.matches(msg, botKeys.NewChat):
		var cmd tea.Cmd
		a.chat, cmd = a.chat.start(a.botID)
		return a, cmd, true
	case m.matches(msg, botKeys.Memory):
		return a, openBotMemory(c, a.botID), true
	case m.matches(msg, botKeys.MakePrimary):
		return a, m.setBotPrimaryCmd(a.botID, a.botID), true
	case m.matches(msg, botKeys.UnsetPrimary):
		return a, m.setBotPrimaryCmd(a.botID, ""), true
	case m.matches(msg, k.Refresh):
		return a, tea.Batch(m.loadBotViewCmd(a.botID), m.loadBotsCmd()), true
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

func (a botComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	threads := a.threads(c)
	switch {
	case t.index >= a.items(c):
	case focused && t.index == a.at(c) && t.index < len(threads):
		return a, openThread(c, threads[t.index])
	default:
		a = a.moveTo(c, t.index)
	}
	return a, nil
}

func (a botComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	a = a.moveTo(c, cursorBy(a.at(c), d, a.items(c)))
	return a, nil
}

func (a botComp) paneHeader(c *ctx, w int) (string, int) {
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
	if c.m.botPrimaryOf(ag) {
		parts = append(parts, "primary")
	}
	return strings.Join(parts, dimStyle.Render(" · ")), centerGutter(min(w, maxCardWidth), w)
}

func (a botComp) view(c *ctx, w, h int) string {
	m := c.m
	cardW := min(w, maxCardWidth)
	ag := a.info(c)
	threads := a.threads(c)
	cursor := a.at(c)
	focused := m.focused == mainPane && len(m.popups) == 0
	l := itemLines{key: "bot-view"}
	l.text(StyleSecondaryBold.Render("Threads"))
	switch {
	case ag.Status == botUnreachable:
		l.text(dimStyle.Render(botOffline))
	case !a.loaded:
		l.text(dimStyle.Render("loading threads…"))
	case a.err != nil && len(threads) == 0:
		l.text(dimStyle.Render("error: " + a.err.Error()))
	case len(threads) == 0:
		l.text(dimStyle.Render("no threads · " + m.keyTextOn(a.section(), botKeys.NewChat) + " starts a chat"))
	}
	for i, t := range threads {
		l.add(i, m.threadRow(t, i == cursor && focused, cardW))
	}
	if a.routines != nil || !a.loaded {
		l.text("")
		l.text(StyleSecondaryBold.Render("Routines"))
		if len(a.routines) == 0 {
			l.text(dimStyle.Render("no routines"))
		}
		for i, r := range a.routines {
			at := len(threads) + i
			l.add(at, routineRow(r, at == cursor && focused, cardW))
		}
	}
	if u := a.usage; u != nil {
		l.text("")
		l.text(StyleSecondaryBold.Render("Usage"))
		l.text(dimStyle.Render("Last 7 days · " + usageText(*u)))
	}
	if rows := a.settingRows(c); len(rows) > 0 {
		l.text("")
		l.text(StyleSecondaryBold.Render("Settings"))
		for i, s := range rows {
			at := len(threads) + len(a.routines) + i
			l.add(at, settingRow(s, ag.Settings[s.field], at == cursor && focused, cardW))
		}
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
func routineRow(r api.BotRoutineInfo, sel bool, w int) string {
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
func routineProgress(r api.BotRoutineInfo) string {
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

func (m model) threadRow(t api.BotThreadInfo, sel bool, w int) string {
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

// chatPrompt is the one-line prompt that sends a bot a message.
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

// paste inserts pasted text while the prompt is on.
func (p chatPrompt) paste(msg tea.PasteMsg) (chatPrompt, tea.Cmd) {
	if !p.on() {
		return p, nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

func (p chatPrompt) view(label string) string {
	return asstStyle.Render(label + ": " + p.input.View() + "  enter send · esc cancel")
}

// usageWindow is how far back the bot view counts usage.
const usageWindow = 7 * 24 * time.Hour

// usageText is usage in words: sessions, then cost and tokens when reported.
func usageText(r api.BotUsageRow) string {
	parts := []string{plural(int(r.Sessions), "session")}
	if r.Cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", r.Cost))
	}
	if r.Tokens > 0 {
		t := fmt.Sprint(r.Tokens)
		if r.Tokens >= 1000 {
			t = fmt.Sprintf("%.1fk", float64(r.Tokens)/1000)
		}
		parts = append(parts, t+" tokens")
	}
	return strings.Join(parts, " · ")
}

// botSettingSpec is a setting the bot view shows and sets: key is the
// set RPC's key, field the key in BotInfo.settings, cmd its command word.
type botSettingSpec struct {
	key, field, cmd, title string
	options                []string
	// bindings are "bot set <cmd> <option>" for each option, then
	// "bot unset <cmd>".
	bindings []binding
}

// value is what binding i sets; "" removes the setting.
func (s botSettingSpec) value(i int) string {
	if i < len(s.options) {
		return s.options[i]
	}
	return ""
}

// next is the value after cur in the cycle: the options in order, then the
// default.
func (s botSettingSpec) next(cur api.BotSetting) string {
	if !cur.Explicit {
		return s.options[0]
	}
	i := slices.Index(s.options, cur.Value)
	if i < 0 || i+1 >= len(s.options) {
		return ""
	}
	return s.options[i+1]
}

func newSettingSpec(key, field, cmd, title string, options ...string) botSettingSpec {
	s := botSettingSpec{key: key, field: field, cmd: cmd, title: title, options: options}
	for _, o := range options {
		s.bindings = append(s.bindings, nm("bot set "+cmd+" "+o, "", ""))
	}
	s.bindings = append(s.bindings, nm("bot unset "+cmd, "", ""))
	return s
}

var botSettingSpecs = []botSettingSpec{
	newSettingSpec("notify", "notify", "notify", "Notifications", "all", "needs-you", "off"),
	newSettingSpec("proactivity", "proactivity", "proactivity", "Proactivity", "off", "low", "default", "high"),
	newSettingSpec("memory.mode", "memory_mode", "memory", "Memory", "auto", "review", "off"),
}

// settingKeys are the set and unset commands of every setting.
func settingKeys() []binding {
	var out []binding
	for _, s := range botSettingSpecs {
		out = append(out, s.bindings...)
	}
	return out
}

func settingRow(s botSettingSpec, st api.BotSetting, sel bool, w int) string {
	marker := "  "
	if sel {
		marker = asstStyle.Render("❯ ")
	}
	val := cmp.Or(st.Value, "?")
	line := marker + headlineStyle(sel).Render(fmt.Sprintf("%-14s", s.title)) + " " + val
	if !st.Explicit {
		line += dimStyle.Render(" (default)")
	}
	return truncateLine(line, w)
}
