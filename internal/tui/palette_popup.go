package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
)

const (
	paletteMaxWidth = 100
	paletteMaxRows  = 12
	// paletteChrome is the rows around the list: the frame, the title, the
	// scope line, the input, the help, and the blank rows between them.
	paletteChrome = 9
)

// The mode with no prefix is the default.
func paletteModes() []paletteMode {
	return []paletteMode{{
		title:       "Go to",
		placeholder: "Search sessions, workspaces, projects",
		sources:     []paletteSource{sessionsSource{}, placesSource{}},
		scoped:      true,
	}}
}

// palettePopup is the open palette. snaps holds the snapshot of each mode
// that the palette entered. scope is the id of the item that the list is
// under; "" is the root.
type palettePopup struct {
	input   textinput.Model
	modes   []paletteMode
	snaps   map[int]paletteSnapshot
	mode    int
	scope   string
	matches []paletteMatch
	cursor  int
}

func (palettePopup) id() string         { return "palette" }
func (palettePopup) keySection() string { return "" }
func (palettePopup) spins(*ctx) bool    { return false }

func (m model) openPalette() (model, tea.Cmd) {
	in := textinput.New()
	in.Prompt = ""
	p := palettePopup{input: in, modes: paletteModes(), snaps: map[int]paletteSnapshot{}}
	p.mode, _ = modeFor(p.modes, "")
	p.input.Placeholder = p.modes[p.mode].placeholder
	p.refresh(m)
	// A first scope that the snapshot lacks, such as a node when the tree
	// shows no node rows, falls back to the root. One with nothing under it,
	// such as a new workspace, falls back to the nearest scope above it.
	scope := m.paletteScope()
	if _, ok := p.snap().item(scope); !ok || !p.scoped() {
		scope = ""
	}
	for scope != "" && !p.snap().hasKids(scope) {
		scope = p.snap().parent(scope)
	}
	if scope != "" {
		p.scope = scope
		p.refresh(m)
	}
	cmd := p.input.Focus()
	m.flash = ""
	m.popups = m.popups.open(p)
	return m, cmd
}

// paletteScope is the palette id of the place that the user is in: the tree's
// cursor row while the tree has focus, else the main pane's place.
func (m model) paletteScope() string {
	if m.focused == leftSidebar {
		if r, ok := m.left.tree.cursorRow(); ok {
			return placeID(r.kind, r.id)
		}
		return ""
	}
	if b, ok := m.baseComp().(summaryComp); ok {
		return placeID(b.kind, b.id)
	}
	if id := m.mainRow(); id != "" && id != homeRowID {
		return placeID(rowWorkspace, id)
	}
	return ""
}

func (p palettePopup) scoped() bool { return p.modes[p.mode].scoped }

func (p palettePopup) snap() paletteSnapshot { return p.snaps[p.mode] }

// refresh matches the query again. It takes the snapshot of a mode the first
// time that the query enters the mode.
func (p *palettePopup) refresh(m model) {
	mode, q := modeFor(p.modes, p.input.Value())
	p.mode = mode
	if _, ok := p.snaps[mode]; !ok {
		p.snaps[mode] = takeSnapshot(m, p.modes[mode])
	}
	items := p.snap().items
	if p.scoped() {
		items = p.snap().under(p.scope)
	}
	p.matches, p.cursor = matchPalette(items, q), 0
}

func (p palettePopup) query() string {
	_, q := modeFor(p.modes, p.input.Value())
	return q
}

func (p palettePopup) update(c *ctx, msg tea.Msg) (popup, tea.Cmd) {
	if msg, ok := msg.(tea.PasteMsg); ok {
		return p.edit(*c.m, msg)
	}
	return p, nil
}

func (p palettePopup) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.closePopup()
		return p, nil
	case "enter":
		if p.cursor < len(p.matches) && len(p.matches[p.cursor].actions) > 0 {
			c.closePopup()
			p.matches[p.cursor].actions[0].run(c)
		}
		return p, nil
	case "tab":
		p.narrow(*c.m)
		return p, nil
	case "backspace":
		if p.query() == "" && p.scoped() && p.scope != "" {
			p.scope = p.snap().parent(p.scope)
			p.refresh(*c.m)
			return p, nil
		}
	case "up", "ctrl+p":
		p.move(-1)
		return p, nil
	case "down", "ctrl+n":
		p.move(1)
		return p, nil
	}
	return p.edit(*c.m, msg)
}

