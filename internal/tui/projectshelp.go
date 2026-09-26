package tui

import (
	"strings"

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
	case m.projects.pendingForget != "":
		p, _ := m.findProject(m.projects.pendingForget)
		return asstStyle.Render("forget project " + p.Name + "? it leaves the list; its files stay · y/n")
	case m.projects.offerSpawn != nil:
		return asstStyle.Render("start an agent with this issue? y/n")
	case len(m.keyBuf) > 0:
		return asstStyle.Render(m.keyHint())
	case m.flash != "":
		return asstStyle.Render(firstLine(m.flash))
	case m.projects.create.active:
		return m.createFooter()
	case m.projects.retarget != nil:
		return m.footer(hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "cancel"))
	case m.projects.showHelp:
		return m.footer(hint("any key", "close"))
	case m.projects.focus == focusFiles && m.filesVisible():
		return m.footer(append(m.sidebarBindings("pane"), m.sideKey(k.ToggleFiles), k.Help)...)
	case m.projects.focus == focusTree && m.sidebarVisible():
		bindings := m.treeRowBindings()
		if m.projects.filter != "" {
			bindings = append(bindings, helpAs(k.Back, "clear filter"))
		}
		return m.footer(append(bindings, listKeys.Quit, m.sideKey(k.ToggleFiles), k.Help)...)
	case m.projects.fileView.open() && m.projects.focus == focusPane:
		return m.footer(append(m.fileViewBindings(), k.Help)...)
	}
	bindings := []binding{k.Up, k.Enter, listKeys.Jump, k.Spawn, listKeys.Kill}
	if m.sidebarVisible() || m.nextFromPane() == "files" {
		bindings = append(bindings, helpAs(paneKeys.Next, m.nextFromPane()))
	}
	return m.footer(append(bindings, k.Help, helpAs(k.Back, "tree"))...)
}

// fileViewBindings are the keys of an open file or diff; ]f/[f step only a diff.
func (m model) fileViewBindings() []binding {
	fk := fileViewKeys
	b := []binding{helpAs(fk.Up, "scroll"), helpAs(fk.HalfDown, "page"), helpAs(fk.Bottom, "ends")}
	if m.projects.fileView.diff {
		b = append(b, fk.NextFile)
	}
	return append(b, fk.Wrap, helpAs(fk.Refresh, "reload"), helpAs(fk.Back, "close"))
}

// treeRowBindings are the tree keys that act on the selected row.
func (m model) treeRowBindings() []binding {
	k := projectsKeys
	r, _ := m.cursorRow()
	switch r.kind {
	case rowHome:
		return []binding{k.Up, k.Enter, paneKeys.Next, k.Spawn, k.Filter}
	case rowWorkspace:
		return []binding{k.Up, k.Left, k.Enter, paneKeys.Next, k.Spawn, k.New, k.Remove, k.Filter}
	case rowProject:
		return []binding{k.Up, k.Left, k.Enter, k.Spawn, k.New, k.Filter}
	}
	return []binding{k.Up, k.Left, k.Enter, k.Filter}
}

// projectsHelpView lists every projects-screen key, grouped by where it acts.
// commitBackKeys names the keys that leave an open commit: back, and collapse
// while it still has a key on the projects screen.
func (m model) commitBackKeys() string {
	keys := m.helpKeys(projectsKeys.Back)
	if ids := m.keymap().screenKeys("projects").keys(projectsKeys.Left); len(ids) > 0 {
		keys += " " + keyLabel(ids[0])
	}
	return keys
}

