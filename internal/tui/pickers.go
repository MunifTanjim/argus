package tui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// pickerMaxHeight caps a picker's height, as crush's defaultDialogHeight does.
const pickerMaxHeight = 20

func pickerFrame(area uv.Rectangle) popupFrame {
	return popupFrame{width: min(area.Dx(), maxCardWidth), height: min(area.Dy(), pickerMaxHeight)}
}

func (p createPicker) id() string         { return "create" }
func (p createPicker) keySection() string { return "project-tree" }
func (p createPicker) spins(*ctx) bool    { return p.creating || p.listLoading() }

func (p createPicker) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	f := pickerFrame(area)
	f.title = "New workspace in " + p.project
	f.titleInfo = p.titleInfo(c)
	f.help = c.m.hints(f.innerWidth(), p.footer(c)...)
	f.parts = []string{p.body(c, f.innerWidth(), f.bodyHeight())}
	drawCenter(scr, area, f.render())
}

type retargetPicker struct {
	workspaceID string
	projectID   string
	label       string // the workspace's name, for the picker's title
	pick        branchPicker
}

func (r retargetPicker) id() string         { return "retarget" }
func (r retargetPicker) keySection() string { return "" }
func (r retargetPicker) spins(*ctx) bool    { return false }

func (r retargetPicker) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	if msg.String() == "esc" {
		c.closePopup()
		return r, nil
	}
	picked, cmd := r.pick.key(msg)
	if picked == nil {
		return r, cmd
	}
	c.closePopup()
	return r, c.m.setTargetCmd(r.workspaceID, picked.Name)
}

func (r retargetPicker) update(_ *ctx, msg tea.Msg) (popup, tea.Cmd) {
	switch msg := msg.(type) {
	case branchesMsg:
		if msg.projectID == r.projectID {
			r.pick.load(msg.branches, msg.err)
		}
	case tea.PasteMsg:
		return r, r.pick.paste(msg)
	}
	return r, nil
}

func (r retargetPicker) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	f := pickerFrame(area)
	f.title = "Target for " + r.label
	if r.pick.current != "" {
		f.titleInfo = dimStyle.Render("· now " + r.pick.current)
	}
	f.help = c.m.hints(f.innerWidth(), r.footer()...)
	f.parts = []string{r.pick.view(f.innerWidth(), f.bodyHeight(), false)}
	drawCenter(scr, area, f.render())
}

func (r retargetPicker) footer() []binding {
	return []binding{hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "cancel")}
}

func (m model) frontCreate() (createPicker, bool) {
	p, ok := m.popups.front().(createPicker)
	return p, ok
}

// createDone reports a create call's result: the tree selects the new
// workspace, and the picker that made the call closes or shows the error.
func (m model) createDone(msg createDoneMsg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	comp, treeCmd := m.left.tree.update(c, msg)
	m.left.tree = comp.(projectTreeComp)
	pickCmd := m.updatePopupIn(c, msg)
	cmd := tea.Batch(treeCmd, pickCmd, m.apply(c))
	return m, cmd
}
