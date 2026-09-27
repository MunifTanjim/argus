package tui

import tea "charm.land/bubbletea/v2"

// The create and retarget pickers open over the main pane from the tree. The
// main pane has focus while one is open; closing it returns focus to the tree.
// The create picker's key is mapped in the tree's section; the retarget picker
// reads no bindings.

func (p createComp) section() string           { return "project-tree" }
func (p createComp) raw(*ctx) bool             { return true }
func (p createComp) fullScreen(*ctx) fullLevel { return notFull }
func (p createComp) close(*ctx) tea.Cmd        { return nil }
func (p createComp) offers(*ctx) []binding     { return nil }
func (p createComp) pageStep(c *ctx) int       { return c.m.listPageStep() }
func (p createComp) layer() layer              { return overLayer }

func (p createComp) spins(*ctx) bool { return p.creating || p.listLoading() }

// retargetComp is a branch picker that sets a workspace's target branch.
type retargetComp struct {
	workspaceID string
	projectID   string
	label       string // the workspace's name, for the picker's header
	pick        branchPicker
}

func (r retargetComp) section() string           { return "" }
func (r retargetComp) raw(*ctx) bool             { return true }
func (r retargetComp) spins(*ctx) bool           { return false }
func (r retargetComp) fullScreen(*ctx) fullLevel { return notFull }
func (r retargetComp) close(*ctx) tea.Cmd        { return nil }
func (r retargetComp) offers(*ctx) []binding     { return nil }
func (r retargetComp) pageStep(c *ctx) int       { return c.m.listPageStep() }
func (r retargetComp) layer() layer              { return overLayer }

func (r retargetComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	if msg.String() == "esc" {
		leavePicker(c)
		return r, nil, true
	}
	picked, cmd := r.pick.key(msg)
	if picked == nil {
		return r, cmd, true
	}
	leavePicker(c)
	return r, c.m.setTargetCmd(r.workspaceID, picked.Name), true
}

func (r retargetComp) update(_ *ctx, msg tea.Msg) (component, tea.Cmd) {
	if msg, ok := msg.(branchesMsg); ok && msg.projectID == r.projectID {
		r.pick.load(msg.branches, msg.err)
	}
	return r, nil
}

func (r retargetComp) view(_ *ctx, w, h int) string {
	return pickerView(w, h, func(w, h int) string {
		head := StylePrimaryBold.Render("Target for " + r.label)
		if r.pick.current != "" {
			head += dimStyle.Render(" · now " + r.pick.current)
		}
		return head + "\n\n" + r.pick.view(w, max(1, h-2), false)
	})
}

// pickerView draws a picker's column, which draw lays out w wide and h high,
// centered in a main pane w by h.
func pickerView(w, h int, draw func(w, h int) string) string {
	cardW := min(w, maxCardWidth)
	return centerBlock(draw(cardW, max(1, h-footerRows)), cardW, w)
}

func (p createComp) footerText(c *ctx) string { return c.m.treeScreenFooter(p.footer(c)...) }

func (r retargetComp) footerText(c *ctx) string { return c.m.treeScreenFooter(r.footer(c)...) }

func (r retargetComp) footer(*ctx) []binding {
	return []binding{hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "cancel")}
}

func leavePicker(c *ctx) {
	c.back()
	c.focusOn(leftSidebar)
}

func isPicker(comp component) bool {
	switch comp.(type) {
	case createComp, retargetComp:
		return true
	}
	return false
}

func (m model) picker() (component, bool) {
	if top := m.main.top(); top != nil && isPicker(top) {
		return top, true
	}
	return nil, false
}

func (m model) createPicker() (createComp, bool) {
	p, ok := m.main.top().(createComp)
	return p, ok
}

// updatePicker gives a reply to the open picker; with none open, it is dropped.
func (m model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	cmd := m.updatePickerIn(c, msg)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (m *model) updatePickerIn(c *ctx, msg tea.Msg) tea.Cmd {
	p, ok := m.picker()
	if !ok {
		return nil
	}
	comp, cmd := p.update(c, msg)
	m.main = m.main.replaceTop(comp)
	return cmd
}

// createDone reports a create call's result: the tree selects the new
// workspace, and the picker that made the call closes or shows the error.
func (m model) createDone(msg createDoneMsg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	comp, treeCmd := m.left.tree.update(c, msg)
	m.left.tree = comp.(projectTreeComp)
	pickCmd := m.updatePickerIn(c, msg)
	cmd := tea.Batch(treeCmd, pickCmd, m.apply(c))
	return m, cmd
}
