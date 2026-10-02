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
	if req.loadDir != nil || req.openFile != nil {
		return t, t.run(c, req), true
	}
	return t, nil, used
}

func (t fileTreeComp) run(c *ctx, req treeRequest) tea.Cmd {
	switch {
	case req.loadDir != nil:
		return c.m.fetchListDir(t.ws, *req.loadDir)
	case req.openFile != nil:
		p := *req.openFile
		fileComp{ws: t.ws, path: p, loading: true}.show(c)
		c.focusOn(mainPane)
		return c.m.fetchReadFile(t.ws, p)
	}
	return nil
}

func (t fileTreeComp) click(c *ctx, h hitTarget, focused bool) (component, tea.Cmd) {
	if h.kind == hitFold {
		t.cursor = h.index
		cmd := t.run(c, t.toggle(t.rows()))
		return t, cmd
	}
	if focused && h.index == t.cursor {
		cmd := t.run(c, t.enter(t.rows()))
		return t, cmd
	}
	t.cursor = h.index
	return t, nil
}

func (t fileTreeComp) wheel(_ *ctx, d int) (component, tea.Cmd) {
	t.cursor = cursorBy(t.cursor, d, len(t.rows()))
	return t, nil
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
	return t.fileTree.view(c, w, h, c.m.focused == rightSidebar)
}

func (t fileTreeComp) footerText(c *ctx) string { return c.m.right.footerText(c) }

func (t fileTreeComp) footer(c *ctx) []binding {
	k := projectsKeys
	return []binding{k.SideTabNext, helpAs(k.Left, "fold")}
}
