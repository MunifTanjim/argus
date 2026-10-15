package tui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

type dragTarget int

const (
	dragNone dragTarget = iota
	dragTree
	dragFiles
	dragSelect
)

// selectDrag is a press on selectable text: a drag from it selects, and a
// release without a drag is the click it deferred.
type selectDrag struct {
	k            container
	line, col    int
	target       hitTarget
	hit, focused bool
	moved        bool
}

// focused reports whether the pane had focus before the click.
type clicker interface {
	click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd)
}

type wheeler interface {
	wheel(c *ctx, delta int) (component, tea.Cmd)
}

// menuer is a component with commands for the row under its cursor.
type menuer interface {
	menu(c *ctx) []binding
}

type popupClicker interface {
	click(c *ctx, t hitTarget) (popup, tea.Cmd)
}

type popupWheeler interface {
	wheel(c *ctx, delta int) (popup, tea.Cmd)
}

// popupHoverer is a popup whose cursor follows the pointer. While it is in
// front, the frame asks for every pointer move, not only drags.
type popupHoverer interface {
	hover(t hitTarget) popup
}

type outsideCloser interface{ closesOnOutsideClick() }

var enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}

func (m model) prompting() bool {
	p, ok := m.focusedComp().(footerPrompter)
	return ok && p.footerPrompt(&ctx{m: &m}) != ""
}

