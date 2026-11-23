package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// botMemoryTab is a tab of the bot memory view.
type botMemoryTab int

const (
	botMemoryFiles botMemoryTab = iota
	botMemoryHistory
	botMemoryReview
)

var botMemoryTabNames = []string{"Files", "History", "Review"}

// botMemoryComp is a bot's memory: its files, the history of changes
// with undo, and the changes awaiting review. undo is the commit that awaits
// an undo confirmation.
type botMemoryComp struct {
	botID     string
	tab       botMemoryTab
	cursor    int // History and Review: the selected row; Files: the top line
	loaded    bool
	err       error
	show      api.BotMemoryShowResult
	commits   []api.BotMemoryCommit
	proposals []api.BotMemoryProposal
	undo      string
}

// botMemoryMsg is one load of the memory view.
type botMemoryMsg struct {
	botID     string
	show      api.BotMemoryShowResult
	commits   []api.BotMemoryCommit
	proposals []api.BotMemoryProposal
	err       error
}

// botMemoryActionMsg is the reply to an undo or a decision.
type botMemoryActionMsg struct {
	botID string
	verb  string
	done  string // the flash on success
	err   error
}

func openBotMemory(c *ctx, id string) tea.Cmd {
	c.open(botMemoryComp{botID: id})
	c.focusOn(mainPane)
	return c.m.loadBotMemoryCmd(id)
}

func (m model) loadBotMemoryCmd(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		msg := botMemoryMsg{botID: id}
		ref := api.BotRef{BotID: id}
		if err := client.Call(api.MethodBotMemoryShow, ref, &msg.show); err != nil {
			msg.err = err
			return msg
		}
		var log api.BotMemoryLogResult
		if err := client.Call(api.MethodBotMemoryLog, api.BotMemoryLogParams{BotID: id}, &log); err != nil && !api.IsMethodNotFound(err) {
			msg.err = err
		}
		var props api.BotMemoryProposalsResult
		if err := client.Call(api.MethodBotMemoryProposals, ref, &props); err != nil && !api.IsMethodNotFound(err) {
			msg.err = err
		}
		msg.commits, msg.proposals = log.Commits, props.Proposals
		return msg
	}
}

func (m model) botMemoryUndoCmd(id, commit string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.BotMemoryUndoResult
		err := client.Call(api.MethodBotMemoryUndo, api.BotMemoryUndoParams{BotID: id, Commit: commit}, &res)
		return botMemoryActionMsg{botID: id, verb: "undo", done: "undone", err: err}
	}
}

func (m model) botMemoryDecideCmd(id, proposal string, accept bool) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.BotMemoryDecideResult
		err := client.Call(api.MethodBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: proposal, Accept: accept}, &res)
		msg := botMemoryActionMsg{botID: id, verb: "reject", done: "rejected", err: err}
		if accept {
			msg.verb, msg.done = "accept", fmt.Sprintf("accepted: %d applied", res.Applied)
			if len(res.Rejected) > 0 {
				msg.done += " · not applied: " + strings.Join(res.Rejected, "; ")
			}
		}
		return msg
	}
}

func (m model) updateBotMemory(msg tea.Msg, id string) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	var cmd tea.Cmd
	for i := len(m.main) - 1; i >= 0; i-- {
		if v, ok := m.main[i].(botMemoryComp); ok && v.botID == id {
			var comp component
			comp, cmd = v.update(c, msg)
			m.main = m.main.replaceAt(i, comp)
			break
		}
	}
	return m, tea.Batch(cmd, m.apply(c))
}

func (v botMemoryComp) section() string           { return "bot-memory" }
func (v botMemoryComp) raw(*ctx) bool             { return v.undo != "" }
func (v botMemoryComp) spins(*ctx) bool           { return false }
func (v botMemoryComp) fullScreen(*ctx) fullLevel { return notFull }
func (v botMemoryComp) close(*ctx) tea.Cmd        { return nil }
func (v botMemoryComp) offers(*ctx) []binding     { return sectionOffers[v.section()].keys }
func (v botMemoryComp) commands(*ctx) []binding   { return sectionLists[v.section()].own }
func (v botMemoryComp) layer() layer              { return baseLayer }
func (v botMemoryComp) pageStep(c *ctx) int       { return c.m.listPageStep() }

func (v botMemoryComp) footer(*ctx) []binding {
	k := listKeys
	switch v.tab {
	case botMemoryHistory:
		return []binding{k.TabNext, memoryKeys.Undo, k.Back, k.Refresh}
	case botMemoryReview:
		return []binding{k.TabNext, memoryKeys.Accept, memoryKeys.Reject, k.Back, k.Refresh}
	}
	return []binding{k.TabNext, k.Back, k.Refresh}
}

