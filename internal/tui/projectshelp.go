package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// treeScreenFooter is the footer of a component on the tree screen: bs, unless
// a pending key sequence, the flash, or the open help takes their place.
func (m model) treeScreenFooter(bs ...binding) string {
	switch {
	case len(m.keyBuf) > 0:
		return asstStyle.Render(m.keyHint())
	case m.flash != "":
		return asstStyle.Render(firstLine(m.flash))
	case m.showHelp:
		return m.footer(hint("any key", "close"))
	}
	return m.footer(bs...)
}

// sessionHint is a footer of bs, unless a pending key sequence or the flash
// takes their place.
func (m model) sessionHint(bs ...binding) string {
	switch {
	case len(m.keyBuf) > 0:
		return asstStyle.Render(m.keyHint())
	case m.flash != "":
		return asstStyle.Render(firstLine(m.flash))
	}
	return m.footer(bs...)
}

// commitBackKeys names the keys that leave an open commit: back, and collapse
// while it still has a key in the Changes tab.
func (m model) commitBackKeys() string {
	keys := m.helpKeys("changes", projectsKeys.Back)
	if ids := m.keymap().screenKeys("changes").keys(projectsKeys.Left); len(ids) > 0 {
		keys += " " + keyLabel(ids[0])
	}
	return keys
}

// projectsHelpView lists every tree screen key, grouped by where it acts; each
// key shows as its section maps it.
func (m model) projectsHelpView() string {
	k := projectsKeys
	type helpRow struct {
		key  string
		desc string
		name string
	}
	row := func(section string, b binding, desc string) helpRow {
		return helpRow{key: m.helpKeys(section, b), desc: desc, name: b.name}
	}
	tree := func(b binding, desc string) helpRow { return row("project-tree", b, desc) }
	pane := func(b binding, desc string) helpRow { return row("workspace", b, desc) }
	lrKey := m.helpKeys("workspace", paneKeys.Left) + "/" + m.helpKeys("workspace", paneKeys.Right)
	groups := []struct {
		title string
		rows  []helpRow
	}{
		{"Tree", []helpRow{
			tree(k.Up, "move"),
			tree(k.Bottom, "top / bottom"),
			tree(k.HalfDown, "page"),
			tree(k.Left, "fold / unfold"),
			tree(k.Enter, "open / fold"),
			tree(paneKeys.Next, "cycle focus"),
			tree(k.Filter, "filter"),
		}},
		{"Pane", []helpRow{
			pane(k.Up, "move / scroll"),
			pane(k.Enter, "open session"),
			pane(listKeys.Jump, "jump to its tmux pane"),
			pane(k.Back, "close / tree"),
			pane(listKeys.Kill, "kill session"),
			pane(k.SetupLog, "workspace setup log"),
			{key: lrKey, desc: "focus left / right pane", name: paneKeys.Left.name},
			row("transcript", paneKeys.Down, "session: focus the prompt"),
			row("file-tree", k.SideTabNext, "right sidebar: Files / Changes"),
			row("changes", k.DiffMode, "changes: uncommitted / vs target"),
			{key: m.commitBackKeys(), desc: "changes: back from a commit", name: k.Back.name},
			row("file", fileViewKeys.NextFile, "diff: next / previous file"),
			row("file", fileViewKeys.Wrap, "open file: wrap long lines"),
		}},
		{"Manage (tree)", []helpRow{
			tree(k.New, "new workspace (branch/PR/issue)"),
			tree(k.Remove, "remove workspace"),
			tree(k.ForceRemove, "force remove"),
			tree(k.Rename, "rename project"),
			tree(k.Forget, "forget project (files stay)"),
			tree(k.Pin, "pin / unpin project"),
			tree(k.Hide, "hide / unhide project"),
			tree(k.ShowHidden, "show hidden"),
			tree(k.ShowGone, "show gone"),
			tree(k.Target, "change target branch"),
			tree(k.RunSetup, "run setup again"),
		}},
		{"Screen", []helpRow{
			tree(k.Spawn, "spawn in the selected workspace"),
			tree(k.Widen, "resize focused sidebar"),
			tree(k.ToggleFiles, "toggle right sidebar"),
			tree(k.ToggleSidebar, "toggle sidebar"),
			tree(k.Refresh, "refresh"),
			tree(k.Help, "help"),
			tree(listKeys.Quit, "quit"),
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

func (m model) sideTabLabel() string { return m.right.tabLabel() }

func (m model) sideKey(b binding) binding {
	return helpAs(b, m.sideTabLabel())
}
