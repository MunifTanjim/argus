package tui

import (
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// killID is the session awaiting a kill confirmation.
type homeComp struct {
	cursor int
	killID string
	filter filterPrompt
}

func (h homeComp) section() string                           { return "home" }
func (h homeComp) raw(*ctx) bool                             { return h.killID != "" || h.filter.on }
func (h homeComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return h, nil }
func (h homeComp) close(*ctx) tea.Cmd                        { return nil }
func (h homeComp) offers(*ctx) []binding                     { return sectionOffers[h.section()].keys }
func (h homeComp) commands(*ctx) []binding                   { return sectionLists[h.section()].own }
func (h homeComp) layer() layer                              { return baseLayer }
func (h homeComp) spins(c *ctx) bool                         { return c.m.anyWorking() }

// fullScreen gives the welcome splash the whole terminal.
func (h homeComp) fullScreen(c *ctx) fullLevel {
	if c.m.focused != leftSidebar && len(c.m.sessions) == 0 {
		return fullTerminal
	}
	return notFull
}

func (h homeComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k, n := c.m, listKeys, len(c.m.order)
	if id := h.killID; id != "" {
		h.killID = ""
		if msg.String() == "y" {
			return h, m.killCmd(id), true
		}
		return h, nil, true
	}
	if h.filter.on {
		var cmd tea.Cmd
		h.filter, cmd = h.filter.handleKey(c, msg)
		return h, cmd, true
	}
	c.setFlash("")
	var cmd tea.Cmd
	switch {
	case m.matches(msg, k.Up):
		h.cursor = cursorUp(h.cursor)
	case m.matches(msg, k.Down):
		h.cursor = cursorDown(h.cursor, n)
	case m.matches(msg, k.Top):
		h.cursor = 0
	case m.matches(msg, k.Bottom):
		h.cursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		h.cursor = max(0, h.cursor-m.cardListPageStep())
	case m.matches(msg, k.HalfDown):
		h.cursor = min(cursorBottom(n), h.cursor+m.cardListPageStep())
	case m.matches(msg, k.Open):
		if h.cursor < n {
			c.openSession(m.order[h.cursor])
		}
	case m.matches(msg, k.Jump):
		if h.cursor < n {
			cmd = jump(c, m.sessions[m.order[h.cursor]])
		}
	case m.matches(msg, k.TabNext):
		cmd = stepHomeTab(c, tabSessions, 1)
	case m.matches(msg, k.TabPrev):
		cmd = stepHomeTab(c, tabSessions, -1)
	case m.matches(msg, k.New):
		cmd = m.newSessionCmd()
	case m.matches(msg, k.Kill):
		if h.cursor < n {
			if refusal := killRefusal(m.sessions[m.order[h.cursor]]); refusal != "" {
				c.setFlash(refusal)
			} else {
				h.killID = m.order[h.cursor]
			}
		}
	case m.matches(msg, k.Refresh):
		// While disconnected, refresh means reconnect now, not a call over a dead connection.
		if m.reconnecting {
			m.client.Reconnect()
		} else {
			cmd = m.refreshCmd()
		}
	case m.matches(msg, k.Back):
		if m.sessionFilter != "" {
			c.setSessionFilter("")
		} else {
			c.focusTree()
		}
	case m.matches(msg, k.ActiveOnly):
		c.toggleActiveOnly()
	case m.matches(msg, k.Filter):
		h.filter, cmd = h.filter.start(m.sessionFilter)
	default:
		return h, nil, false
	}
	return h, cmd, true
}

func (h homeComp) view(c *ctx, w, ht int) string {
	m := c.m
	// The status bar shows connection and quarantine state when framed; the bare
	// splash has no bar, so it keeps them in its own header.
	bare := m.layout().bare
	title := m.homeTabs(tabSessions) + m.sessionFilterTitle()
	if bare {
		title = brandMark() + title
		if m.reconnecting {
			title += dimStyle.Render("  (reconnecting…)")
		}
	}
	// chrome counts non-content rows: title, the blank after it, the footer, and
	// the blank before it; the quarantine banner adds a second title row.
	chrome := 4
	if bare && m.quarantined() {
		title += "\n" + StyleErrorBold.Render("⚠ QUARANTINED") +
			dimStyle.Render("  pin this device: argus lock pin")
		chrome++
	}
	cardW := max(30, min(containerWidthOf(w), maxCardWidth))
	tabX := centerGutter(cardW, w)
	if bare {
		tabX += lipgloss.Width(brandMark())
	}
	m.hitHomeTabs(c, tabX)
	if len(m.sessions) == 0 {
		return h.welcome(c, title, chrome, w, ht)
	}
	if len(m.order) == 0 {
		hint := dimStyle.Render(m.emptyFilterHint("no active sessions", listKeys.Back))
		return centerBlock(title+"\n\n"+hint, cardW, w)
	}
	sel := h.cursor
	// On a gateway, a host header precedes each group.
	grouped := m.grouped()
	showAgent := m.multiAgent()
	var l itemLines
	for i, id := range m.order {
		s := m.sessions[id]
		if i > 0 {
			l.text("")
		}
		if i == 0 || sectionKey(s) != sectionKey(m.sessions[m.order[i-1]]) {
			switch {
			case s.Status == session.StatusAwaitingInput:
				l.text(m.needsYouHeader())
			case grouped:
				l.text(m.groupHeader(s.NodeLabel))
			}
		}
		l.add(i, m.sessionCard(s, i == sel, cardW, showAgent))
	}
	lines := l.window(c.below(lipgloss.Height(title)+1), sel, max(1, ht-chrome))
	return centerBlock(title+"\n\n"+strings.Join(lines, "\n"), cardW, w)
}

func (h homeComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	switch {
	case t.kind == hitTab:
		if c.m.homeTabAt(t.index) != tabSessions {
			return h, switchHomeTab(c, c.m.homeTabAt(t.index))
		}
	case t.index >= len(c.m.order):
	case focused && t.index == h.cursor:
		c.openSession(c.m.order[t.index])
	default:
		h.cursor = t.index
	}
	return h, nil
}

func (h homeComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	h.cursor = cursorBy(h.cursor, d, len(c.m.order))
	return h, nil
}

func (h homeComp) menu(c *ctx) []binding {
	if h.cursor >= len(c.m.order) {
		return nil
	}
	return []binding{listKeys.Jump, listKeys.Kill}
}

func switchHomeTab(c *ctx, t homeTab) tea.Cmd {
	switch t {
	case tabSessions:
		c.replaceBase(c.m.homePane())
	case tabHistory:
		return openHistory(c)
	case tabTerminals:
		return openTerminals(c)
	case tabLogs:
		openLogs(c)
	}
	return nil
}

// stepHomeTab shows the Home tab d places from from, wrapping.
func stepHomeTab(c *ctx, from homeTab, d int) tea.Cmd {
	tabs := c.m.homeTabList()
	n := len(tabs)
	i := max(0, slices.Index(tabs, from))
	return switchHomeTab(c, tabs[(i+d+n)%n])
}

func openTerminals(c *ctx) tea.Cmd {
	c.replaceBase(terminalsComp{})
	return c.m.loadTerminalsCmd()
}

func (h homeComp) welcome(c *ctx, title string, chrome, w, ht int) string {
	m := c.m
	textW := max(16, min(w-2, 52))
	center := lipgloss.NewStyle().Width(textW).Align(lipgloss.Center)
	hint := dimStyle.Render("No sessions yet. Start an AI agent in a tmux pane, or press ") +
		StyleAccentBold.Render(m.keyText(listKeys.New)) + dimStyle.Render(" to spawn one right here.")
	welcome := lipgloss.JoinVertical(lipgloss.Center,
		argusLogo(w, ht),
		"",
		headerStyle.Render("Argus"),
		center.Render(StyleSecondary.Render("Watch and control all your AI agents.")),
		"",
		center.Render(hint),
	)
	avail := max(1, ht-chrome)
	top := max(0, (avail-lipgloss.Height(welcome))/2)
	block := strings.Repeat("\n", top) + centerBlock(welcome, lipgloss.Width(welcome), w)
	cardW := max(30, min(containerWidthOf(w), maxCardWidth))
	return centerBlock(title, cardW, w) + "\n\n" + block
}

func (h homeComp) footer(c *ctx) []binding {
	k := listKeys
	if len(c.m.sessions) == 0 {
		return []binding{k.TabNext, k.New, k.Refresh, c.m.listBackKey()}
	}
	clear := clearFilterKey(k.Back, c.m.sessionFilter != "")
	if len(c.m.order) == 0 {
		return []binding{k.ActiveOnly, k.Filter, clear, k.TabNext, k.New, k.Refresh, c.m.quitKey(), projectsKeys.Help}
	}
	return []binding{k.Jump, k.TabNext, k.New, k.Kill, k.ActiveOnly, k.Filter, clear, k.Refresh, c.m.quitKey(), projectsKeys.Help}
}

func (h homeComp) footerPrompt(c *ctx) string {
	m := c.m
	if h.killID != "" {
		return asstStyle.Render(killPrompt(m.sessions[h.killID]))
	}
	if h.filter.on {
		return h.filter.view()
	}
	return ""
}

// jump reveals s's tmux pane in the terminal argus runs in.
func jump(c *ctx, s session.Session) tea.Cmd {
	host, _ := os.Hostname()
	paneID, reason := planJump(s, host, os.Getenv("TMUX"))
	if reason != "" {
		c.setFlash(reason)
		return nil
	}
	return jumpCmd(paneID)
}

func (h homeComp) pageStep(c *ctx) int { return c.m.listPageStep() }
