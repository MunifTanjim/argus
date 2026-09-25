package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// projectsFooter shows the keys that act in the current focus, tab, and viewer.
func (m model) projectsFooter() string {
	k := projectsKeys
	switch {
	case m.projects.inputMode == pmRename:
		return asstStyle.Render("rename: " + m.projects.input.View() + "  enter rename · esc cancel")
	case m.projects.inputMode == pmFilter:
		return asstStyle.Render("filter: " + m.projects.input.View() + "  enter keep · esc clear")
	case m.projects.pendingKill != "":
		return asstStyle.Render(killPrompt(m.sessions[m.projects.pendingKill]))
	case m.projects.pendingRemove != "":
		return asstStyle.Render(m.removePrompt())
	case m.projects.offerSpawn != nil:
		return asstStyle.Render("start an agent with this issue? y/n")
	case m.flash != "":
		return asstStyle.Render(firstLine(m.flash))
	case m.projects.create.active:
		return m.createFooter()
	case m.projects.retarget != nil:
		return m.footer(helpAs(k.Up, "↑/↓", "move"), helpAs(k.Enter, "enter", "select"), helpAs(k.Back, "esc", "cancel"))
	case m.projects.showHelp:
		return m.footer(helpAs(k.Back, "any key", "close"))
	case m.projects.focus == focusFiles && m.filesVisible():
		return m.footer(append(m.sidebarBindings("pane"), m.sideKey(k.ToggleFiles), k.Help)...)
	case m.projects.focus == focusTree && m.sidebarVisible():
		bindings := m.treeRowBindings()
		if m.projects.filter != "" {
			bindings = append(bindings, helpAs(k.Back, "esc", "clear filter"))
		}
		return m.footer(append(bindings, helpAs(k.Back, "q", "quit"), m.sideKey(k.ToggleFiles), k.Help)...)
	case m.projects.fileView.open() && m.projects.focus == focusPane:
		return m.footer(append(m.fileViewBindings(), k.Help)...)
	}
	bindings := []key.Binding{k.Up, k.Enter, listKeys.Jump, k.Spawn, listKeys.Kill}
	if m.sidebarVisible() || m.nextFromPane() == "files" {
		bindings = append(bindings, helpAs(k.Focus, "tab", m.nextFromPane()))
	}
	return m.footer(append(bindings, k.Help, helpAs(k.Back, "esc", "tree"))...)
}

// fileViewBindings are the keys of an open file or diff; J/K step only a diff.
func (m model) fileViewBindings() []key.Binding {
	k := projectsKeys
	b := []key.Binding{helpAs(k.Up, "↑/↓", "scroll"), helpAs(k.HalfDown, "^u/^d", "page"), helpAs(k.Bottom, "g/G", "ends")}
	if m.projects.fileView.diff {
		b = append(b, k.NextFile)
	}
	return append(b, k.Wrap, helpAs(k.Refresh, "r", "reload"), helpAs(k.Back, "esc", "close"))
}

// treeRowBindings are the tree keys that act on the selected row.
func (m model) treeRowBindings() []key.Binding {
	k := projectsKeys
	r, _ := m.cursorRow()
	switch r.kind {
	case rowHome:
		return []key.Binding{k.Up, k.Enter, k.Focus, k.Filter}
	case rowWorkspace:
		return []key.Binding{k.Up, k.Left, k.Enter, k.Focus, k.Spawn, k.New, k.Remove, k.Filter}
	case rowProject:
		return []key.Binding{k.Up, k.Left, k.Enter, k.New, k.Filter}
	}
	return []key.Binding{k.Up, k.Left, k.Enter, k.Filter}
}

