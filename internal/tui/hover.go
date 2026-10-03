package tui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
)

// hoverer is a component whose cursor item can show its full details, which
// its list truncates.
type hoverer interface {
	hoverItem(c *ctx) (hoverItem, bool)
}

// hoverItem is what the hover shows for one item. key tells items apart, so
// that the hover closes when the cursor leaves the item. index is the item's
// index in its view's hit map rows.
type hoverItem struct {
	key    string
	index  int
	title  string
	fields [][2]string
}

func (it *hoverItem) add(label, value string) {
	if value != "" {
		it.fields = append(it.fields, [2]string{label, value})
	}
}

func (m model) hoverItem() (hoverItem, bool) {
	h, ok := m.focusedComp().(hoverer)
	if !ok {
		return hoverItem{}, false
	}
	return h.hoverItem(&ctx{m: &m})
}

func (m model) openHover() model {
	if it, ok := m.hoverItem(); ok {
		m.hovered = it.key
	}
	return m
}

func (m model) syncHover() model {
	if m.hovered == "" {
		return m
	}
	if it, ok := m.hoverItem(); !ok || it.key != m.hovered || len(m.popups) > 0 || m.showHelp {
		m.hovered = ""
	}
	return m
}

func (m model) drawHover(scr uv.Screen) {
	if m.hovered == "" {
		return
	}
	it, ok := m.hoverItem()
	if !ok {
		return
	}
	at, ok := m.hits.rowStart(m.focused.region(), it.index)
	if !ok {
		return
	}
	box := it.render(m.width)
	at.X += screenMargin
	if h := lipgloss.Height(box); at.Y+1+h <= m.height {
		at.Y++
	} else {
		at.Y -= h
	}
	drawAt(scr, scr.Bounds(), at, box)
}

func (it hoverItem) render(maxW int) string {
	labelW := 0
	for _, f := range it.fields {
		labelW = max(labelW, lipgloss.Width(f[0]))
	}
	contentW := lipgloss.Width(it.title)
	for _, f := range it.fields {
		contentW = max(contentW, labelW+2+lipgloss.Width(f[1]))
	}
	f := popupFrame{width: min(maxW, contentW+popupFrameW), title: it.title}
	valueW := max(1, f.innerWidth()-labelW-2)
	pad := strings.Repeat(" ", labelW+2)
	for _, fl := range it.fields {
		lines := strings.Split(xansi.Wrap(fl[1], valueW, "/-_."), "\n")
		f.parts = append(f.parts, dimStyle.Render(fl[0]+strings.Repeat(" ", labelW+2-lipgloss.Width(fl[0])))+lines[0])
		for _, l := range lines[1:] {
			f.parts = append(f.parts, pad+l)
		}
	}
	return f.render()
}