func (p palettePopup) canNarrow() bool {
	return p.scoped() && p.cursor < len(p.matches) && p.snap().hasKids(p.matches[p.cursor].id)
}

// narrow makes the highlighted item the scope and clears the query.
func (p *palettePopup) narrow(m model) {
	if !p.canNarrow() {
		return
	}
	p.scope = p.matches[p.cursor].id
	p.input.SetValue(p.modes[p.mode].prefix)
	p.input.CursorEnd()
	p.refresh(m)
}

func (p *palettePopup) move(d int) {
	if n := len(p.matches); n > 0 {
		p.cursor = (p.cursor + d + n) % n
	}
}

func (p palettePopup) edit(m model, msg tea.Msg) (popup, tea.Cmd) {
	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
		p.refresh(m)
	}
	return p, cmd
}

// draw centers the palette over the whole terminal, since it acts on every
// pane.
func (p palettePopup) draw(c *ctx, scr uv.Screen, area uv.Rectangle) {
	f := popupFrame{
		width:  min(paletteMaxWidth, max(area.Dx()-4, popupFrameW+1)),
		height: min(area.Dy(), paletteMaxRows+paletteChrome),
		title:  p.modes[p.mode].title,
	}
	iw := f.innerWidth()
	f.help = c.m.hints(iw, p.footer()...)
	var head []string
	if p.scoped() {
		head = append(head, p.scopeLine(iw))
	}
	head = append(head, p.inputLine(iw), "")
	f.parts = append(head, p.listRows(iw, max(1, f.bodyHeight()-len(head)))...)
	drawCenter(scr, area, f.render())
}

func (p palettePopup) footer() []binding {
	out := []binding{hint("↑/↓", "choose"), hint("enter", "open")}
	if p.canNarrow() {
		out = append(out, hint("tab", "narrow"))
	}
	if p.scoped() && p.scope != "" {
		out = append(out, hint("⌫", "widen"))
	}
	return append(out, hint("esc", "close"))
}

// scopeLine is the scope chain, root first, each scope with its list marker.
// A chain wider than w loses its start, so the nearest scope stays.
func (p palettePopup) scopeLine(w int) string {
	chain := p.snap().chain(p.scope)
	if len(chain) == 0 {
		return StyleDim.Render("Everywhere")
	}
	labels := make([]string, len(chain))
	for i, it := range chain {
		labels[i] = it.marker + " " + StyleSecondary.Render(it.label)
	}
	line := strings.Join(labels, StyleDim.Render(" › "))
	if over := lipgloss.Width(line) - w; over > 0 {
		line = xansi.TruncateLeft(line, over+1, StyleDim.Render("…"))
	}
	return line
}

func (p palettePopup) inputLine(w int) string {
	p.input.SetWidth(max(1, w-1))
	return p.input.View()
}

// listRows are the matches, scrolled to keep the cursor in n rows. The label
// width fits every match, not only the shown ones, so it stays while
// scrolling.
func (p palettePopup) listRows(w, n int) []string {
	if len(p.matches) == 0 {
		return []string{StyleDim.Render("no matches")}
	}
	labelW := 0
	for _, pm := range p.matches {
		labelW = max(labelW, lipgloss.Width(pm.label))
	}
	labelW = min(labelW, w*3/5)
	rows := make([]string, len(p.matches))
	for i, pm := range p.matches {
		base, dim := lipgloss.NewStyle(), StyleDim
		if i == p.cursor {
			base = StyleSecondary.Reverse(true)
			dim = base
		}
		label := xansi.Truncate(highlightName(pm.label, pm.labelHits, base), labelW, "…")
		label += base.Render(strings.Repeat(" ", max(0, labelW-lipgloss.Width(label))))
		row := xansi.Truncate(pm.marker+" "+label+base.Render("  ")+highlightName(pm.detail, pm.detailHits, dim), w, "…")
		if i == p.cursor {
			row += base.Render(strings.Repeat(" ", max(0, w-lipgloss.Width(row))))
		}
		rows[i] = row
	}
	return windowSpan(rows, p.cursor, p.cursor+1, n)
}
