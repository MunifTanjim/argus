package tui

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// workspaceComp is the workspace pane: the sessions of the tree's workspace
// row, or an overview of its node or project row. ws is the workspace the
// cursor was last reset for; killID is the session awaiting a kill
// confirmation.
type workspaceComp struct {
	ws     string
	cursor int
	killID string
}

func (p workspaceComp) section() string                           { return "workspace" }
func (p workspaceComp) raw(*ctx) bool                             { return p.killID != "" }
func (p workspaceComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return p, nil }
func (p workspaceComp) fullScreen(*ctx) fullLevel                 { return notFull }
func (p workspaceComp) close(*ctx) tea.Cmd                        { return nil }
func (p workspaceComp) offers(*ctx) []binding                     { return sectionOffers[p.section()].keys }
func (p workspaceComp) layer() layer                              { return baseLayer }
func (p workspaceComp) pageStep(c *ctx) int                       { return cardPageStep(c.m.paneRows()) }
func (p workspaceComp) workspace(c *ctx) string                   { return c.m.selectedWorkspaceID() }
func (p workspaceComp) spins(c *ctx) bool                         { return c.m.anyWorking() }

func (p workspaceComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m := c.m
	if id := p.killID; id != "" {
		p.killID = ""
		if msg.String() == "y" {
			return p, m.killCmd(id), true
		}
		return p, nil, true
	}
	c.setFlash("")
	if cmd, ok := p.treeKey(c, msg); ok {
		return p, cmd, true
	}
	// A file over the pane passes it only the keys that act on the tree.
	if _, onTop := m.main.top().(workspaceComp); !onTop {
		return p, nil, false
	}
	ss := m.paneSessions()
	var cmd tea.Cmd
	switch {
	case m.matches(msg, projectsKeys.Up):
		p.cursor = cursorUp(p.cursor)
	case m.matches(msg, projectsKeys.Down):
		p.cursor = cursorDown(p.cursor, len(ss))
	case m.matches(msg, projectsKeys.Top):
		p.cursor = 0
	case m.matches(msg, projectsKeys.Bottom):
		p.cursor = cursorBottom(len(ss))
	case m.matches(msg, projectsKeys.HalfUp):
		p.cursor = max(0, p.cursor-m.cardListPageStep())
	case m.matches(msg, projectsKeys.HalfDown):
		p.cursor = min(cursorBottom(len(ss)), p.cursor+m.cardListPageStep())
	case m.matches(msg, listKeys.Jump):
		if p.cursor < len(ss) {
			cmd = jump(c, ss[p.cursor])
		}
	case m.matches(msg, projectsKeys.Enter):
		if p.cursor < len(ss) {
			c.openSession(ss[p.cursor].ID)
		}
	case m.matches(msg, projectsKeys.Back):
		if m.sidebarVisible() {
			c.focusOn(leftSidebar)
		} else { // no tree to return to: go to the Home pane
			c.home()
		}
	case m.matches(msg, listKeys.Kill):
		if p.cursor < len(ss) {
			s := ss[p.cursor]
			if refusal := killRefusal(s); refusal != "" {
				c.setFlash(refusal)
			} else {
				p.killID = s.ID
			}
		}
	default:
		return p, nil, false
	}
	return p, cmd, true
}

// treeKey runs the keys that act on the tree from the pane; the tree's manage
// keys only say where they work.
func (p workspaceComp) treeKey(c *ctx, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	m, k := c.m, projectsKeys
	var cmd tea.Cmd
	switch {
	case m.matches(msg, k.Filter):
		if m.sidebarVisible() {
			c.onTree(treeFilter)
		} else {
			c.setFlash("the filter needs the tree · " + m.keyText(k.ToggleSidebar) + " shows the tree")
		}
	case m.matches(msg, k.Spawn):
		cmd = spawnSession(c)
	case m.matches(msg, k.SetupLog):
		cmd = openSetupLog(c)
	case m.matches(msg, k.ShowHidden):
		c.onTree(treeToggleHidden)
	case m.matches(msg, k.ShowGone):
		c.onTree(treeToggleGone)
	case m.matches(msg, k.Refresh):
		c.onTree(treeReload)
	case m.matches(msg, k.Widen):
		c.resizeTree(4)
	case m.matches(msg, k.Narrow):
		c.resizeTree(-4)
	case m.matches(msg, k.New, k.Rename, k.Hide, k.Pin, k.Target, k.ForceRemove, k.Forget, k.RunSetup):
		c.setFlash("manage keys work in the tree · " + m.keyText(k.Back) + " to go there")
	default:
		return nil, false
	}
	return cmd, true
}

func (p workspaceComp) view(c *ctx, w, h int) string {
	cardW := min(w, maxCardWidth)
	return centerBlock(p.column(c, cardW, max(1, h-footerRows)), cardW, w)
}

// fileHeader is the workspace row's header, which stays over a file opened on
// the pane.
func (p workspaceComp) fileHeader(c *ctx, w int) string {
	r, ok := c.m.cursorRow()
	if !ok || r.kind != rowWorkspace {
		return ""
	}
	cardW := min(w, maxCardWidth)
	return centerBlock(truncateLine(c.m.wsHeader(r), cardW), cardW, w)
}

func (p workspaceComp) column(c *ctx, w, h int) string {
	m := c.m
	r, ok := m.cursorRow()
	if !ok {
		return dimStyle.Render("no projects")
	}
	if r.kind != rowWorkspace {
		return p.summary(c, r, w)
	}
	return truncateLine(m.wsHeader(r), w) + "\n\n" + p.sessions(c, w, max(1, h-2))
}

