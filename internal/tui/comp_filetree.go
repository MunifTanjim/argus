package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"
)

type fileTreeComp struct{ fileTree }

func (t fileTreeComp) section() string           { return "file-tree" }
func (t fileTreeComp) raw(*ctx) bool             { return false }
func (t fileTreeComp) spins(*ctx) bool           { return false }
func (t fileTreeComp) fullScreen(*ctx) fullLevel { return notFull }
func (t fileTreeComp) close(*ctx) tea.Cmd        { return nil }
func (t fileTreeComp) offers(c *ctx) []binding   { return c.m.baseComp().offers(c) }
func (t fileTreeComp) commands(*ctx) []binding   { return sectionLists[t.section()].own }
func (t fileTreeComp) pageStep(c *ctx) int       { return c.m.baseComp().pageStep(c) }
func (t fileTreeComp) layer() layer              { return baseLayer }

func (t fileTreeComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	k := projectsKeys
	switch {
	case c.m.matches(msg, k.Back):
		c.focusOn(mainPane)
		return t, nil, true
	case c.m.matches(msg, k.Refresh):
		t, cmd := t.reload(c)
		return t, cmd, true
	}
	is := func(bs ...binding) bool { return c.m.matches(msg, bs...) }
	used := is(k.Up, k.Down, k.Top, k.Bottom, k.HalfUp, k.HalfDown, k.Left, k.Right, k.Enter)
	req := t.key(is, c.m.cardListPageStep())
	switch {
	case req.loadDir != nil:
		return t, c.m.fetchListDir(t.ws, *req.loadDir), true
	case req.openFile != nil:
		p := *req.openFile
		fileComp{ws: t.ws, path: p, loading: true}.show(c)
		c.focusOn(mainPane)
		return t, c.m.fetchReadFile(t.ws, p), true
	}
	return t, nil, used
}

// reload keeps the old listings until the answers replace them, so the tree
// keeps its shape and the cursor its path.
func (t fileTreeComp) reload(c *ctx) (fileTreeComp, tea.Cmd) {
	dirs := []string{""}
	for d := range t.dirs {
		if t.expanded[d] {
			dirs = append(dirs, d)
		} else if d != "" {
			delete(t.dirs, d)
		}
	}
	sort.Strings(dirs)
	cmds := make([]tea.Cmd, len(dirs))
	for i, d := range dirs {
		cmds[i] = c.m.fetchListDir(t.ws, d)
	}
	return t, tea.Batch(cmds...)
}

func (t fileTreeComp) load(c *ctx) (fileTreeComp, tea.Cmd) {
	if _, ok := t.dirs[""]; ok {
		return t, nil
	}
	t.dirs[""] = &treeDir{loading: true}
	return t, c.m.fetchListDir(t.ws, "")
}

func (t fileTreeComp) update(_ *ctx, msg tea.Msg) (component, tea.Cmd) {
	if msg, ok := msg.(listDirMsg); ok && msg.ws == t.ws {
		t.setDir(msg.dir, msg.entries, msg.err)
	}
	return t, nil
}

func (t fileTreeComp) view(c *ctx, w, h int) string {
	return t.fileTree.view(w, h, c.m.focused == rightSidebar)
}

func (t fileTreeComp) footerText(c *ctx) string { return c.m.right.footerText(c) }

func (t fileTreeComp) footer(c *ctx) []binding {
	k := projectsKeys
	return []binding{k.Up, k.SideTabNext, helpAs(k.Left, "fold"), helpAs(k.Enter, "open"), helpAs(k.Back, sidebarEscDesc(c))}
}
