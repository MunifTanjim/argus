package tui

import (
	"log"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// leftTab is the left sidebar's tab.
type leftTab int

const (
	leftProjects leftTab = iota
	leftSessions
)

var leftTabNames = []string{"Projects", "Sessions"}

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
	hidden   bool // the user toggled it off
	width    int  // 0 = default
}

func (l leftSidebarState) current() component {
	if l.tab == leftSessions {
		return l.sessions
	}
	return l.tree
}

func (l leftSidebarState) store(comp component) leftSidebarState {
	switch comp := comp.(type) {
	case projectTreeComp:
		l.tree = comp
	case sidebarSessionsComp:
		l.sessions = comp
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
	strip := tabStrip(leftTabNames, int(l.tab), focused)
	if l.tab == leftProjects {
		strip += l.tree.titleSuffix()
	} else {
		strip += l.sessions.titleSuffix()
	}
	c.hitTabs(screenMargin, 0, 2, leftTabNames...)
	head := strings.Repeat(" ", screenMargin) + truncateLine(strip, max(1, w)) + "\n\n"
	return head + l.current().view(c.below(2), w, max(1, h-2))
}
