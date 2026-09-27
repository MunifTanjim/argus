package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type sideTab int

const (
	sideFiles sideTab = iota
	sideChanges
)

var sideTabNames = []string{"Files", "Changes"}

type rightSidebarState struct {
	tab      sideTab
	fileTree fileTreeComp
	changes  changesComp
	hidden   bool // the user toggled it off
	width    int  // 0 = default
	ws       string
}

func (r rightSidebarState) current() component {
	if r.tab == sideChanges {
		return r.changes
	}
	return r.fileTree
}

func (r rightSidebarState) store(comp component) rightSidebarState {
	switch comp := comp.(type) {
	case fileTreeComp:
		r.fileTree = comp
	case changesComp:
		r.changes = comp
	}
	return r
}

func (r rightSidebarState) rawWidth() int {
	if r.width == 0 {
		return filesDefaultW
	}
	return r.width
}

func (r rightSidebarState) tabLabel() string { return strings.ToLower(sideTabNames[r.tab]) }

func (r rightSidebarState) handleKey(c *ctx, msg tea.KeyPressMsg) (rightSidebarState, bool) {
	k := projectsKeys
	m := c.m
	switch {
	case m.matches(msg, k.Widen), m.matches(msg, k.Narrow):
		d := 4
		if m.matches(msg, k.Narrow) {
			d = -4
		}
		return r.resize(c, d), true
	case m.matches(msg, k.SideTabNext), m.matches(msg, k.SideTabPrev):
		d := 1
		if m.matches(msg, k.SideTabPrev) {
			d = -1
		}
		r.tab = sideTab((int(r.tab) + d + len(sideTabNames)) % len(sideTabNames))
		return r, true
	}
	return r, false
}

func (r rightSidebarState) resize(c *ctx, d int) rightSidebarState {
	maxW := c.m.filesMaxW()
	r.width = clampWidth(clampWidth(r.rawWidth(), 20, maxW)+d, 20, maxW)
	return r
}

// Each component drops a reply that no longer matches its workspace or request.
func (r rightSidebarState) update(c *ctx, msg tea.Msg) (rightSidebarState, tea.Cmd) {
	var comp component
	var cmd tea.Cmd
	switch msg.(type) {
	case listDirMsg:
		comp, cmd = r.fileTree.update(c, msg)
	case changedFilesMsg, commitsMsg, commitFilesMsg:
		comp, cmd = r.changes.update(c, msg)
	default:
		return r, nil
	}
	return r.store(comp), cmd
}

func (r rightSidebarState) show(c *ctx, ws string) (rightSidebarState, tea.Cmd) {
	r.ws = ws
	if ws != r.fileTree.ws {
		r.fileTree = fileTreeComp{newFileTree(ws)}
	}
	if ws != r.changes.ws {
		r.changes = changesComp{ws: ws, gen: r.changes.gen}
	}
	if ws == "" || !c.m.filesVisible() {
		return r, nil
	}
	var cmd tea.Cmd
	if r.tab == sideChanges {
		r.changes, cmd = r.changes.load(c)
	} else {
		r.fileTree, cmd = r.fileTree.load(c)
	}
	return r, cmd
}

func (r rightSidebarState) view(c *ctx, w, h int) string {
	gutter := strings.Repeat(" ", screenMargin)
	focused := c.m.focused == rightSidebar
	head := gutter + truncateLine(r.tabStrip(focused), max(1, w-screenMargin)) + "\n\n"
	if r.ws == "" {
		return head + gutter + dimStyle.Render("select a workspace")
	}
	return head + r.current().view(c, w, max(1, h-2))
}

func (r rightSidebarState) tabStrip(focused bool) string {
	parts := make([]string, len(sideTabNames))
	for i, n := range sideTabNames {
		parts[i] = StyleDim.Render(n)
		if sideTab(i) == r.tab {
			parts[i] = paneTitle(n, focused)
			if !focused {
				parts[i] = StylePrimaryBold.Render(n)
			}
		}
	}
	return strings.Join(parts, StyleDim.Render("  "))
}

func (r rightSidebarState) footerText(c *ctx) string {
	m, k := c.m, projectsKeys
	keys := r.current().footer(c)
	if offersKey(r.current().offers(c), k.Help) {
		return m.treeScreenFooter(append(keys, m.sideKey(k.ToggleFiles), k.Help)...)
	}
	return m.sessionHint(append(keys, k.Refresh)...)
}

// sidebarEscDesc names where back goes from the top level of a tab.
func sidebarEscDesc(c *ctx) string {
	if c.m.onTreeScreen() {
		return "pane"
	}
	return "back"
}
