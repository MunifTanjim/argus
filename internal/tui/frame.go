package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/MunifTanjim/argus/internal/session"
)

// View and the size functions that input handling reads both come from
// layout, so drawing and scroll math agree.
type frameLayout struct {
	full  bool // the top asked for the full screen: no left sidebar, no margin
	bare  bool // the top takes the whole terminal and draws its own title
	left  int  // the left sidebar's width; 0 when it does not show
	right int  // the right sidebar's width; 0 when it does not show
	w, h  int  // the main pane's size, its footer rows included
}

func (m model) layout() frameLayout {
	full := m.topFull()
	if full == fullTerminal {
		return frameLayout{full: true, bare: true, w: m.width, h: m.height}
	}
	l := frameLayout{full: full == fullWidth, h: m.framedHeight()}
	if m.filesVisible() {
		l.right = m.projectsFilesW()
	}
	switch {
	case l.full:
		l.w = max(1, m.width-m.rightCols())
	case m.sidebarVisible():
		l.left = m.projectsLeftW()
		l.w = m.bodyWidthFor(l.left)
	default:
		l.w = m.bodyWidthFor(-dividerWidth)
	}
	return l
}

func (m model) topFull() fullLevel { return m.main.top().fullScreen(&ctx{m: &m}) }

func (m model) framedHeight() int { return max(1, m.height-2) }

// screenMargin is the left margin of the frame, so content never touches the
// terminal edge and nothing shifts when the sidebar toggles.
const screenMargin = 2

func (m model) frameWidth() int { return max(1, m.width-screenMargin) }

// footerRows is the height a pinned footer takes: a blank row and the footer.
const footerRows = 2

func (m model) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	// The live screen takes the wheel for its program even with the mouse off.
	if _, live := m.liveScreen(); live || m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	if _, hovers := m.popups.front().(popupHoverer); hovers && m.mouse {
		v.MouseMode = tea.MouseModeAllMotion
	}
	v.Cursor = m.screenCursor()
	return v
}

// screenCursor shows the live screen program's cursor while the screen has the
// keys.
func (m model) screenCursor() *tea.Cursor {
	s, ok := m.liveScreen()
	if !ok || m.focused != mainPane || len(m.popups) > 0 || m.helpShown() {
		return nil
	}
	l := m.layout()
	_, rows := termDimsFor(l.w, l.h-m.dockRows())
	return s.cursor(m.mainRect().Min, rows)
}

func (m model) frame() string {
	m.hits.reset()
	if m.helpShown() {
		return m.helpScreen()
	}
	l := m.layout()
	c := &ctx{m: &m}
	pane := m.mainColumn(c, l)
	footer := m.currentFooter()
	if len(m.popups) > 0 {
		footer = m.popupFooter()
	}
	var out string
	if l.bare {
		out = pinFooter(pane, footer, m.width, m.height)
	} else {
		out = pinFooter(m.frameTitle()+"\n\n"+m.framedBody(c, l, pane), footer, m.width, m.height)
	}
	return m.drawPopups(out)
}

func (m model) mainRect() uv.Rectangle {
	l := m.layout()
	h := max(1, l.h-footerRows)
	if l.bare {
		return uv.Rect(0, 0, l.w, h)
	}
	x := screenMargin
	switch {
	case l.full:
		x = 0
	case l.left > 0:
		x = l.left + screenMargin + dividerWidth
	}
	// The body starts below the title row and a blank row.
	return uv.Rect(x, 2, l.w, h)
}

// A spawn flow opened from the tree or over a row's pane stays in front of the
// help overlay.
func (m model) helpShown() bool {
	s, spawning := m.spawnTop()
	return m.showHelp && !(spawning && (m.onRowPane() || s.back == leftSidebar))
}

func (m model) mainColumn(c *ctx, l frameLayout) string {
	dockH := m.dockRows()
	r := m.mainRect()
	mc := &ctx{m: c.m, area: m.hits.add(regMain, r)}
	pane := m.main.top().view(mc, l.w, l.h-dockH)
	if dockH > 0 {
		paneH := lipgloss.Height(pane)
		if mc.area != nil {
			mc.area.rect.Max.Y = r.Min.Y + paneH
		}
		dc := &ctx{m: c.m, area: m.hits.add(regDock, uv.Rect(r.Min.X, r.Min.Y+paneH, r.Dx(), dockH))}
		pane += "\n" + m.dock.view(dc, l.w, dockH)
	}
	return pane
}

func (m model) mainSize() (w, h int) {
	l := m.layout()
	return l.w, l.h - m.dockRows()
}

func (m model) dockRows() int {
	if !m.dockDrawn() {
		return 0
	}
	_, dockH := m.sessionLayout()
	return dockH
}