func (v botMemoryComp) footerPrompt(*ctx) string {
	if v.undo == "" {
		return ""
	}
	return asstStyle.Render("undo memory change " + shortHash(v.undo) + "? y/n")
}

func shortHash(h string) string { return h[:min(len(h), 7)] }

func (v botMemoryComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case botMemoryMsg:
		sel := v.selectedKey()
		v.loaded, v.err = true, msg.err
		v.show, v.commits, v.proposals = msg.show, msg.commits, msg.proposals
		if i := v.keyIndex(sel); i >= 0 {
			v.cursor = i // the selection follows its commit or proposal
		}
		v.cursor = min(v.cursor, cursorBottom(v.rows(c)))
	case botMemoryActionMsg:
		if msg.err != nil {
			c.setFlash(msg.verb + ": " + msg.err.Error())
		} else {
			c.setFlash(msg.done)
		}
		return v, tea.Batch(c.m.loadBotMemoryCmd(v.botID), c.m.loadBotViewCmd(v.botID))
	}
	return v, nil
}

// selectedKey is the selected commit's hash or proposal's id; "" on Files.
func (v botMemoryComp) selectedKey() string {
	switch {
	case v.tab == botMemoryHistory && v.cursor < len(v.commits):
		return v.commits[v.cursor].Hash
	case v.tab == botMemoryReview && v.cursor < len(v.proposals):
		return v.proposals[v.cursor].ID
	}
	return ""
}

// keyIndex is the row of the tab's commit or proposal key, or -1.
func (v botMemoryComp) keyIndex(key string) int {
	if key == "" {
		return -1
	}
	switch v.tab {
	case botMemoryHistory:
		return slices.IndexFunc(v.commits, func(cm api.BotMemoryCommit) bool { return cm.Hash == key })
	case botMemoryReview:
		return slices.IndexFunc(v.proposals, func(p api.BotMemoryProposal) bool { return p.ID == key })
	}
	return -1
}

// rows is how many rows the tab moves the cursor over.
func (v botMemoryComp) rows(c *ctx) int {
	switch v.tab {
	case botMemoryHistory:
		return len(v.commits)
	case botMemoryReview:
		return len(v.proposals)
	}
	return len(v.fileLines(min(c.m.bodyWidth(), maxCardWidth)))
}

func (v botMemoryComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k := c.m, listKeys
	if commit := v.undo; commit != "" {
		v.undo = ""
		if msg.String() == "y" {
			return v, m.botMemoryUndoCmd(v.botID, commit), true
		}
		return v, nil, true
	}
	n := v.rows(c)
	v.cursor = min(v.cursor, cursorBottom(n))
	c.setFlash("")
	switch {
	case m.matches(msg, k.Up):
		v.cursor = cursorUp(v.cursor)
	case m.matches(msg, k.Down):
		v.cursor = cursorDown(v.cursor, n)
	case m.matches(msg, k.Top):
		v.cursor = 0
	case m.matches(msg, k.Bottom):
		v.cursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		v.cursor = cursorBy(v.cursor, -v.pageStep(c), n)
	case m.matches(msg, k.HalfDown):
		v.cursor = cursorBy(v.cursor, v.pageStep(c), n)
	case m.matches(msg, k.TabNext), m.matches(msg, k.TabPrev):
		d, t := 1, len(botMemoryTabNames)
		if m.matches(msg, k.TabPrev) {
			d = -1
		}
		v.tab, v.cursor = botMemoryTab((int(v.tab)+d+t)%t), 0
	case m.matches(msg, memoryKeys.Undo):
		if v.tab != botMemoryHistory || v.cursor >= len(v.commits) {
			c.setFlash("pick a change in History to undo")
			return v, nil, true
		}
		v.undo = v.commits[v.cursor].Hash
	case m.matches(msg, memoryKeys.Accept), m.matches(msg, memoryKeys.Reject):
		if v.tab != botMemoryReview || v.cursor >= len(v.proposals) {
			c.setFlash("pick a change in Review")
			return v, nil, true
		}
		return v, m.botMemoryDecideCmd(v.botID, v.proposals[v.cursor].ID, m.matches(msg, memoryKeys.Accept)), true
	case m.matches(msg, k.Refresh):
		return v, m.loadBotMemoryCmd(v.botID), true
	case m.matches(msg, k.Back):
		c.back()
	default:
		return v, nil, false
	}
	return v, nil, true
}

