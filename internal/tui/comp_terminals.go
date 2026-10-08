package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// terminalsComp lists every node's terminals on Home, or one node's in a node
// pane. killID and renameID are the terminal that awaits a confirmation or a
// name.
type terminalsComp struct {
	nodeID   string
	cursor   int
	killID   string
	renameID string
	input    textinput.Model
}

func (t terminalsComp) section() string           { return "terminals" }
func (t terminalsComp) raw(*ctx) bool             { return t.killID != "" || t.renameID != "" }
func (t terminalsComp) spins(*ctx) bool           { return false }
func (t terminalsComp) fullScreen(*ctx) fullLevel { return notFull }
func (t terminalsComp) close(*ctx) tea.Cmd        { return nil }
func (t terminalsComp) offers(*ctx) []binding     { return sectionOffers[t.section()].keys }
func (t terminalsComp) commands(*ctx) []binding   { return sectionLists[t.section()].own }
func (t terminalsComp) layer() layer              { return baseLayer }
func (t terminalsComp) pageStep(c *ctx) int       { return c.m.listPageStep() }

func (t terminalsComp) row() string {
	if t.nodeID == "" {
		return homeRowID
	}
	return t.nodeID
}

func (t terminalsComp) shown(m model) []api.Terminal {
	if t.nodeID == "" {
		return m.terminals
	}
	var out []api.Terminal
	for _, x := range m.terminals {
		if x.NodeID == t.nodeID {
			out = append(out, x)
		}
	}
	return out
}

func (t terminalsComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	a, ok := msg.(terminalActionMsg)
	if !ok {
		return t, nil
	}
	// A failed action can mean the terminal is gone, so the list reloads too.
	cmd := c.m.loadTerminalsCmd()
	if a.err != nil {
		c.setFlash(a.verb + ": " + a.err.Error())
		return t, cmd
	}
	if a.created != nil {
		cmd = tea.Batch(cmd, attachTerminal(c, *a.created, t.row()))
	}
	return t, cmd
}

func (t terminalsComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k := c.m, listKeys
	if id := t.killID; id != "" {
		t.killID = ""
		if msg.String() == "y" {
			return t, m.killTerminalCmd(id), true
		}
		return t, nil, true
	}
	if t.renameID != "" {
		var cmd tea.Cmd
		t, cmd = t.renameKey(c, msg)
		return t, cmd, true
	}
	list := t.shown(*m)
	n := len(list)
	t.cursor = min(t.cursor, cursorBottom(n))
	c.setFlash("")
	var cmd tea.Cmd
	switch {
	case m.matches(msg, k.Up):
		t.cursor = cursorUp(t.cursor)
	case m.matches(msg, k.Down):
		t.cursor = cursorDown(t.cursor, n)
	case m.matches(msg, k.Top):
		t.cursor = 0
	case m.matches(msg, k.Bottom):
		t.cursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		t.cursor = max(0, t.cursor-m.listPageStep())
	case m.matches(msg, k.HalfDown):
		t.cursor = min(cursorBottom(n), t.cursor+m.listPageStep())
	case m.matches(msg, k.Open):
		if t.cursor < n {
			cmd = attachTerminal(c, list[t.cursor], t.row())
		}
	case m.matches(msg, k.TabNext):
		cmd = t.stepTab(c, 1)
	case m.matches(msg, k.TabPrev):
		cmd = t.stepTab(c, -1)
	case m.matches(msg, terminalKeys.New):
		cmd = t.create(c)
	case m.matches(msg, terminalKeys.Rename):
		if t.cursor < n {
			cmd = t.startRename(list[t.cursor])
		}
	case m.matches(msg, terminalKeys.Kill):
		if t.cursor < n {
			t.killID = list[t.cursor].ID
		}
	case m.matches(msg, k.Refresh):
		cmd = m.loadTerminalsCmd()
	case m.matches(msg, k.Back):
		if m.sidebarVisible() {
			c.focusTree()
		} else {
			c.home()
		}
	default:
		return t, nil, false
	}
	return t, cmd, true
}

func (t terminalsComp) stepTab(c *ctx, d int) tea.Cmd {
	if t.nodeID != "" {
		return openNodeSummary(c, t.nodeID)
	}
	return stepHomeTab(c, tabTerminals, d)
}

func (t terminalsComp) create(c *ctx) tea.Cmd {
	m := c.m
	if t.nodeID != "" {
		return m.createTerminalCmd(t.nodeID)
	}
	if len(m.nodeInfo) == 0 {
		return m.createTerminalCmd("")
	}
	nodes := m.terminalNodes()
	switch len(nodes) {
	case 0:
		c.setFlash("no node has tmux")
		return nil
	case 1:
		return m.createTerminalCmd(nodes[0].ID)
	}
	c.openPopup(terminalNodePicker{nodes: nodes})
	return nil
}

func (t *terminalsComp) startRename(x api.Terminal) tea.Cmd {
	t.renameID = x.ID
	t.input = newProjectsInput()
	t.input.SetValue(x.Name)
	return t.input.Focus()
}

