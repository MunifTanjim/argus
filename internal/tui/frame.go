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
	x     int  // the main pane's screen column
	w, h  int  // the main pane's size, its footer rows included
	// joined: the pane draws against the left divider, or the terminal edge
	// with no left sidebar (see dividerJoiner).
	joined bool
	// dockX and dockW place the dock under the pane on screen.
	dockX, dockW int
}

func (m model) layout() frameLayout {
	full := m.topFull()
	if full == fullTerminal {
		return frameLayout{full: true, bare: true, w: m.width, h: m.height, dockW: m.width}
	}
	l := frameLayout{full: full >= fullWidth, h: m.framedHeight()}
	if m.filesVisible() {
		l.right = m.projectsFilesW()
	}
	switch {
	case l.full:
		l.w = max(1, m.width-m.rightCols())
	case m.sidebarVisible():
		l.left = m.projectsLeftW()
		l.x = l.left + screenMargin + dividerWidth
		l.w = m.bodyWidthFor(l.left)
	default:
		l.x = screenMargin
		l.w = m.bodyWidthFor(-dividerWidth)
	}
	if !l.full && m.joinsDivider() {
		// A joined pane starts in the divider's trailing space, or at the
		// terminal edge, and keeps a cell off the terminal edge on its right, as
		// the right divider's leading space does.
		l.joined = true
		x := l.x - 1
		if l.left == 0 {
			x = 0
		}
		l.w += l.x - x
		l.x = x
		if l.right == 0 {
			l.w = max(1, l.w-1)
		}
	}
	// The dock keeps a cell off what a joined pane draws against.
	l.dockX, l.dockW = l.x, l.w
	if l.joined {
		l.dockX, l.dockW = l.x+1, max(1, l.w-1)
	}
	return l
}

// dividerJoiner is a pane that draws its cursor marker against the divider on
// its left, or against the terminal edge with no left sidebar.
type dividerJoiner interface{ joinsDivider() }