// projectsHelpView lists every projects-screen key, grouped by where it acts.
func (m model) projectsHelpView() string {
	k := projectsKeys
	groups := []struct {
		title string
		keys  []key.Binding
	}{
		{"Tree", []key.Binding{
			helpAs(k.Up, "↑/↓ j/k", "move"),
			helpAs(k.Bottom, "g/G", "top / bottom"),
			helpAs(k.HalfDown, "^u/^d", "page"),
			helpAs(k.Left, "h ←", "fold / parent"),
			helpAs(k.Right, "l →", "unfold / open pane"),
			helpAs(k.Enter, "enter", "open / fold"),
			helpAs(k.Focus, "tab shift+tab", "cycle focus"),
			helpAs(k.Filter, "/", "filter"),
		}},
		{"Pane", []key.Binding{
			helpAs(k.Up, "↑/↓ j/k", "move / scroll"),
			helpAs(k.Enter, "enter", "open session"),
			helpAs(listKeys.Jump, "O", "jump to its tmux pane"),
			helpAs(k.Back, "esc", "close / tree"),
			helpAs(listKeys.Kill, "x", "kill session"),
			helpAs(sessionKeys.Files, "^f", "session: go to right sidebar"),
			helpAs(k.SideTabNext, "[/]", "right sidebar: Files / Changes"),
			helpAs(k.DiffMode, "t", "changes: uncommitted / vs target"),
			helpAs(k.Back, "esc h", "changes: back from a commit"),
			helpAs(k.NextFile, "J/K", "diff: next / previous file"),
			helpAs(k.Wrap, "w", "open file: wrap long lines"),
		}},
		{"Manage (tree)", []key.Binding{
			helpAs(k.New, "n", "new workspace (branch, PR, issue)"),
			helpAs(k.Remove, "x", "remove workspace"),
			helpAs(k.ForceRemove, "X", "force remove"),
			helpAs(k.Rename, "R", "rename project"),
			helpAs(k.Pin, "P", "pin project"),
			helpAs(k.Hide, "H", "hide project"),
			helpAs(k.ShowHidden, "z", "show hidden"),
			helpAs(k.ShowGone, "o", "show gone"),
			helpAs(k.Target, "T", "change target branch"),
		}},
		{"Screen", []key.Binding{
			helpAs(k.Spawn, "s", "spawn in the selected workspace"),
			helpAs(k.Widen, "</>", "resize focused sidebar"),
			helpAs(k.ToggleFiles, "^e", "toggle right sidebar"),
			helpAs(k.ToggleSidebar, "^b", "toggle sidebar"),
			helpAs(k.Refresh, "r", "refresh"),
			helpAs(k.Help, "?", "help"),
			helpAs(k.Back, "q", "quit (tree) · back (pane)"),
		}},
	}
	cols := make([]string, len(groups))
	for i, g := range groups {
		kw := 0
		for _, b := range g.keys {
			kw = max(kw, lipgloss.Width(b.Help().Key))
		}
		lines := []string{StyleAccentBold.Render(g.title)}
		for _, b := range g.keys {
			h := b.Help()
			lines = append(lines, StyleSecondary.Render(h.Key+strings.Repeat(" ", kw-lipgloss.Width(h.Key)))+"  "+StyleDim.Render(h.Desc))
		}
		cols[i] = strings.Join(lines, "\n")
	}
	// All groups side by side, else two per row, else stacked.
	for _, perRow := range []int{len(cols), 2} {
		if out := helpGrid(cols, perRow); m.width <= 0 || lipgloss.Width(out) <= m.frameWidth() {
			return out
		}
	}
	return strings.Join(cols, "\n\n")
}

func helpGrid(cols []string, perRow int) string {
	var rows []string
	for start := 0; start < len(cols); start += perRow {
		var parts []string
		for i, c := range cols[start:min(start+perRow, len(cols))] {
			if i > 0 {
				parts = append(parts, "    ")
			}
			parts = append(parts, c)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	}
	return strings.Join(rows, "\n\n")
}

// nextFromPane names where tab goes from the pane.
func (m model) nextFromPane() string {
	if m.filesVisible() && m.currentWorkspace() != "" {
		return m.sideTabLabel()
	}
	return "tree"
}

func (m model) sideTabLabel() string { return strings.ToLower(sideTabNames[m.projects.sideTab]) }

func (m model) sideKey(b key.Binding) key.Binding {
	return helpAs(b, b.Help().Key, m.sideTabLabel())
}

// sidebarBindings are the focused right sidebar's keys for its current tab and
// row; escDesc names where esc goes from the top level.
func (m model) sidebarBindings(escDesc string) []key.Binding {
	k := projectsKeys
	esc := helpAs(k.Back, "esc", escDesc)
	if m.projects.sideTab == sideFiles {
		return []key.Binding{k.Up, k.SideTabNext, helpAs(k.Left, "h/l", "fold"), helpAs(k.Enter, "enter", "open"), esc}
	}
	c := m.projects.changes
	switch {
	case c.commit != nil:
		return []key.Binding{k.Up, helpAs(k.Enter, "enter", "diff"), helpAs(k.Back, "esc", "back")}
	case c.cursor >= len(c.files) && c.cursor < len(c.files)+len(c.commits):
		return []key.Binding{k.Up, k.SideTabNext, helpAs(k.Enter, "enter", "files"), m.diffModeKey(), esc}
	}
	return []key.Binding{k.Up, k.SideTabNext, helpAs(k.Enter, "enter", "diff"), m.diffModeKey(), esc}
}