// summary is the overview of a node or project row: what the row holds, with
// the live session count per workspace.
func (p workspaceComp) summary(c *ctx, r projectsRow, w int) string {
	m := c.m
	act := m.workspaceActivity()
	var b strings.Builder
	if r.kind == rowNode {
		b.WriteString(StylePrimaryBold.Render(r.label) + "\n\n")
		for _, pr := range m.left.tree.data {
			if pr.NodeID != r.id || (pr.Hidden && !m.left.tree.showHidden) || (pr.IsGone && !m.left.tree.showGone) {
				continue
			}
			pr.Workspaces = visibleWorkspaces(pr.Workspaces, m.left.tree.showGone)
			line := "  " + projectLabel(pr.Name, pr.Hidden, pr.Pinned) + dimStyle.Render("  "+plural(len(pr.Workspaces), "workspace"))
			b.WriteString(withBadge(line, m.activityBadge(act, workspaceIDs(pr)), w) + "\n")
		}
		return b.String()
	}
	pr, _ := m.findProject(r.id)
	dir := pr.Root
	if dir == "" {
		dir = pr.Dir
	}
	b.WriteString(truncateLine(StylePrimaryBold.Render(pr.Name)+projectLabel("", pr.Hidden, pr.Pinned)+dimStyle.Render("  "+dir), w) + "\n\n")
	if pr.Error != "" {
		b.WriteString(truncateLine(StyleErrorBold.Render("git error: "+pr.Error), w) + "\n\n")
	}
	for _, ws := range visibleWorkspaces(pr.Workspaces, m.left.tree.showGone) {
		row := projectsRow{
			kind: rowWorkspace, id: ws.ID, label: filepath.Base(ws.Dir), branch: ws.Branch, target: ws.TargetBranch,
			ws: []string{ws.ID}, isGone: ws.IsGone, isMain: ws.IsMain,
		}
		b.WriteString(m.projRowLine(row, false, false, act, w) + "\n")
	}
	hint := m.keyText(projectsKeys.New) + " new workspace · " + m.keyText(projectsKeys.Rename) + " rename"
	if m.left.tree.isFolded(pr.ID) {
		hint = m.keyText(projectsKeys.Right) + " unfold · " + hint
	}
	b.WriteString("\n" + dimStyle.Render(truncateLine(hint, w)))
	return b.String()
}

func (p workspaceComp) sessions(c *ctx, w, avail int) string {
	m := c.m
	block := m.setupBlock(min(w, maxCardWidth))
	avail = max(1, avail-lipgloss.Height(block))
	ss := m.paneSessions()
	if len(ss) == 0 {
		return block + dimStyle.Render("no sessions in this workspace")
	}
	focused := m.focused == mainPane
	cardW := min(w, maxCardWidth)
	cards := make([]string, len(ss))
	for i, s := range ss {
		cards[i] = m.sessionCard(s, focused && i == p.cursor, cardW, true)
	}
	cursor := 0
	if focused {
		cursor = p.cursor
	}
	return block + renderCardList(cards, cursor, avail)
}

func (p workspaceComp) footerText(c *ctx) string {
	if p.killID != "" {
		return asstStyle.Render(killPrompt(c.m.sessions[p.killID]))
	}
	return c.m.treeScreenFooter(p.footer(c)...)
}

func (p workspaceComp) footer(c *ctx) []binding {
	k := projectsKeys
	bindings := []binding{k.Up, k.Enter, listKeys.Jump, k.Spawn, listKeys.Kill}
	if c.m.sidebarVisible() || c.m.nextFromPane() == "files" {
		bindings = append(bindings, helpAs(paneKeys.Next, c.m.nextFromPane()))
	}
	return append(bindings, k.Help, helpAs(k.Back, "tree"))
}

// syncPane keeps the root on the tree's cursor row: the Home pane on the Home
// row, else the workspace pane, whose cursor starts over for another
// workspace. The pane that does not show is kept for the way back.
func (m *model) syncPane() tea.Cmd {
	m.keepPanes()
	if ws := m.selectedWorkspaceID(); ws != "" && m.keptPane.ws != ws {
		m.keptPane = workspaceComp{ws: ws}
	}
	if root := m.rootComp(); isPane(root) {
		// A kill prompt must not come back unseen with the pane that leaves.
		if _, onHome := root.(homeComp); onHome != m.onHomeRow() {
			m.disarmKept()
		}
		m.main = m.main.replaceAt(0, m.rowPane())
	}
	return nil
}

func (m model) rowPane() component {
	if m.onHomeRow() {
		return m.keptHome
	}
	return m.keptPane
}

func isPane(comp component) bool {
	switch comp.(type) {
	case homeComp, workspaceComp:
		return true
	}
	return false
}

// keepPanes saves the Home or workspace pane at the root, so that it comes back
// with its cursor once another component replaces it.
func (m *model) keepPanes() {
	switch p := m.rootComp().(type) {
	case homeComp:
		m.keptHome = p
	case workspaceComp:
		m.keptPane = p
	}
}

// disarmRoot drops a kill confirmation on the root pane before a component
// covers it, so an unseen prompt cannot take a later key.
func (m *model) disarmRoot() {
	m.keepPanes()
	m.disarmKept()
	m.showKept()
}

func (m *model) disarmKept() {
	m.keptHome.pendingKill = false
	m.keptPane.killID = ""
}

// showKept puts the kept pane of the root's kind back at the root.
func (m *model) showKept() {
	switch m.rootComp().(type) {
	case homeComp:
		m.main = m.main.replaceAt(0, m.keptHome)
	case workspaceComp:
		m.main = m.main.replaceAt(0, m.keptPane)
	}
}

func (m model) rootComp() component {
	if len(m.main) == 0 {
		return nil
	}
	return m.main[0]
}
