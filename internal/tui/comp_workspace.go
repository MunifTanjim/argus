package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// killID is the session awaiting a kill confirmation.
type workspaceComp struct {
	ws     string
	cursor int
	killID string
}

func (p workspaceComp) section() string                           { return "workspace" }
func (p workspaceComp) raw(*ctx) bool                             { return p.killID != "" }
func (p workspaceComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return p, nil }
func (p workspaceComp) fullScreen(*ctx) fullLevel                 { return notFull }
func (p workspaceComp) close(*ctx) tea.Cmd                        { return nil }
func (p workspaceComp) offers(*ctx) []binding                     { return sectionOffers[p.section()].keys }
func (p workspaceComp) commands(*ctx) []binding                   { return sectionLists[p.section()].own }
func (p workspaceComp) layer() layer                              { return baseLayer }
func (p workspaceComp) pageStep(c *ctx) int                       { return cardPageStep(c.m.paneRows()) }
func (p workspaceComp) workspace(*ctx) string                     { return p.ws }
func (p workspaceComp) spins(c *ctx) bool                         { return c.m.anyWorking() }

func (p workspaceComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m := c.m
	if id := p.killID; id != "" {
		p.killID = ""
		if msg.String() == "y" {
			return p, m.killCmd(id), true
		}
		return p, nil, true
	}
	c.setFlash("")
	if cmd, ok := paneTreeKey(c, msg, p.row(c)); ok {
		return p, cmd, true
	}
	// A file over the pane passes it only the keys that act on the tree.
	if _, onTop := m.main.top().(workspaceComp); !onTop {
		return p, nil, false
	}
	ss := m.wsSessions(p.ws)
	var cmd tea.Cmd
	switch {
	case m.matches(msg, projectsKeys.Up):
		p.cursor = cursorUp(p.cursor)
	case m.matches(msg, projectsKeys.Down):
		p.cursor = cursorDown(p.cursor, len(ss))
	case m.matches(msg, projectsKeys.Top):
		p.cursor = 0
	case m.matches(msg, projectsKeys.Bottom):
		p.cursor = cursorBottom(len(ss))
	case m.matches(msg, projectsKeys.HalfUp):
		p.cursor = max(0, p.cursor-m.cardListPageStep())
	case m.matches(msg, projectsKeys.HalfDown):
		p.cursor = min(cursorBottom(len(ss)), p.cursor+m.cardListPageStep())
	case m.matches(msg, listKeys.Jump):
		if p.cursor < len(ss) {
			cmd = jump(c, ss[p.cursor])
		}
	case m.matches(msg, projectsKeys.Enter):
		if p.cursor < len(ss) {
			c.openSession(ss[p.cursor].ID)
		}
	case m.matches(msg, projectsKeys.Back):
		if m.sidebarVisible() {
			c.focusTree()
		} else {
			c.home()
		}
	case m.matches(msg, listKeys.Kill):
		if p.cursor < len(ss) {
			s := ss[p.cursor]
			if refusal := killRefusal(s); refusal != "" {
				c.setFlash(refusal)
			} else {
				p.killID = s.ID
			}
		}
	default:
		return p, nil, false
	}
	return p, cmd, true
}

