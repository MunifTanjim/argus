package tui

import (
	"log"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

// leftTab is the left sidebar's tab.
type leftTab int

const (
	leftProjects leftTab = iota
	leftSessions
	leftBots
)

var leftTabNames = []string{"Projects", "Sessions", "Bots"}

// key is the tab's name in tui.json.
func (t leftTab) key() string { return strings.ToLower(leftTabNames[t]) }

func parseLeftTab(s string) (leftTab, bool) {
	i := slices.IndexFunc(leftTabNames, func(n string) bool { return strings.ToLower(n) == s })
	return leftTab(max(i, 0)), i >= 0
}

type leftSidebarState struct {
	tab      leftTab
	tree     projectTreeComp
	sessions sidebarSessionsComp
	bots     botsSidebarComp
	hidden   bool // the user toggled it off
	width    int  // 0 = default
}

func (l leftSidebarState) current() component {
	switch l.tab {
	case leftSessions:
		return l.sessions
	case leftBots:
		return l.bots
	}
	return l.tree
}

func (l leftSidebarState) store(comp component) leftSidebarState {
	switch comp := comp.(type) {
	case projectTreeComp:
		l.tree = comp
	case sidebarSessionsComp:
		l.sessions = comp
	case botsSidebarComp:
		l.bots = comp
	}
	return l
}

func (l leftSidebarState) rawWidth() int {
	if l.width == 0 {
		return 34
	}
	return l.width
}

func (l leftSidebarState) handleKey(c *ctx, msg tea.KeyPressMsg) (leftSidebarState, bool) {
	k, m := projectsKeys, c.m
	switch {
	case m.matches(msg, k.Widen):
		return l.resize(c, 4), true
	case m.matches(msg, k.Narrow):
		return l.resize(c, -4), true
	case m.matches(msg, k.SideTabNext), m.matches(msg, k.SideTabPrev):
		d, n := 1, len(leftTabNames)
		if m.matches(msg, k.SideTabPrev) {
			d = -1
		}
		return l.setTab(c, leftTab((int(l.tab)+d+n)%n)), true
	}
	return l, false
}

// setTab shows tab t and saves the choice.
func (l leftSidebarState) setTab(c *ctx, t leftTab) leftSidebarState {
	l.tab = t
	if path := c.m.statePath; path != "" {
		if err := saveLeftTab(path, t); err != nil {
			log.Printf("tui state: %v", err)
		}
	}
	return l
}

func (l leftSidebarState) setWidth(c *ctx, w int) leftSidebarState {
	l.width = clampWidth(w, 20, c.m.leftMaxW())
	return l
}

func (l leftSidebarState) resize(c *ctx, d int) leftSidebarState {
	return l.setWidth(c, c.m.projectsLeftW()+d)
}

func (l leftSidebarState) view(c *ctx, w, h int) string {
	focused := c.m.focused == leftSidebar && len(c.m.popups) == 0
	margin := strings.Repeat(" ", screenMargin)
	strip := tabStrip(leftTabNames, int(l.tab), focused)
	var suffix string
	switch l.tab {
	case leftProjects:
		suffix = l.tree.titleSuffix()
	case leftSessions:
		suffix = l.sessions.titleSuffix(c)
	case leftBots:
		suffix = l.bots.titleSuffix(c)
	}
	// A suffix that does not fit after the strip takes the row under it.
	var below string
	if lipgloss.Width(strip+suffix) <= w {
		strip += suffix
	} else if text := strings.TrimSpace(xansi.Strip(suffix)); text != "" {
		below = margin + truncateLine(StyleDim.Render(text), max(1, w))
	}
	c.hitTabs(screenMargin, 0, 2, leftTabNames...)
	head := margin + truncateLine(strip, max(1, w)) + "\n" + below + "\n"
	return head + l.current().view(c.below(2), w, max(1, h-2))
}
