package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
)

// popup draws over the frame and takes every key and reply while it is the
// front popup, the way crush's dialogs do.
// keySection is the keymap section its keys match in; "" reads no bindings.
type popup interface {
	id() string
	keySection() string
	handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd)
	update(c *ctx, msg tea.Msg) (popup, tea.Cmd)
	draw(c *ctx, scr uv.Screen, area uv.Rectangle)
	spins(c *ctx) bool
}

type popupStack []popup

func (s popupStack) front() popup {
	if len(s) == 0 {
		return nil
	}
	return s[len(s)-1]
}

func (s popupStack) open(p popup) popupStack { return append(s[:len(s):len(s)], p) }

func (s popupStack) closeFront() popupStack {
	if len(s) == 0 {
		return s
	}
	return s[: len(s)-1 : len(s)-1]
}

func (s popupStack) replaceFront(p popup) popupStack {
	out := slices.Clone(s)
	out[len(out)-1] = p
	return out
}

// The new value is stored before the actions run, so that a popup that closes
// itself stays closed.
func (m model) popupKey(p popup, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	p, cmd := p.handleKey(c, msg)
	m.popups = m.popups.replaceFront(p)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (m model) updatePopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	cmd := m.updatePopupIn(c, msg)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (m *model) updatePopupIn(c *ctx, msg tea.Msg) tea.Cmd {
	p := m.popups.front()
	if p == nil {
		return nil
	}
	p, cmd := p.update(c, msg)
	m.popups = m.popups.replaceFront(p)
	return cmd
}

// popupFooter shows only the flash, since a popup shows its own keys.
func (m model) popupFooter() string {
	if m.flash == "" {
		return ""
	}
	return asstStyle.Render(firstLine(m.flash))
}

func (m model) drawPopups(frame string) string {
	if len(m.popups) == 0 || m.width < 1 || m.height < 1 {
		return frame
	}
	scr := uv.NewScreenBuffer(m.width, m.height)
	uv.NewStyledString(frame).Draw(scr, scr.Bounds())
	c := &ctx{m: &m}
	for _, p := range m.popups {
		p.draw(c, scr, scr.Bounds())
	}
	return scr.Render()
}

func centerRect(area uv.Rectangle, w, h int) uv.Rectangle {
	return uv.Rect(area.Min.X+area.Dx()/2-w/2, area.Min.Y+area.Dy()/2-h/2, w, h)
}

func bottomLeftRect(area uv.Rectangle, w, h int) uv.Rectangle {
	return uv.Rect(area.Min.X, area.Max.Y-h, w, h)
}

func drawCenter(scr uv.Screen, area uv.Rectangle, view string) {
	w, h := lipgloss.Size(view)
	uv.NewStyledString(view).Draw(scr, centerRect(area, min(w, area.Dx()), min(h, area.Dy())))
}

// popupFrameW and popupFrameH are the columns and rows a popupFrame's border
// and padding take. popupMinBody is the body rows the blank rows around the
// title and the help give way to.
const (
	popupFrameW  = 4
	popupFrameH  = 2
	popupMinBody = 3
)

// popupFrame is a popup's box: a title line, the parts, and a help line. width
// and height are the total size; height 0 fits the content.
type popupFrame struct {
	width, height    int
	title, titleInfo string
	parts            []string
	help             string
}

func (f popupFrame) innerWidth() int { return max(1, f.width-popupFrameW) }

func (f popupFrame) lines() int {
	n := 0
	if f.title != "" {
		n++
	}
	if f.help != "" {
		n++
	}
	return n
}

// spaced reports whether a blank row follows the title and precedes the help.
func (f popupFrame) spaced() bool {
	return f.height == 0 || f.height-popupFrameH-2*f.lines() >= popupMinBody
}

func (f popupFrame) bodyHeight() int {
	h := f.height - popupFrameH - f.lines()
	if f.spaced() {
		h -= f.lines()
	}
	return max(1, h)
}

func (f popupFrame) render() string {
	iw := f.innerWidth()
	var rows []string
	if f.title != "" {
		title := xansi.Truncate(f.title, iw, "…")
		line := StylePrimaryBold.Render(title)
		// Styled info cannot be cut cleanly, so it is dropped when it does not fit.
		if f.titleInfo != "" && lipgloss.Width(title)+1+lipgloss.Width(f.titleInfo) <= iw {
			line += " " + f.titleInfo
		}
		rows = append(rows, line)
		if f.spaced() {
			rows = append(rows, "")
		}
	}
	body := strings.Join(f.parts, "\n")
	if f.height > 0 {
		body = fitRows(body, f.bodyHeight())
	}
	rows = append(rows, truncateEachLine(body, iw))
	if f.help != "" {
		if f.spaced() {
			rows = append(rows, "")
		}
		rows = append(rows, xansi.Truncate(f.help, iw, "…"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorBorder).
		Padding(0, 1).Width(f.width).Render(strings.Join(rows, "\n"))
}

func fitRows(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
