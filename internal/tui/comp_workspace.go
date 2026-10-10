package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// killID is the session awaiting a kill confirmation.
type workspaceComp struct {
	ws     string
	cursor int
	killID string
	filter filterPrompt
}

func (p workspaceComp) section() string                           { return "workspace" }
func (p workspaceComp) raw(*ctx) bool                             { return p.killID != "" || p.filter.on }
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
	if p.filter.on {
		var cmd tea.Cmd
		p.filter, cmd = p.filter.handleKey(c, msg)
		return p, cmd, true
	}
	c.setFlash("")
	if m.matches(msg, listKeys.Filter) {
		var cmd tea.Cmd
		p.filter, cmd = p.filter.start(m.sessionFilter)
		return p, cmd, true
	}
	switch {
	case m.matches(msg, projectsKeys.Spawn):
		return p, spawnSession(c, p.row(c)), true
	case (m.matches(msg, listKeys.TabNext) || m.matches(msg, listKeys.TabPrev)) && m.wsHasTerminals(p.ws):
		return p, openWorkspaceTerminals(c, p.ws), true
	case m.matches(msg, projectsKeys.SetupLog):
		return p, openSetupLog(c, p.ws), true
	case paneTreeKey(c, msg):
		return p, nil, true
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
		switch {
		case m.sessionFilter != "":
			c.setSessionFilter("")
		case m.sidebarVisible():
			c.focusTree()
		default:
			c.home()
		}
	case m.matches(msg, listKeys.ActiveOnly):
		c.toggleActiveOnly()
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

func (p workspaceComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	ss := c.m.wsSessions(p.ws)
	switch {
	case t.kind == hitTab:
		if wsTab(t.index) == wsTabTerminals {
			return p, openWorkspaceTerminals(c, p.ws)
		}
	case t.index >= len(ss):
	case focused && t.index == p.cursor:
		c.openSession(ss[t.index].ID)
	default:
		p.cursor = t.index
	}
	return p, nil
}

func (p workspaceComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, len(c.m.wsSessions(p.ws)))
	return p, nil
}

func (p workspaceComp) menu(c *ctx) []binding {
	if p.cursor >= len(c.m.wsSessions(p.ws)) {
		return nil
	}
	return []binding{listKeys.Jump, listKeys.Kill}
}

func paneTreeKey(c *ctx, msg tea.KeyPressMsg) bool {
	m, k := c.m, projectsKeys
	switch {
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
	default:
		return false
	}
	return true
}

func (p workspaceComp) row(c *ctx) projectsRow {
	r, _ := c.m.wsRow(p.ws)
	return r
}

func (p workspaceComp) view(c *ctx, w, h int) string {
	cardW := min(w, maxCardWidth)
	return centerBlock(p.column(c, centerGutter(cardW, w), cardW, max(1, h-footerRows)), cardW, w)
}

func (p workspaceComp) paneHeader(c *ctx, w int) (string, int) {
	if r, ok := c.m.wsRow(p.ws); ok {
		return c.m.wsHeader(r), centerGutter(min(w, maxCardWidth), w)
	}
	return "", 0
}

// column draws the pane's card column; x is its offset in the pane.
func (p workspaceComp) column(c *ctx, x, w, h int) string {
	if _, ok := c.m.wsRow(p.ws); !ok {
		return dimStyle.Render("workspace not found")
	}
	head := truncateLine(c.m.wsTabsHeader(c, p.ws, wsTabSessions, x)+c.m.sessionFilterTitle(), w)
	return head + "\n\n" + p.sessions(c.below(2), w, max(1, h-2))
}

func (p workspaceComp) sessions(c *ctx, w, avail int) string {
	m := c.m
	block := m.setupBlock(p.ws, min(w, maxCardWidth))
	avail = max(1, avail-lipgloss.Height(block))
	ss := m.wsSessions(p.ws)
	if len(ss) == 0 {
		if m.activeOnly || m.sessionFilter != "" {
			return block + dimStyle.Render(m.emptyFilterHint("no active sessions in this workspace", projectsKeys.Back))
		}
		return block + dimStyle.Render("no sessions in this workspace")
	}
	focused := m.focused == mainPane
	cardW := min(w, maxCardWidth)
	cards := make([]string, len(ss))
	for i, s := range ss {
		cards[i] = m.sessionCard(s, focused && i == p.cursor, cardW, true, s.Status == session.StatusAwaitingInput && m.grouped())
	}
	cursor := 0
	if focused {
		cursor = p.cursor
	}
	return block + renderCardList(c.below(strings.Count(block, "\n")), "workspace:"+p.ws, cards, cursor, avail)
}

func (p workspaceComp) footerPrompt(c *ctx) string {
	if p.killID != "" {
		return asstStyle.Render(killPrompt(c.m.sessions[p.killID]))
	}
	if p.filter.on {
		return p.filter.view()
	}
	return ""
}

func (p workspaceComp) footer(c *ctx) []binding {
	k := projectsKeys
	out := []binding{listKeys.Jump, k.Spawn, listKeys.Kill, listKeys.ActiveOnly, listKeys.Filter,
		clearFilterKey(k.Back, c.m.sessionFilter != "")}
	if c.m.wsHasTerminals(p.ws) {
		out = append(out, listKeys.TabNext)
	}
	return append(out, k.Help)
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