func (m model) projectsHelpView() string {
	k := projectsKeys
	type helpRow struct {
		key  string
		desc string
		name string
	}
	row := func(b binding, desc string) helpRow {
		return helpRow{key: m.helpKeys(b), desc: desc, name: b.name}
	}
	lrKey := m.helpKeys(paneKeys.Left) + "/" + m.helpKeys(paneKeys.Right)
	groups := []struct {
		title string
		rows  []helpRow
	}{
		{"Tree", []helpRow{
			row(k.Up, "move"),
			row(k.Bottom, "top / bottom"),
			row(k.HalfDown, "page"),
			row(k.Left, "fold / unfold"),
			row(k.Enter, "open / fold"),
			row(paneKeys.Next, "cycle focus"),
			row(k.Filter, "filter"),
		}},
		{"Pane", []helpRow{
			row(k.Up, "move / scroll"),
			row(k.Enter, "open session"),
			row(listKeys.Jump, "jump to its tmux pane"),
			row(k.Back, "close / tree"),
			row(listKeys.Kill, "kill session"),
			row(k.SetupLog, "workspace setup log"),
			{key: lrKey, desc: "focus left / right pane", name: paneKeys.Left.name},
			row(paneKeys.Down, "session: focus the prompt"),
			row(k.SideTabNext, "right sidebar: Files / Changes"),
			row(k.DiffMode, "changes: uncommitted / vs target"),
			{key: m.commitBackKeys(), desc: "changes: back from a commit", name: k.Back.name},
			row(fileViewKeys.NextFile, "diff: next / previous file"),
			row(fileViewKeys.Wrap, "open file: wrap long lines"),
		}},
		{"Manage (tree)", []helpRow{
			row(k.New, "new workspace (branch/PR/issue)"),
			row(k.Remove, "remove workspace"),
			row(k.ForceRemove, "force remove"),
			row(k.Rename, "rename project"),
			row(k.Forget, "forget project (files stay)"),
			row(k.Pin, "pin project"),
			row(k.Hide, "hide project"),
			row(k.ShowHidden, "show hidden"),
			row(k.ShowGone, "show gone"),
			row(k.Target, "change target branch"),
			row(k.RunSetup, "run setup again"),
		}},
		{"Screen", []helpRow{
			row(k.Spawn, "spawn in the selected workspace"),
			row(k.Widen, "resize focused sidebar"),
			row(k.ToggleFiles, "toggle right sidebar"),
			row(k.ToggleSidebar, "toggle sidebar"),
			row(k.Refresh, "refresh"),
			row(k.Help, "help"),
			row(listKeys.Quit, "quit"),
		}},
	}
	cols := make([]string, len(groups))
	for i, g := range groups {
		kw := 0
		for _, r := range g.rows {
			kw = max(kw, lipgloss.Width(r.key))
		}
		lines := []string{StyleAccentBold.Render(g.title)}
		for _, r := range g.rows {
			if r.key == "" {
				continue
			}
			desc := r.desc
			if r.name != "" {
				desc += " · " + r.name
			}
			lines = append(lines, StyleSecondary.Render(r.key+strings.Repeat(" ", kw-lipgloss.Width(r.key)))+"  "+StyleDim.Render(desc))
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
				parts = append(parts, "  ")
			}
			parts = append(parts, c)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	}
	return strings.Join(rows, "\n\n")
}

func (m model) nextFromPane() string {
	if m.filesVisible() && m.currentWorkspace() != "" {
		return m.sideTabLabel()
	}
	return "tree"
}

func (m model) sideTabLabel() string { return strings.ToLower(sideTabNames[m.projects.sideTab]) }

func (m model) sideKey(b binding) binding {
	return helpAs(b, m.sideTabLabel())
}

// sidebarBindings are the focused right sidebar's keys for its current tab and
// row; escDesc names where esc goes from the top level.
func (m model) sidebarBindings(escDesc string) []binding {
	k := projectsKeys
	esc := helpAs(k.Back, escDesc)
	if m.projects.sideTab == sideFiles {
		return []binding{k.Up, k.SideTabNext, helpAs(k.Left, "fold"), helpAs(k.Enter, "open"), esc}
	}
	c := m.projects.changes
	switch {
	case c.commit != nil:
		return []binding{k.Up, helpAs(k.Enter, "diff"), helpAs(k.Back, "back")}
	case c.cursor >= len(c.files) && c.cursor < len(c.files)+len(c.commits):
		return []binding{k.Up, k.SideTabNext, helpAs(k.Enter, "files"), m.diffModeKey(), esc}
	}
	return []binding{k.Up, k.SideTabNext, helpAs(k.Enter, "diff"), m.diffModeKey(), esc}
}
