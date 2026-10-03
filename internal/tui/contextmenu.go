package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
)

// contextMenu is the right-click menu.
type contextMenu struct {
	at      uv.Position
	screen  string
	entries []binding
	cursor  int
}

func (p contextMenu) id() string                            { return "context-menu" }
func (p contextMenu) keySection() string                    { return "" }
func (p contextMenu) spins(*ctx) bool                       { return false }
func (p contextMenu) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }
func (p contextMenu) closesOnOutsideClick()                 {}

func (p contextMenu) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.closePopup()
	case "up", "k":
		p.cursor = cursorUp(p.cursor)
	case "down", "j":
		p.cursor = cursorDown(p.cursor, len(p.entries))
	case "enter":
		p.run(c)
	}
	return p, nil
}

func (p contextMenu) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	p.cursor = t.index
	p.run(c)
	return p, nil
}

func (p contextMenu) wheel(_ *ctx, d int) (popup, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, len(p.entries))
	return p, nil
}

func (p contextMenu) hover(t hitTarget) popup {
	p.cursor = t.index
	return p
}

func (p contextMenu) run(c *ctx) {
	c.closePopup()
	if p.cursor < len(p.entries) {
		c.runNamed(p.entries[p.cursor].name)
	}
}

func (p contextMenu) draw(c *ctx, scr uv.Screen, area uv.Rectangle) {
	sk := c.m.keymap().screenKeys(p.screen)
	keys := make([]string, len(p.entries))
	nameW, keysW := 0, 0
	for i, b := range p.entries {
		keys[i] = keyLabels(sk, b)
		nameW, keysW = max(nameW, len(b.name)), max(keysW, lipgloss.Width(keys[i]))
	}
	f := popupFrame{width: min(area.Dx(), nameW+2+keysW+popupFrameW)}
	var l itemLines
	for i, b := range p.entries {
		base := lipgloss.NewStyle()
		if i == p.cursor {
			base = StyleSecondary.Reverse(true)
		}
		row := base.Render(b.name+strings.Repeat(" ", nameW-len(b.name))) + "  " + StyleDim.Render(fmt.Sprintf("%-*s", keysW, keys[i]))
		l.add(i, xansi.Truncate(row, f.innerWidth(), "…"))
	}
	f.parts = l.window(f.bodyCtx(c), p.cursor, max(1, min(len(p.entries), area.Dy()-popupFrameH)))
	c.hitRect(drawAt(scr, area, p.at, f.render()))
}
