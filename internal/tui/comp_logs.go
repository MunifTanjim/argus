package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// logsComp is the embedded node's log tail. scroll is the absolute top-line
// offset while paused; follow pins the newest line and ignores scroll. At ring
// capacity a paused offset addresses shifting content as old lines evict.
type logsComp struct {
	scroll int
	follow bool
}

func newLogsComp() logsComp { return logsComp{follow: true} }

func (l logsComp) section() string                           { return "logs" }
func (l logsComp) raw(*ctx) bool                             { return false }
func (l logsComp) spins(*ctx) bool                           { return false }
func (l logsComp) close(*ctx) tea.Cmd                        { return nil }
func (l logsComp) offers(*ctx) []binding                     { return sectionOffers[l.section()].keys }
func (l logsComp) commands(*ctx) []binding                   { return sectionLists[l.section()].own }
func (l logsComp) layer() layer                              { return baseLayer }
func (l logsComp) pageStep(c *ctx) int                       { return c.m.listPageStep() }
func (l logsComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return l, nil }

// Long log lines read best edge to edge.
func (l logsComp) fullScreen(c *ctx) fullLevel {
	if c.m.sidebarVisible() {
		return notFull
	}
	return fullWidth
}

func (l logsComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k := c.m, logsKeys
	c.setFlash("")
	switch {
	case m.matches(msg, k.Up):
		l = l.scrollBy(c, -1)
	case m.matches(msg, k.Down):
		l = l.scrollBy(c, 1)
	case m.matches(msg, k.HalfUp):
		l = l.scrollBy(c, -m.logsAvail()/2)
	case m.matches(msg, k.HalfDown):
		l = l.scrollBy(c, m.logsAvail()/2)
	case m.matches(msg, k.Top):
		l.follow, l.scroll = false, 0
	case m.matches(msg, k.Bottom):
		l.follow = true
	case m.matches(msg, listKeys.TabPrev):
		return l, openHistory(c), true
	case m.matches(msg, listKeys.TabNext), m.matches(msg, k.Back):
		c.replaceBase(c.m.homePane())
	default:
		return l, nil, false
	}
	return l, nil, true
}

func (l logsComp) scrollBy(c *ctx, delta int) logsComp {
	l = l.unfollow(c)
	bottom := c.m.logsBottom()
	l.scroll = max(0, min(l.scroll+delta, bottom))
	l.follow = l.scroll >= bottom
	return l
}

// unfollow pins the current bottom as an absolute offset before manual
// scrolling, so lines arriving while paused don't shift the viewport.
func (l logsComp) unfollow(c *ctx) logsComp {
	if l.follow {
		l.scroll = c.m.logsBottom()
		l.follow = false
	}
	return l
}

func (l logsComp) view(c *ctx, w, h int) string {
	m := c.m
	if !m.hasLogsTab() {
		return ""
	}
	title := m.homeTabs(tabLogs)
	// The tabs row lines up with the other home tabs (centered card column). A
	// full-screen body spans the terminal, so its tabs row adds the margin back.
	cardW := historyWidth(w)
	gutter := strings.Repeat(" ", max(0, (w-cardW)/2))
	if l.fullScreen(c) == fullWidth {
		gutter = strings.Repeat(" ", screenMargin+max(0, (m.frameWidth()-cardW)/2))
	}
	var body string
	if m.logs.Len() == 0 {
		body = dimStyle.Render("no logs yet")
	} else {
		avail := max(1, h-4)
		bottom := m.logsBottom()
		off := l.scroll
		if l.follow {
			off = bottom
		}
		off = max(0, min(off, bottom))
		// Each ring line wraps to one or more display lines, so a window of avail
		// ring lines fills the screen once clamped to avail.
		var disp []string
		for _, ln := range m.logs.LinesRange(off, avail) {
			disp = append(disp, strings.Split(ansi.Hardwrap(ln, w, false), "\n")...)
		}
		if len(disp) > avail {
			if l.follow {
				disp = disp[len(disp)-avail:]
			} else {
				disp = disp[:avail]
			}
		}
		body = strings.Join(disp, "\n")
	}
	return gutter + title + "\n\n" + body
}

func (l logsComp) footer(c *ctx) []binding {
	return []binding{listKeys.TabNext, logsKeys.Bottom, projectsKeys.Help}
}

// The Logs tab exists only with an embedded node.
func openLogs(c *ctx) {
	if c.m.hasLogsTab() {
		c.replaceBase(newLogsComp())
	}
}
