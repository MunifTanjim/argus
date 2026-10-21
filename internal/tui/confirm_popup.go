package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
)

// confirmPopup asks before an action. Its first choice, action, runs accept;
// the second cancels. body shows above the choices, wrapped so that none of it
// is cut.
type confirmPopup struct {
	popupID               string
	title, action, detail string
	body                  []string
	accept                func(*ctx) tea.Cmd
	cursor                int
}

func (p confirmPopup) choices() []string { return []string{p.action, "Cancel"} }

func (p confirmPopup) id() string                            { return p.popupID }
func (p confirmPopup) keySection() string                    { return "" }
func (p confirmPopup) spins(*ctx) bool                       { return false }
func (p confirmPopup) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }

func (p confirmPopup) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.closePopup()
	case "up", "k":
		p.cursor = cursorUp(p.cursor)
	case "down", "j":
		p.cursor = cursorDown(p.cursor, len(p.choices()))
	case "enter":
		c.closePopup()
		if p.cursor == 0 {
			return p, p.accept(c)
		}
	}
	return p, nil
}

func (p confirmPopup) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	p.cursor = t.index
	return p.handleKey(c, enterKey)
}

func (p confirmPopup) wheel(_ *ctx, d int) (popup, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, len(p.choices()))
	return p, nil
}

func (p confirmPopup) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	f := pickerFrame(area)
	f.title = p.title
	f.help = c.m.hints(f.innerWidth(), hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "cancel"))
	var head []string
	for _, b := range p.body {
		head = append(head, strings.Split(xansi.Wrap(b, f.innerWidth(), ""), "\n")...)
	}
	if len(head) > 0 {
		head = append(head, "")
	}
	l := itemLines{key: p.popupID}
	for i, ch := range p.choices() {
		sub := ""
		if i == 0 {
			sub = p.detail
		}
		l.add(i, spawnChoiceRow(ch, sub, i == p.cursor, f.innerWidth()))
	}
	f.parts = append(head, l.window(f.bodyCtx(c).below(len(head)), p.cursor, max(1, f.bodyHeight()-len(head)))...)
	c.hitRect(drawCenter(scr, area, f.render()))
}