func (m model) mouseClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	m.drag = dragNone
	if !m.mouse {
		return m, nil
	}
	a, ok := m.hits.at(ms.X, ms.Y)
	if len(m.popups) > 0 && !m.helpShown() && (!ok || a.region != regPopup) {
		if _, closes := m.popups.front().(outsideCloser); closes {
			m.popups = m.popups.closeFront()
		}
		return m, nil
	}
	if ms.Button != tea.MouseLeft && ms.Button != tea.MouseRight {
		return m, nil
	}
	if len(m.keyBuf) > 0 {
		m.clearKeys()
	}
	if m.helpShown() {
		m.showHelp = false
		return m, nil
	}
	if len(m.popups) > 0 {
		t, hit := a.target(ms.X, ms.Y)
		if !hit || ms.Button != tea.MouseLeft {
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
		if ms.Button == tea.MouseLeft {
			m.drag = dragTree
		}
		return m, nil
	case regFilesDivider:
		if ms.Button == tea.MouseLeft {
			m.drag = dragFiles
		}
		return m, nil
	case regTitle:
		return m.titleClick(a, ms), nil
	}
	k, ok := a.region.container()
	if !ok {
		return m, nil
	}
	t, hit := a.target(ms.X, ms.Y)
	if ms.Button == tea.MouseRight && hit && t.kind != hitRow {
		return m, nil
	}
	focused := m.focused == k
	if !focused {
		m.flash = ""
		m = m.endVisualIn(m.focused)
	}
	m = m.focusContainer(k)
	_, selects := m.paneComp(k).(textSelector)
	switch ms.Button {
	case tea.MouseRight:
		if m.visualIn(k) {
			return m.openMenuOnly(ms), nil // the selection stays, so no card-select click
		}
	case tea.MouseLeft:
		if selects && a.text != nil && (!hit || t.kind == hitRow) {
			if line, col, inside := a.textAt(ms.X, ms.Y); inside {
				m.drag = dragSelect
				m.sel = selectDrag{k: k, line: line, col: col, target: t, hit: hit, focused: focused}
				return m, nil
			}
		}
		m = m.endVisualIn(k) // a click ends visual mode, then acts as a click
	}
	if !hit {
		return m, nil
	}
	if ms.Button == tea.MouseRight {
		return m.openMenu(k, t, ms)
	}
	if k == leftSidebar && t.kind == hitTab {
		m.left = m.left.setTab(&ctx{m: &m}, leftTab(t.index))
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

func (m model) visualIn(k container) bool {
	ts, ok := m.paneComp(k).(textSelector)
	return ok && ts.visualActive()
}

// endVisualIn ends pane k's visual mode, as a click does.
func (m model) endVisualIn(k container) model {
	if !m.visualIn(k) {
		return m
	}
	res, _ := m.paneMouse(k, func(c *ctx, comp component) (component, tea.Cmd, bool) {
		return comp.(textSelector).endVisual(c), nil, true
	})
	return res.(model)
}

func (m model) openMenu(k container, t hitTarget, at tea.Mouse) (tea.Model, tea.Cmd) {
	res, cmd := m.paneMouse(k, func(c *ctx, comp component) (component, tea.Cmd, bool) {
		cl, ok := comp.(clicker)
		if !ok {
			return comp, nil, false
		}
		comp, cmd := cl.click(c, t, false)
		return comp, cmd, true
	})
	return res.(model).openMenuOnly(at), cmd
}

// openMenuOnly opens the focused component's menu as it is.
func (m model) openMenuOnly(at tea.Mouse) model {
	if m.keysRaw() {
		return m
	}
	mn, ok := m.focusedComp().(menuer)
	if !ok {
		return m
	}
	entries := m.applicable(mn.menu(&ctx{m: &m}))
	if len(entries) == 0 {
		return m
	}
	m.popups = m.popups.open(contextMenu{at: uv.Pos(at.X, at.Y), screen: m.screen(), entries: entries})
	return m
}

func (m model) applicable(bs []binding) []binding {
	set := m.commandSet()
	var out []binding
	for _, b := range bs {
		if offersKey(set, b) {
			out = append(out, b)
		}
	}
	return out
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
	comp, cmd, ok := f(c, m.paneComp(k))
	if !ok {
		return m, nil
	}
	switch k {
	case leftSidebar:
		m.left = m.left.store(comp)
	case rightSidebar:
		m.right = m.right.store(comp)
	case sessionDock:
		m.dock = comp.(dockComp)
	default:
		m.main = m.main.replaceTop(comp)
	}
	return m, tea.Batch(cmd, m.apply(c))
}

func (m model) paneComp(k container) component {
	switch k {
	case leftSidebar:
		return m.left.current()
	case rightSidebar:
		return m.right.current()
	case sessionDock:
		return m.dock
	}
	return m.main.top()
}

// textAreaOf is the pane's text area in the last frame.
func (m model) textAreaOf(k container) *hitArea {
	if m.hits == nil {
		return nil
	}
	for _, a := range m.hits.areas {
		if a.region == k.region() && a.text != nil {
			return a
		}
	}
	return nil
}

// selectTo extends the drag's selection to the point under ms, anchoring it
// at the press on the first move.
func (m model) selectTo(ms tea.Mouse) (tea.Model, tea.Cmd) {
	a := m.textAreaOf(m.sel.k)
	if a == nil {
		return m, nil
	}
	line, col, _ := a.textAt(ms.X, ms.Y)
	first := !m.sel.moved
	m.sel.moved = true
	s := m.sel
	return m.paneMouse(s.k, func(c *ctx, comp component) (component, tea.Cmd, bool) {
		ts, ok := comp.(textSelector)
		if !ok {
			return comp, nil, false
		}
		if first {
			comp = ts.selectText(c, s.line, s.col, false)
			ts = comp.(textSelector)
		}
		return ts.selectText(c, line, col, true), nil, true
	})
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

func (m model) titleClick(a *hitArea, ms tea.Mouse) model {
	t, hit := a.target(ms.X, ms.Y)
	switch {
	case !hit || ms.Button != tea.MouseLeft:
	case t.index == treeIcon:
		m = m.toggleTreeFocus()
	case t.index == filesIcon:
		m = m.toggleFilesFocus()
	case t.index == brandIcon:
		m.cycleBrand()
	}
	return m
}

// screenMouse hands a button event over the main pane to the live screen; a
// motion with no button held is not a drag and stays local.
func (m model) screenMouse(ms tea.Mouse, kind screenMouseKind) (tea.Model, tea.Cmd) {
	a, ok := m.hits.at(ms.X, ms.Y)
	if ok && a.region == regTitle && kind == mousePress && m.mouse {
		return m.titleClick(a, ms), nil
	}
	if !ok || a.region != regMain || ms.Button == tea.MouseNone {
		return m, nil
	}
	if t, hit := a.target(ms.X, ms.Y); hit && t.kind == hitClose {
		if kind != mousePress || ms.Button != tea.MouseLeft || !m.mouse {
			return m, nil
		}
		c := &ctx{m: &m}
		c.back()
		return m, m.apply(c)
	}
	return m.updateScreen(m.topScreen(), screenMouseMsg{x: ms.X - a.rect.Min.X, y: ms.Y - a.rect.Min.Y, button: ms.Button, kind: kind})
}

// A drag keeps the divider glyph under the pointer: the glyph is the middle
// column of the divider.
func (m model) mouseMotion(ms tea.Mouse) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	switch {
	case m.drag == dragNone:
		return m.hover(ms)
	case m.drag == dragSelect:
		return m.selectTo(ms)
	case m.drag == dragTree && m.sidebarVisible():
		m.left = m.left.setWidth(c, ms.X-screenMargin-1)
	case m.drag == dragFiles && m.filesVisible():
		m.right = m.right.setWidth(c, m.width-ms.X-screenMargin-dividerWidth/2-1)
	default:
		m.drag = dragNone
	}
	return m, nil
}

func (m model) releasedAway(ms tea.Mouse) bool {
	a := m.textAreaOf(m.sel.k)
	if a == nil {
		return false
	}
	line, col, _ := a.textAt(ms.X, ms.Y)
	return line != m.sel.line || col != m.sel.col
}

func (m model) hover(ms tea.Mouse) (tea.Model, tea.Cmd) {
	h, ok := m.popups.front().(popupHoverer)
	if !ok {
		return m, nil
	}
	if a, ok := m.hits.at(ms.X, ms.Y); ok && a.region == regPopup {
		if t, hit := a.target(ms.X, ms.Y); hit {
			m.popups = m.popups.replaceFront(h.hover(t))
		}
	}
	return m, nil
}

// Motion is throttled, so the last motion before a release may be dropped: a
// release away from the press is a drag even when no motion arrived.
func (m model) mouseRelease(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if m.drag == dragSelect {
		m.drag = dragNone
		if m.sel.moved || m.releasedAway(ms) {
			return m.selectTo(ms)
		}
		s := m.sel
		m = m.endVisualIn(s.k) // a click ends visual mode, then acts as a click
		if !s.hit {
			return m, nil
		}
		return m.paneMouse(s.k, func(c *ctx, comp component) (component, tea.Cmd, bool) {
			cl, ok := comp.(clicker)
			if !ok {
				return comp, nil, false
			}
			comp, cmd := cl.click(c, s.target, s.focused)
			return comp, cmd, true
		})
	}
	if m.drag != dragNone {
		next, _ := m.mouseMotion(ms)
		m = next.(model)
	}
	m.drag = dragNone
	return m, nil
}