func paneTreeKey(c *ctx, msg tea.KeyPressMsg, r projectsRow) (tea.Cmd, bool) {
	m, k := c.m, projectsKeys
	var cmd tea.Cmd
	switch {
	case m.matches(msg, k.Filter):
		if m.sidebarVisible() {
			c.onTree(treeFilter)
		} else {
			c.setFlash("the filter needs the tree · " + m.keyText(k.ToggleSidebar) + " shows the tree")
		}
	case m.matches(msg, k.Spawn):
		cmd = spawnSession(c, r)
	case m.matches(msg, k.SetupLog):
		ws := ""
		if r.kind == rowWorkspace {
			ws = r.id
		}
		cmd = openSetupLog(c, ws)
	case m.matches(msg, k.ShowHidden):
		c.onTree(treeToggleHidden)
	case m.matches(msg, k.ShowGone):
		c.onTree(treeToggleGone)
	case m.matches(msg, k.Refresh):
		c.onTree(treeReload)
	case m.matches(msg, k.Widen):
		c.resizeTree(4)
	case m.matches(msg, k.Narrow):
		c.resizeTree(-4)
	case m.matches(msg, k.New, k.Rename, k.Hide, k.Unhide, k.Pin, k.Unpin, k.Target, k.ForceRemove, k.Forget, k.RunSetup):
		c.setFlash("manage keys work in the tree · " + m.keyText(k.Back) + " to go there")
	default:
		return nil, false
	}
	return cmd, true
}

func (p workspaceComp) row(c *ctx) projectsRow {
	r, _ := c.m.wsRow(p.ws)
	return r
}

func (p workspaceComp) treeKey(c *ctx, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	return paneTreeKey(c, msg, p.row(c))
}

func (p workspaceComp) view(c *ctx, w, h int) string {
	cardW := min(w, maxCardWidth)
	return centerBlock(p.column(c, cardW, max(1, h-footerRows)), cardW, w)
}

// fileHeader stays over a file opened on the pane.
func (p workspaceComp) fileHeader(c *ctx, w int) string {
	r, ok := c.m.wsRow(p.ws)
	if !ok {
		return ""
	}
	cardW := min(w, maxCardWidth)
	return centerBlock(truncateLine(c.m.wsHeader(r), cardW), cardW, w)
}

func (p workspaceComp) column(c *ctx, w, h int) string {
	r, ok := c.m.wsRow(p.ws)
	if !ok {
		return dimStyle.Render("workspace not found")
	}
	return truncateLine(c.m.wsHeader(r), w) + "\n\n" + p.sessions(c, w, max(1, h-2))
}

func (p workspaceComp) sessions(c *ctx, w, avail int) string {
	m := c.m
	block := m.setupBlock(p.ws, min(w, maxCardWidth))
	avail = max(1, avail-lipgloss.Height(block))
	ss := m.wsSessions(p.ws)
	if len(ss) == 0 {
		return block + dimStyle.Render("no sessions in this workspace")
	}
	focused := m.focused == mainPane
	cardW := min(w, maxCardWidth)
	cards := make([]string, len(ss))
	for i, s := range ss {
		cards[i] = m.sessionCard(s, focused && i == p.cursor, cardW, true)
	}
	cursor := 0
	if focused {
		cursor = p.cursor
	}
	return block + renderCardList(cards, cursor, avail)
}

func (p workspaceComp) footerText(c *ctx) string {
	if p.killID != "" {
		return asstStyle.Render(killPrompt(c.m.sessions[p.killID]))
	}
	return c.m.treeScreenFooter(p.footer(c)...)
}

func (p workspaceComp) footer(c *ctx) []binding {
	k := projectsKeys
	bindings := []binding{k.Up, k.Enter, listKeys.Jump, k.Spawn, listKeys.Kill}
	if c.m.sidebarVisible() || c.m.nextFromPane() == "files" {
		bindings = append(bindings, helpAs(paneKeys.Next, c.m.nextFromPane()))
	}
	return append(bindings, k.Help, helpAs(k.Back, "tree"))
}

// disarmRoot drops a kill confirmation on the root pane before a component
// covers it, so an unseen prompt cannot take a later key.
func (m *model) disarmRoot() {
	switch p := m.rootComp().(type) {
	case homeComp:
		p.killID = ""
		m.main = m.main.replaceAt(0, p)
	case workspaceComp:
		p.killID = ""
		m.main = m.main.replaceAt(0, p)
	}
}

func (m model) rootComp() component {
	if len(m.main) == 0 {
		return nil
	}
	return m.main[0]
}