func (m model) joinsDivider() bool {
	_, ok := m.main.top().(dividerJoiner)
	return ok
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
// keys, and marks the cursor end of a visual selection.
func (m model) screenCursor() *tea.Cursor {
	if m.focused != mainPane || len(m.popups) > 0 || m.helpShown() {
		return nil
	}
	s, ok := m.liveScreen()
	if !ok {
		return m.visualScreenCursor()
	}
	l := m.layout()
	_, rows := termDimsFor(l.w, l.h-m.dockRows(), l.right == 0)
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
	return m.drawOverlays(out)
}

func (m model) mainRect() uv.Rectangle {
	l := m.layout()
	h := max(1, l.h-footerRows)
	if l.bare {
		return uv.Rect(0, 0, l.w, h)
	}
	// The body starts below the title row and a blank row.
	return uv.Rect(l.x, 2, l.w, h)
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
		// Pad a short pane so the region sits at the bottom.
		if pad := m.sessionRows() - dockH - lipgloss.Height(pane); pad > 0 {
			pane += strings.Repeat("\n", pad)
		}
		paneH := lipgloss.Height(pane)
		if mc.area != nil {
			mc.area.rect.Max.Y = r.Min.Y + paneH
		}
		dc := &ctx{m: c.m, area: m.hits.add(regDock, uv.Rect(l.dockX, r.Min.Y+paneH, l.dockW, dockH))}
		pane += "\n" + indentBlock(m.dock.view(dc, l.dockW, dockH), strings.Repeat(" ", l.dockX-l.x))
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
	return !m.paneCovered() && m.dockShown()
}

func (m model) framedBody(c *ctx, l frameLayout, pane string) string {
	h := max(1, l.h-footerRows)
	var panels []hpanel
	switch {
	case l.full:
		panels = append(panels, flexPanel(pane))
	case l.left == 0:
		panels = append(panels, flexPanel(indentBlock(pane, strings.Repeat(" ", l.x))))
	default:
		tc := &ctx{m: c.m, area: m.hits.add(regTree, uv.Rect(0, 2, l.left+screenMargin, h))}
		main := flexPanel(pane)
		main.joined = l.joined
		divX := l.left + screenMargin
		m.hits.add(regTreeDivider, uv.Rect(divX, 2, l.x-divX, h))
		panels = append(panels, fixedPanel(m.left.view(tc, l.left, h), l.left+screenMargin), main)
	}
	if l.right > 0 {
		w := l.right + 1
		x := m.width - w - screenMargin
		rc := &ctx{m: c.m, area: m.hits.add(regRight, uv.Rect(x, 2, w+screenMargin, h))}
		m.hits.add(regFilesDivider, uv.Rect(x-dividerWidth+1, 2, dividerWidth-1, h))
		panels = append(panels, joinedPanel(m.right.view(rc, w, h), w+screenMargin))
	}
	return composeH(m.width, h, panels...)
}

// frameTitle is the status bar.
func (m model) frameTitle() string {
	left := strings.Repeat(" ", screenMargin) + m.brandIcon() + " " + headerStyle.Render("Argus")
	var parts []string
	if n := m.waitingCount(); n > 0 {
		attn := lipgloss.NewStyle().Foreground(statusColor(session.StatusAwaitingInput))
		parts = append(parts, attn.Render(statusGlyph(session.StatusAwaitingInput)+" "+strconv.Itoa(n)+" need you"))
	}
	if m.reconnecting {
		parts = append(parts, dimStyle.Render("reconnecting…"))
	}
	if m.serverExited != "" {
		parts = append(parts, StyleErrorBold.Render("⚠ server exited"))
	}
	if m.quarantined() {
		parts = append(parts, StyleErrorBold.Render("⚠ QUARANTINED")+dimStyle.Render(" · argus lock pin"))
	}
	right := strings.Join(parts, dimStyle.Render(" · "))
	if !m.viewer && m.width >= sidebarMinWidth {
		right += m.sidebarIcons()
	}
	if header, col := m.paneTitle(); header != "" {
		left = m.titleWithPane(left, header, col, lipgloss.Width(right))
	}
	if right == "" {
		return left
	}
	gap := max(1, m.width-screenMargin-lipgloss.Width(left)-lipgloss.Width(right))
	title := left + strings.Repeat(" ", gap) + right
	if lipgloss.Width(title) <= m.width-screenMargin {
		m.hitBrandIcon()
	}
	return truncateLeft(title, max(1, m.width-screenMargin))
}

// paneHeader is a pane that names its context in the top bar; col is the
// column of the pane content the header lines up with.
type paneHeader interface {
	paneHeader(c *ctx, w int) (header string, col int)
}

func (m model) paneTitle() (header string, col int) {
	p, ok := m.baseComp().(paneHeader)
	if !ok || m.paneCovered() {
		return "", 0
	}
	r := m.mainRect()
	header, col = p.paneHeader(&ctx{m: &m}, r.Dx())
	return header, r.Min.X + col
}

// paneCovered is true while the spawn flow covers the pane and its chrome.
func (m model) paneCovered() bool {
	top := m.main.top()
	return top == nil || top.layer() == overLayer
}

// titleWithPane sets the dot over the tree divider and the pane header at
// column headX. Without the tree, or without the room, the header follows the
// brand, clipped so that the brand and the right side stay.
func (m model) titleWithPane(brand, header string, headX, rightW int) string {
	dot := dimStyle.Render("·")
	room := m.width - screenMargin - 1 - rightW
	if l := m.layout(); l.left > 0 && !l.full {
		dotX := l.left + screenMargin + 1
		if dotX > lipgloss.Width(brand) && headX > dotX+1 && headX+lipgloss.Width(header) <= room {
			return brand + strings.Repeat(" ", dotX-lipgloss.Width(brand)) + dot +
				strings.Repeat(" ", headX-dotX-1) + header
		}
	}
	prefix := brand + " " + dot + " "
	if avail := room - lipgloss.Width(prefix); avail > 0 {
		return prefix + truncateLine(header, avail)
	}
	return brand
}

const (
	treeIcon = iota
	filesIcon
	brandIcon
)

func (m model) hitBrandIcon() {
	if a := m.hits.add(regTitle, uv.Rect(screenMargin, 0, 1, 1)); a != nil {
		a.zones = []hitZone{{rect: uv.Rect(0, 0, 1, 1), target: hitTarget{kind: hitTab, index: brandIcon}}}
	}
}

// sidebarIcons ends the title row, so the zones sit at the frame's right edge.
func (m model) sidebarIcons() string {
	icon := func(on bool, shown, hidden string) string {
		if on {
			return shown
		}
		return dimStyle.Render(hidden)
	}
	x := m.width - screenMargin - 3
	if a := m.hits.add(regTitle, uv.Rect(x, 0, 3, 1)); a != nil {
		a.zones = []hitZone{
			{rect: uv.Rect(0, 0, 1, 1), target: hitTarget{kind: hitTab, index: treeIcon}},
			{rect: uv.Rect(2, 0, 1, 1), target: hitTarget{kind: hitTab, index: filesIcon}},
		}
	}
	return "  " + icon(m.sidebarVisible(), glyphSidebarLeft, glyphSidebarLeftOff) +
		" " + icon(m.filesVisible(), glyphSidebarRight, glyphSidebarRightOff)
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
		return m.plainFooter(hint("any key", "close"))
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
	return l.left > 0 && m.left.current().spins(c) ||
		l.right > 0 && m.right.current().spins(c) ||
		m.dockDrawn() && m.dock.spins(c)
}

// visualScreenCursor is the terminal cursor on the cursor end of the main
// pane's visual selection, when that cell is on screen.
func (m model) visualScreenCursor() *tea.Cursor {
	t, ok := m.focusedComp().(transcriptComp)
	if !ok || !t.visualActive() {
		return nil
	}
	a := m.textAreaOf(mainPane)
	if a == nil {
		return nil
	}
	line, col, ok := t.bind(&ctx{m: &m}).visualCursor()
	if !ok || line < a.text.scroll || line >= a.text.end {
		return nil
	}
	return tea.NewCursor(a.rect.Min.X+a.text.left+col, a.rect.Min.Y+a.text.top+line-a.text.scroll)
}
