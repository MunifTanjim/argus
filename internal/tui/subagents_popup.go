package tui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func runningLabel(n int) string {
	if n == 0 {
		return ""
	}
	return Icon.Subagent.Render() + " " + strconv.Itoa(n)
}

func (m model) runningSubagents() []session.RunningSubagent {
	return m.sessions[m.liveSessionID()].Subagents
}

func openSubagents(c *ctx) {
	if len(c.m.runningSubagents()) > 0 {
		c.openPopup(subagentsPopup{})
	}
}

// subagentsPopup reads the list from the live session rather than holding one,
// so it follows every update.
type subagentsPopup struct{ cursor int }

func (p subagentsPopup) id() string                            { return "subagents" }
func (p subagentsPopup) keySection() string                    { return "" }
func (p subagentsPopup) spins(*ctx) bool                       { return true } // the elapsed times tick
func (p subagentsPopup) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }

func (p subagentsPopup) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	n := len(c.m.runningSubagents())
	switch msg.String() {
	case "esc":
		c.closePopup()
	case "up", "k":
		p.cursor = cursorUp(p.cursor)
	case "down", "j":
		p.cursor = cursorDown(p.cursor, n)
	case "enter":
		return p, p.drill(c)
	}
	return p, nil
}

func (p subagentsPopup) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	if t.index == p.cursor {
		return p.handleKey(c, enterKey)
	}
	p.cursor = t.index
	return p, nil
}

func (p subagentsPopup) wheel(c *ctx, d int) (popup, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, len(c.m.runningSubagents()))
	return p, nil
}

// drill opens the picked subagent's trace in the live transcript, over any
// open file, with the transcript focused.
func (p subagentsPopup) drill(c *ctx) tea.Cmd {
	subs := c.m.runningSubagents()
	c.closePopup()
	i := c.m.transcriptAt(isLive)
	if p.cursor >= len(subs) || i < 0 {
		return nil
	}
	r := subs[p.cursor]
	c.closeFile()
	c.focusOn(mainPane)
	return c.m.editTranscript(i, func(v tview) tea.Cmd { return v.drillRunning(r) })
}

func (p subagentsPopup) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	subs := c.m.runningSubagents()
	f := popupFrame{width: min(area.Dx(), maxCardWidth), title: "Subagents"}
	f.help = c.m.hints(f.innerWidth(), hint("↑/↓", "move"), hint("enter", "open"), hint("esc", "cancel"))
	iw := f.innerWidth()
	cursor := min(p.cursor, len(subs)-1)
	l := itemLines{key: "subagents"}
	for i, r := range subs {
		name := r.Name
		if name == "" {
			name = r.Type
		}
		if name == "" {
			name = "Subagent"
		}
		text := name
		if r.Desc != "" {
			text += " · " + r.Desc
		}
		var elapsed string
		if t, err := time.Parse(time.RFC3339, r.Since); err == nil {
			elapsed = formatElapsed(clockNow().Sub(t))
		}
		room := iw
		if elapsed != "" {
			room -= lipgloss.Width(elapsed) + 1
		}
		text = xansi.Truncate(text, max(0, room), "…")
		st := lipgloss.NewStyle()
		if i == cursor {
			st = StyleSecondary.Reverse(true)
		}
		row := st.Render(text + strings.Repeat(" ", max(0, room-lipgloss.Width(text))))
		if elapsed != "" {
			row += " " + dimStyle.Render(elapsed)
		}
		l.add(i, row)
	}
	avail := max(1, min(len(subs), area.Dy()-popupFrameH-2*f.lines()))
	f.parts = l.window(f.bodyCtx(c), cursor, avail)
	c.hitRect(drawCenter(scr, area, f.render()))
}

// syncSubagents keeps an open subagents popup on the live session's list: it
// closes when none run, and its cursor stays on a row when one ends.
func (m *model) syncSubagents() {
	p, ok := m.popups.front().(subagentsPopup)
	if !ok {
		return
	}
	n := len(m.runningSubagents())
	if n == 0 {
		m.popups = m.popups.closeFront()
		return
	}
	p.cursor = min(p.cursor, n-1)
	m.popups = m.popups.replaceFront(p)
}