func (v botMemoryComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	v.cursor = cursorBy(v.cursor, d, v.rows(c))
	return v, nil
}

func (v botMemoryComp) paneHeader(c *ctx, w int) (string, int) {
	a, _ := c.m.bot(v.botID)
	return StylePrimaryBold.Render(a.Name) + dimStyle.Render(" · memory"), centerGutter(min(w, maxCardWidth), w)
}

func (v botMemoryComp) view(c *ctx, w, h int) string {
	cardW := min(w, maxCardWidth)
	strip := tabStrip(botMemoryTabNames, int(v.tab), c.m.focused == mainPane)
	avail := max(1, h-2)
	var body string
	switch {
	case !v.loaded:
		body = dimStyle.Render("loading memory…")
	case v.err != nil && api.IsMethodNotFound(v.err):
		body = dimStyle.Render("this bot's node has no memory to show")
	case v.err != nil:
		body = dimStyle.Render("error: " + v.err.Error())
	case v.tab == botMemoryFiles:
		lines := v.fileLines(cardW)
		top := min(v.cursor, max(0, len(lines)-avail))
		body = strings.Join(lines[top:min(len(lines), top+avail)], "\n")
	case v.tab == botMemoryHistory:
		body = v.historyRows(c.below(2), cardW, avail)
	default:
		body = v.reviewRows(c.below(2), cardW, avail)
	}
	return centerBlock(strip+"\n\n"+body, cardW, w)
}

// fileLines is the Files tab: MEMORY.md, USER.md, and the topic list.
func (v botMemoryComp) fileLines(w int) []string {
	var out []string
	file := func(name, text string) {
		out = append(out, StyleSecondaryBold.Render(name))
		text = strings.TrimRight(text, "\n")
		if strings.TrimSpace(text) == "" {
			out = append(out, dimStyle.Render("empty"), "")
			return
		}
		out = append(out, strings.Split(lipgloss.NewStyle().Width(max(w, 10)).Render(text), "\n")...)
		out = append(out, "")
	}
	head := "MEMORY.md"
	if v.show.Budget > 0 {
		head += dimStyle.Render(" · " + kbSize(v.show.Usage) + " / " + kbSize(v.show.Budget))
	}
	file(head, v.show.Memory)
	file("USER.md", v.show.User)
	out = append(out, StyleSecondaryBold.Render("Topics"))
	if len(v.show.Topics) == 0 {
		out = append(out, dimStyle.Render("none"))
	}
	for _, t := range v.show.Topics {
		out = append(out, "  "+t)
	}
	return out
}

func (v botMemoryComp) historyRows(c *ctx, w, avail int) string {
	if len(v.commits) == 0 {
		return dimStyle.Render("no memory changes")
	}
	l := itemLines{key: "bot-memory-history"}
	for i, cm := range v.commits {
		sub := cm.Source
		if t := agoText(cm.When); t != "" {
			sub += " · " + t
		}
		l.add(i, spawnChoiceRow(cm.Subject, sub, i == v.cursor, w))
	}
	return strings.Join(l.window(c, v.cursor, avail), "\n")
}

func (v botMemoryComp) reviewRows(c *ctx, w, avail int) string {
	if len(v.proposals) == 0 {
		return dimStyle.Render("no changes to review")
	}
	l := itemLines{key: "bot-memory-review"}
	for i, p := range v.proposals {
		l.add(i, spawnChoiceRow(proposalSummary(p), p.Source+" · "+agoText(p.CreatedAt), i == v.cursor, w))
	}
	return strings.Join(l.window(c, v.cursor, avail), "\n")
}

// proposalSummary is a proposal in one line: its ops, or the reason for an
// instruction rewrite.
func proposalSummary(p api.BotMemoryProposal) string {
	if p.Kind == "bot" {
		return cmp.Or(p.Summary, "create a bot")
	}
	if p.Kind == "routine" {
		s := "routine " + p.Slug
		if d := cmp.Or(p.Summary, p.Reason); d != "" {
			s += ": " + firstLine(d)
		}
		return s
	}
	if p.Kind == "instructions" {
		return "instructions: " + firstLine(cmp.Or(p.Reason, p.Text))
	}
	var parts []string
	for _, op := range p.Ops {
		file := op.File
		if file == "" {
			file = "MEMORY.md"
		}
		parts = append(parts, op.Action+" "+file+": "+firstLine(cmp.Or(op.Text, op.Match)))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, "; ")
}
