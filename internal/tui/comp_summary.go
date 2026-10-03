package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type summaryComp struct {
	kind projRowKind
	id   string
}

func (s summaryComp) section() string {
	if s.kind == rowNode {
		return "node"
	}
	return "project"
}

func (s summaryComp) raw(*ctx) bool                             { return false }
func (s summaryComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return s, nil }
func (s summaryComp) fullScreen(*ctx) fullLevel                 { return notFull }
func (s summaryComp) close(*ctx) tea.Cmd                        { return nil }
func (s summaryComp) offers(*ctx) []binding                     { return sectionOffers[s.section()].keys }
func (s summaryComp) layer() layer                              { return baseLayer }
func (s summaryComp) pageStep(c *ctx) int                       { return cardPageStep(c.m.paneRows()) }
func (s summaryComp) spins(c *ctx) bool                         { return c.m.anyWorking() }

func (s summaryComp) commands(*ctx) []binding {
	if s.kind == rowNode {
		return append(slices.Clone(summaryKeys), nodeKeys.Wakelock)
	}
	return summaryKeys
}

func (s summaryComp) row(c *ctx) (projectsRow, bool) {
	for _, p := range c.m.left.tree.data {
		switch {
		case s.kind == rowProject && p.ID == s.id:
			return projectsRow{kind: rowProject, id: p.ID, label: p.Name}, true
		case s.kind == rowNode && p.NodeID == s.id:
			return projectsRow{kind: rowNode, id: p.NodeID, label: projNodeLabel(p)}, true
		}
	}
	return projectsRow{}, false
}

func (s summaryComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	c.setFlash("")
	if c.m.matches(msg, projectsKeys.Filter) {
		if c.m.sidebarVisible() {
			c.onTree(treeFilter)
		} else {
			c.setFlash("the filter needs the tree · " + c.m.keyText(projectsKeys.ToggleSidebar) + " shows the tree")
		}
		return s, nil, true
	}
	if paneTreeKey(c, msg) {
		return s, nil, true
	}
	if s.kind == rowNode && c.m.nodeHasTerminals(s.id) &&
		(c.m.matches(msg, listKeys.TabNext) || c.m.matches(msg, listKeys.TabPrev)) {
		return s, openNodeTerminals(c, s.id), true
	}
	if s.kind == rowNode && c.m.matches(msg, nodeKeys.Wakelock) {
		label := s.id
		if r, ok := s.row(c); ok {
			label = r.label
		}
		return s, openWakelockPicker(c, s.id, label), true
	}
	if !c.m.matches(msg, projectsKeys.Back) {
		return s, nil, false
	}
	if c.m.sidebarVisible() {
		c.focusTree()
	} else {
		c.home()
	}
	return s, nil, true
}

func (s summaryComp) view(c *ctx, w, _ int) string {
	cardW := min(w, maxCardWidth)
	body := dimStyle.Render("no projects")
	if r, ok := s.row(c); ok {
		body = rowSummary(c, r, cardW)
	}
	if s.kind == rowNode && c.m.nodeHasTerminals(s.id) {
		c.m.hitNodeTabs(c, centerGutter(cardW, w))
		body = c.m.nodeTabs(nodeTabProjects) + "\n\n" + body
	}
	return centerBlock(body, cardW, w)
}

func (s summaryComp) click(c *ctx, t hitTarget, _ bool) (component, tea.Cmd) {
	if s.kind == rowNode && t.kind == hitTab && nodeTab(t.index) == nodeTabTerminals {
		return s, openNodeTerminals(c, s.id)
	}
	return s, nil
}

func (s summaryComp) footer(c *ctx) []binding {
	if s.kind == rowNode && c.m.nodeHasWakelock(s.id) {
		return []binding{nodeKeys.Wakelock, projectsKeys.Help}
	}
	return []binding{projectsKeys.Help}
}

func rowSummary(c *ctx, r projectsRow, w int) string {
	m := c.m
	act := m.workspaceActivity()
	var b strings.Builder
	if r.kind == rowNode {
		b.WriteString(m.paneHeadStyle().Render(r.label) + "\n\n")
		if e, ok := m.hosts[r.id]; ok {
			b.WriteString(hostLines(e, m.nodeHasWakelock(r.id), time.Now(), w))
		}
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
	b.WriteString(truncateLine(m.paneHeadStyle().Render(pr.Name)+projectLabel("", pr.Hidden, pr.Pinned)+dimStyle.Render("  "+dir), w) + "\n\n")
	if pr.Error != "" {
		b.WriteString(truncateLine(StyleErrorBold.Render("git error: "+pr.Error), w) + "\n\n")
	}
	for _, ws := range visibleWorkspaces(pr.Workspaces, m.left.tree.showGone) {
		row := projectsRow{
			kind: rowWorkspace, id: ws.ID, label: filepath.Base(ws.Dir), branch: ws.Branch, plain: pr.Kind == "plain",
			target: ws.TargetBranch, ws: []string{ws.ID}, isGone: ws.IsGone, isMain: ws.IsMain,
		}
		b.WriteString(m.projRowLine(row, false, false, act, w) + "\n")
	}
	hint := m.keyText(projectsKeys.Back) + " tree: " + m.keyText(projectsKeys.New) + " new workspace · " + m.keyText(projectsKeys.Rename) + " rename"
	b.WriteString("\n" + dimStyle.Render(truncateLine(hint, w)))
	return b.String()
}