func (m model) dockDrawn() bool {
	if top := m.main.top(); top == nil || top.layer() == overLayer {
		return false
	}
	return m.dockShown() && m.sessions[m.liveSessionID()].Status != session.StatusStarting
}

func (m model) framedBody(c *ctx, l frameLayout, pane string) string {
	h := max(1, l.h-footerRows)
	var panels []hpanel
	switch {
	case l.full:
		panels = append(panels, flexPanel(pane))
	case l.left == 0:
		panels = append(panels, flexPanel(indentBlock(pane, strings.Repeat(" ", screenMargin))))
	default:
		tc := &ctx{m: c.m, area: m.hits.add(regTree, uv.Rect(0, 2, l.left+screenMargin, h))}
		m.hits.add(regTreeDivider, uv.Rect(l.left+screenMargin, 2, dividerWidth, h))
		panels = append(panels, fixedPanel(m.left.tree.view(tc, l.left, h), l.left+screenMargin), flexPanel(pane))
	}
	if l.right > 0 {
		x := m.width - l.right - screenMargin
		rc := &ctx{m: c.m, area: m.hits.add(regRight, uv.Rect(x, 2, l.right+screenMargin, h))}
		m.hits.add(regFilesDivider, uv.Rect(x-dividerWidth, 2, dividerWidth, h))
		panels = append(panels, fixedPanel(m.right.view(rc, l.right, h), l.right+screenMargin))
	}
	return composeH(m.width, h, panels...)
}

// frameTitle is the status bar.
func (m model) frameTitle() string {
	left := strings.Repeat(" ", screenMargin) + Icon.Claude.Render() + " " + headerStyle.Render("argus")
	var parts []string
	if n := m.waitingCount(); n > 0 {
		attn := lipgloss.NewStyle().Foreground(statusColor(session.StatusAwaitingInput))
		parts = append(parts, attn.Render(statusGlyph(session.StatusAwaitingInput)+" "+strconv.Itoa(n)+" need you"))
	}
	if m.reconnecting {
		parts = append(parts, dimStyle.Render("reconnecting…"))
	}
	if m.quarantined() {
		parts = append(parts, StyleErrorBold.Render("⚠ QUARANTINED")+dimStyle.Render(" · argus lock pin"))
	}
	if len(parts) == 0 {
		return left
	}
	right := strings.Join(parts, dimStyle.Render(" · "))
	gap := max(1, m.width-screenMargin-lipgloss.Width(left)-lipgloss.Width(right))
	return truncateLeft(left+strings.Repeat(" ", gap)+right, max(1, m.width-screenMargin))
}

func (m model) waitingCount() int {
	n := 0
	for _, s := range m.sessions {
		if s.Status == session.StatusAwaitingInput && !s.Offline {
			n++
		}
	}
	return n
}

// footerPrompter is a component that can ask a question or take text in the
// footer; its prompt, when not "", takes the whole footer.
type footerPrompter interface {
	footerPrompt(c *ctx) string
}

// footerTexter is a component whose footer draws more than its bindings.
type footerTexter interface {
	footerText(c *ctx) string
}

func (m model) currentFooter() string {
	c := &ctx{m: &m}
	comp := m.focusedComp()
	if p, ok := comp.(footerPrompter); ok {
		if s := p.footerPrompt(c); s != "" {
			return s
		}
	}
	switch {
	case len(m.keyBuf) > 0:
		return asstStyle.Render(m.keyHint())
	case m.flash != "":
		return asstStyle.Render(firstLine(m.flash))
	case m.showHelp:
		return m.footer(hint("any key", "close"))
	}
	if f, ok := comp.(footerTexter); ok {
		return f.footerText(c)
	}
	return m.footer(comp.footer(c)...)
}

func (m model) cardListPageStep() int { return m.focusedComp().pageStep(&ctx{m: &m}) }

// cardPageStep is how many cards a half-page jump moves in rows rows, from a
// card's nominal line count (~5).
func cardPageStep(rows int) int { return max(1, max(1, rows)/5/2) }

func (m model) paneRows() int { return m.layout().h - footerRows }

// listPageStep is the page step of a card list below a title and a blank line.
func (m model) listPageStep() int { return cardPageStep(m.paneRows() - 2) }

// The help overlay covers every component, so nothing spins under it.
func (m model) spinShown() bool {
	if m.helpShown() {
		return false
	}
	c := &ctx{m: &m}
	if p := m.popups.front(); p != nil && p.spins(c) {
		return true
	}
	l := m.layout()
	if top := m.main.top(); top != nil && top.spins(c) {
		return true
	}
	return l.left > 0 && m.left.tree.spins(c) ||
		l.right > 0 && m.right.current().spins(c) ||
		m.dockDrawn() && m.dock.spins(c)
}
