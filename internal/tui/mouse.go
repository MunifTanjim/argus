package tui

import tea "charm.land/bubbletea/v2"

type dragTarget int

const (
	dragNone dragTarget = iota
	dragTree
	dragFiles
)

// focused reports whether the pane had focus before the click.
type clicker interface {
	click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd)
}

type wheeler interface {
	wheel(c *ctx, delta int) (component, tea.Cmd)
}

type popupClicker interface {
	click(c *ctx, t hitTarget) (popup, tea.Cmd)
}

type popupWheeler interface {
	wheel(c *ctx, delta int) (popup, tea.Cmd)
}

var enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}

func (m model) prompting() bool {
	p, ok := m.focusedComp().(footerPrompter)
	return ok && p.footerPrompt(&ctx{m: &m}) != ""
}

func (m model) mouseClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	m.drag = dragNone
	if !m.mouse || ms.Button != tea.MouseLeft {
		return m, nil
	}
	if len(m.keyBuf) > 0 {
		m.clearKeys()
	}
	if m.helpShown() {
		m.showHelp = false
		return m, nil
	}
	a, ok := m.hits.at(ms.X, ms.Y)
	if len(m.popups) > 0 {
		if !ok || a.region != regPopup {
			return m, nil
		}
		t, hit := a.target(ms.X, ms.Y)
		if !hit {
			return m, nil
		}
		return m.popupMouse(func(c *ctx, p popup) (popup, tea.Cmd, bool) {
			pc, ok := p.(popupClicker)
			if !ok {
				return p, nil, false
			}
			p, cmd := pc.click(c, t)
			return p, cmd, true
		})
	}
	if m.topScreen() >= 0 || m.prompting() || !ok {
		return m, nil
	}
	switch a.region {
	case regTreeDivider:
		m.drag = dragTree
		return m, nil
	case regFilesDivider:
		m.drag = dragFiles
		return m, nil
	}
	k, ok := a.region.container()
	if !ok {
		return m, nil
	}
	focused := m.focused == k
	if !focused {
		m.flash = ""
	}
	m = m.focusContainer(k)
	t, hit := a.target(ms.X, ms.Y)
	if !hit {
		return m, nil
	}
	if k == rightSidebar && t.kind == hitTab {
		m.right.tab = sideTab(t.index)
		return m, nil
	}
	return m.paneMouse(k, func(c *ctx, comp component) (component, tea.Cmd, bool) {
		cl, ok := comp.(clicker)
		if !ok {
			return comp, nil, false
		}
		comp, cmd := cl.click(c, t, focused)
		return comp, cmd, true
	})
}

func (m model) liveScreenTakesWheel() bool {
	return m.topScreen() >= 0 && !m.helpShown() && len(m.popups) == 0
}

func (m model) mouseWheel(msg wheelMsg) (tea.Model, tea.Cmd) {
	m.drag = dragNone
	if msg.delta == 0 {
		return m, nil
	}
	if !m.mouse && !m.liveScreenTakesWheel() {
		return m, nil
	}
	if m.helpShown() {
		m.helpScroll = max(0, min(m.helpScroll+msg.delta, m.helpMaxScroll()))
		return m, nil
	}
	a, ok := m.hits.at(msg.X, msg.Y)
	if len(m.popups) > 0 {
		if !ok || a.region != regPopup {
			return m, nil
		}
		return m.popupMouse(func(c *ctx, p popup) (popup, tea.Cmd, bool) {
			pw, ok := p.(popupWheeler)
			if !ok {
				return p, nil, false
			}
			p, cmd := pw.wheel(c, msg.delta)
			return p, cmd, true
		})
	}
	if i := m.topScreen(); i >= 0 {
		if !ok || a.region != regMain {
			return m, nil
		}
		return m.updateScreen(i, screenWheelMsg{x: msg.X - a.rect.Min.X, y: msg.Y - a.rect.Min.Y, delta: msg.delta})
	}
	if m.prompting() || !ok {
		return m, nil
	}
	k, ok := a.region.container()
	if !ok {
		return m, nil
	}
	return m.paneMouse(k, func(c *ctx, comp component) (component, tea.Cmd, bool) {
		w, ok := comp.(wheeler)
		if !ok {
			return comp, nil, false
		}
		comp, cmd := w.wheel(c, msg.delta)
		return comp, cmd, true
	})
}

// f reports false when the component does not take the event.
func (m model) paneMouse(k container, f func(*ctx, component) (component, tea.Cmd, bool)) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	var comp component
	switch k {
	case leftSidebar:
		comp = m.left.tree
	case rightSidebar:
		comp = m.right.current()
	case sessionDock:
		comp = m.dock
	default:
		comp = m.main.top()
	}
	comp, cmd, ok := f(c, comp)
	if !ok {
		return m, nil
	}
	switch k {
	case leftSidebar:
		m.left.tree = comp.(projectTreeComp)
	case rightSidebar:
		m.right = m.right.store(comp)
	case sessionDock:
		m.dock = comp.(dockComp)
	default:
		m.main = m.main.replaceTop(comp)
	}
	return m, tea.Batch(cmd, m.apply(c))
}

func (m model) popupMouse(f func(*ctx, popup) (popup, tea.Cmd, bool)) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	p, cmd, ok := f(c, m.popups.front())
	if !ok {
		return m, nil
	}
	m.popups = m.popups.replaceFront(p)
	return m, tea.Batch(cmd, m.apply(c))
}

// A drag keeps the divider glyph under the pointer: the glyph is the middle
// column of the divider.
func (m model) mouseMotion(ms tea.Mouse) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	switch {
	case m.drag == dragTree && m.sidebarVisible():
		m.left = m.left.setWidth(c, ms.X-screenMargin-1)
	case m.drag == dragFiles && m.filesVisible():
		m.right = m.right.setWidth(c, m.width-ms.X-screenMargin-dividerWidth/2-1)
	default:
		m.drag = dragNone
	}
	return m, nil
}

// Motion is throttled, so the last motion before a release may be dropped.
func (m model) mouseRelease(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if m.drag != dragNone {
		next, _ := m.mouseMotion(ms)
		m = next.(model)
	}
	m.drag = dragNone
	return m, nil
}
