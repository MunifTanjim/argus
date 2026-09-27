package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// commitBackKeys names the keys that leave an open commit: back, and collapse
// while it still has a key in the Changes tab.
func (m model) commitBackKeys() string {
	keys := m.helpKeys("changes", projectsKeys.Back)
	if ids := m.keymap().screenKeys("changes").keys(projectsKeys.Left); len(ids) > 0 {
		keys += " " + keyLabel(ids[0])
	}
	return keys
}

type helpRow struct {
	key  string
	desc string
	name string
}

func (r helpRow) label() string {
	if r.name == "" {
		return r.desc
	}
	return r.desc + " · " + r.name
}

type helpGroup struct {
	title string
	rows  []helpRow
}

// helpGroups are every tree screen key, grouped by where it acts; each key
// shows as its section maps it.
func (m model) helpGroups() []helpGroup {
	k := projectsKeys
	row := func(section string, b binding, desc string) helpRow {
		return helpRow{key: m.helpKeys(section, b), desc: desc, name: b.name}
	}
	tree := func(b binding, desc string) helpRow { return row("project-tree", b, desc) }
	pane := func(b binding, desc string) helpRow { return row("workspace", b, desc) }
	lrKey := m.helpKeys("workspace", paneKeys.Left) + "/" + m.helpKeys("workspace", paneKeys.Right)
	return []helpGroup{
		{"Tree", []helpRow{
			tree(k.Up, "move"),
			tree(k.Bottom, "top / bottom"),
			tree(k.HalfDown, "page"),
			tree(k.Left, "fold / unfold"),
			{key: row("project-tree", k.Enter, "").key, desc: "open; " + m.keyTextOn("project-tree", k.Right) + " opens Home/workspace", name: ""},
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
			tree(m.applying(k.Pin, k.Unpin), "pin / unpin project"),
			tree(m.applying(k.Hide, k.Unhide), "hide / unhide project"),
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
}

// projectsHelpView lays out the help groups: all side by side, else two per
// row, else stacked, whichever first fits the frame. When none fits, it is the
// first that fits the width, and the help screen scrolls it.
func (m model) projectsHelpView() string {
	groups := m.helpGroups()
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
			lines = append(lines, StyleSecondary.Render(r.key+strings.Repeat(" ", kw-lipgloss.Width(r.key)))+"  "+StyleDim.Render(r.label()))
		}
		cols[i] = strings.Join(lines, "\n")
	}
	scrolled := ""
	for _, perRow := range []int{len(cols), 2, 1} {
		out := helpGrid(cols, perRow)
		if m.width > 0 && lipgloss.Width(out) > m.frameWidth() {
			continue
		}
		if m.height <= 0 || lipgloss.Height(out) <= m.helpRows() {
			return out
		}
		if scrolled == "" {
			scrolled = out
		}
	}
	if scrolled == "" {
		return helpGrid(cols, 1)
	}
	return scrolled
}

// helpRows is the height the help screen shows the help in: the frame less its
// title, the blank under it, and the footer.
func (m model) helpRows() int { return max(1, m.height-2-footerRows) }

func (m model) helpMaxScroll() int {
	return max(0, lipgloss.Height(m.projectsHelpView())-m.helpRows())
}

// helpKey reports false for a key that does not scroll the help, which closes
// it.
func (m model) helpKey(msg tea.KeyPressMsg) (model, bool) {
	most := m.helpMaxScroll()
	if most == 0 {
		return m, false
	}
	top, page := min(m.helpScroll, most), max(1, m.helpRows()/2)
	switch msg.String() {
	case "up", "k":
		top--
	case "down", "j":
		top++
	case "pgup", "ctrl+u":
		top -= page
	case "pgdown", "ctrl+d":
		top += page
	default:
		return m, false
	}
	m.helpScroll = max(0, min(top, most))
	return m, true
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
