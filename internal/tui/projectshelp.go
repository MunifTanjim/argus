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
		return asstStyle.Render("rename: " + m.projects.input.View() + "  enter · esc")
	case m.projects.inputMode == pmFilter:
		return asstStyle.Render("filter: " + m.projects.input.View() + "  enter keep · esc clear")
	case m.projects.pendingKill != "":
		return asstStyle.Render(killVerb(m.sessions[m.projects.pendingKill]) + " this session? y/n")
	case m.projects.pendingRemove != "":
		verb := "remove"
		if m.projects.pendingRemoveForce {
			verb = "force-remove"
		}
		return asstStyle.Render(verb + " this workspace? y/n")
	case m.projects.offerSpawn != nil:
		return asstStyle.Render("start an agent with this issue? y/n")
	case m.flash != "":
		return asstStyle.Render(m.flash)
	case m.projects.create.active:
		return m.createFooter()
	case m.projects.retarget != nil:
		return m.footer(helpAs(k.Up, "↑/↓", "move"), helpAs(k.Enter, "enter", "select"), helpAs(k.Back, "esc", "cancel"))
	case m.projects.showHelp:
		return m.footer(helpAs(k.Back, "any key", "close"))
	case m.projects.focus == focusTree && m.sidebarVisible():
		bindings := []key.Binding{k.Up, k.Left, k.Enter, k.Focus, k.Spawn, k.New, k.Remove, k.Filter}
		if m.projects.filter != "" {
			bindings = append(bindings, helpAs(k.Back, "esc", "clear filter"))
		}
		return m.footer(append(bindings, helpAs(k.Back, "q", "quit"), k.Help)...)
	case m.paneViewing():
		return m.footer(helpAs(k.Up, "↑/↓", "scroll"), helpAs(k.HalfDown, "^d/^u", "page"), helpAs(k.Back, "esc", "close"), k.Help)
	}
	open, back := k.Enter, k.Back
	switch m.projects.tab {
	case tabChanges:
		open = helpAs(k.Enter, "enter", "diff")
	case tabFiles:
		if m.projects.files.dir != "" {
			back = helpAs(k.Back, "esc", "up")
		}
	}
	bindings := []key.Binding{k.Up, listKeys.TabNext, open, k.Spawn}
	if m.projects.tab == tabSessions {
		bindings = append(bindings, listKeys.Kill)
	}
	if m.projects.tab == tabChanges {
		bindings = append(bindings, k.DiffMode)
	}
	if m.sidebarVisible() {
		bindings = append(bindings, helpAs(k.Focus, "tab", "tree"))
	}
	return m.footer(append(bindings, k.Help, back)...)
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
			helpAs(k.Focus, "tab", "switch pane"),
			helpAs(k.Filter, "/", "filter"),
		}},
		{"Pane", []key.Binding{
			helpAs(listKeys.TabNext, "h/l", "prev / next tab"),
			helpAs(k.Up, "↑/↓ j/k", "move / scroll"),
			helpAs(k.Enter, "enter", "open"),
			helpAs(k.Back, "esc", "close / up / tree"),
			helpAs(k.DiffMode, "t", "changes: uncommitted / vs target"),
			helpAs(listKeys.Kill, "x", "sessions: kill session"),
		}},
		{"Manage", []key.Binding{
			helpAs(k.Spawn, "s", "start a session in the workspace"),
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
			helpAs(k.Widen, "< >", "resize sidebar"),
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
	var parts []string
	for i, c := range cols {
		if i > 0 {
			parts = append(parts, "    ")
		}
		parts = append(parts, c)
	}
	if out := lipgloss.JoinHorizontal(lipgloss.Top, parts...); m.width <= 0 || lipgloss.Width(out) <= m.frameWidth() {
		return out
	}
	return strings.Join(cols, "\n\n")
}