func (t terminalsComp) renameKey(c *ctx, msg tea.KeyPressMsg) (terminalsComp, tea.Cmd) {
	switch {
	case msg.Code == tea.KeyEscape:
		t.renameID = ""
		return t, nil
	case msg.String() == "enter":
		id, name := t.renameID, strings.TrimSpace(t.input.Value())
		t.renameID = ""
		if name == "" {
			return t, nil
		}
		return t, c.m.renameTerminalCmd(id, name)
	}
	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	return t, cmd
}

func (t terminalsComp) tabsHeader(c *ctx, x int) string {
	if t.nodeID != "" {
		c.m.hitNodeTabs(c, x)
		return c.m.nodeTabs(nodeTabTerminals)
	}
	c.m.hitHomeTabs(c, x)
	return c.m.homeTabs(tabTerminals)
}

func (t terminalsComp) clickTab(c *ctx, i int) tea.Cmd {
	switch {
	case t.nodeID != "":
		if nodeTab(i) == nodeTabProjects {
			return openNodeSummary(c, t.nodeID)
		}
		return nil
	case c.m.homeTabAt(i) == tabTerminals:
		return nil
	}
	return switchHomeTab(c, c.m.homeTabAt(i))
}

func (t terminalsComp) view(c *ctx, w, h int) string {
	m := c.m
	cardW := historyWidth(w)
	title := t.tabsHeader(c, centerGutter(cardW, w))
	list := t.shown(*m)
	var body string
	switch {
	case !m.terminalsDone:
		body = dimStyle.Render("loading terminals…")
	case m.terminalsErr != nil && len(list) == 0:
		body = dimStyle.Render("error: " + m.terminalsErr.Error())
	case len(list) == 0:
		body = dimStyle.Render("no terminals · " + m.keyText(terminalKeys.New) + " new")
	default:
		body = t.rows(c.below(2), list, cardW, max(1, h-2))
	}
	return centerBlock(title+"\n\n"+body, cardW, w)
}

func (t terminalsComp) rows(c *ctx, list []api.Terminal, w, avail int) string {
	nodes := map[string]bool{}
	for _, x := range list {
		nodes[x.NodeID] = true
	}
	grouped := t.nodeID == "" && max(len(nodes), len(c.m.terminalNodes())) > 1
	cursor := min(t.cursor, len(list)-1)
	l := itemLines{key: "terminals"}
	prev := ""
	for i, x := range list {
		if grouped && (i == 0 || x.NodeID != prev) {
			if i > 0 {
				l.text("")
			}
			l.text(StyleSecondaryBold.Render(nodeName(x.NodeLabel, x.NodeID)))
			prev = x.NodeID
		}
		sub := x.Cwd
		if x.Attached {
			sub += " · attached"
		}
		l.add(i, spawnChoiceRow(terminalTitle(x), sub, i == cursor, w))
	}
	return strings.Join(l.window(c, cursor, avail), "\n")
}

func (t terminalsComp) click(c *ctx, tg hitTarget, focused bool) (component, tea.Cmd) {
	list := t.shown(*c.m)
	switch {
	case tg.kind == hitTab:
		return t, t.clickTab(c, tg.index)
	case tg.index >= len(list):
	case focused && tg.index == t.cursor:
		return t, attachTerminal(c, list[tg.index], t.row())
	default:
		t.cursor = tg.index
	}
	return t, nil
}

func (t terminalsComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	t.cursor = cursorBy(t.cursor, d, len(t.shown(*c.m)))
	return t, nil
}

func (t terminalsComp) menu(c *ctx) []binding {
	if t.cursor >= len(t.shown(*c.m)) {
		return nil
	}
	return []binding{listKeys.Open, terminalKeys.Rename, terminalKeys.Kill}
}

func (t terminalsComp) footer(c *ctx) []binding {
	k := listKeys
	if len(t.shown(*c.m)) == 0 {
		return []binding{k.TabNext, terminalKeys.New, k.Refresh, projectsKeys.Help}
	}
	return []binding{k.TabNext, terminalKeys.New, terminalKeys.Rename, terminalKeys.Kill, k.Refresh, projectsKeys.Help}
}

func (t terminalsComp) footerPrompt(c *ctx) string {
	switch {
	case t.killID != "":
		x, _ := c.m.terminalByID(t.killID)
		return asstStyle.Render("kill terminal " + terminalTitle(x) + "? its shell stops · y/n")
	case t.renameID != "":
		return asstStyle.Render("rename: " + t.input.View() + "  enter rename · esc cancel")
	}
	return ""
}

func (m model) terminalsAt() int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if _, ok := m.main[i].(terminalsComp); ok {
			return i
		}
	}
	return -1
}

func (m model) updateTerminals(msg terminalActionMsg) (tea.Model, tea.Cmd) {
	i := m.terminalsAt()
	if i < 0 {
		if msg.err != nil {
			m.flash = msg.verb + ": " + msg.err.Error()
		}
		return m, m.loadTerminalsCmd()
	}
	c := &ctx{m: &m}
	comp, cmd := m.main[i].update(c, msg)
	m.main = m.main.replaceAt(i, comp)
	return m, tea.Batch(cmd, m.apply(c))
}
