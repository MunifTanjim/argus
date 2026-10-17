package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
)

// runningKind is a list of work the live session runs now, counted on the
// dock's bottom rule.
type runningKind int

const (
	runningSubagents runningKind = iota
	runningShells
)

var runningKinds = []runningKind{runningSubagents, runningShells}

// runningRow is one running item; open is nil when it has nothing to open.
type runningRow struct {
	text, since string
	open        func(tview) tea.Cmd
}

func (k runningKind) title() string {
	if k == runningShells {
		return "Shells"
	}
	return "Subagents"
}

func (k runningKind) label(n int) string {
	if n == 0 {
		return ""
	}
	icon := Icon.Subagent
	if k == runningShells {
		icon = Icon.ShellRunning
	}
	return icon.Render() + " " + strconv.Itoa(n)
}

func (m model) runningCount(k runningKind) int {
	s := m.sessions[m.liveSessionID()]
	if k == runningShells {
		return len(s.Shells)
	}
	return len(s.Subagents)
}

func (m model) runningRows(k runningKind) []runningRow {
	s := m.sessions[m.liveSessionID()]
	var rows []runningRow
	if k == runningShells {
		for _, r := range s.Shells {
			text := strings.Join(slices.DeleteFunc([]string{r.Desc, r.Command}, func(s string) bool { return s == "" }), " · ")
			if text == "" {
				text = "Shell"
			}
			row := runningRow{text: text, since: r.Since}
			if r.ToolUseID != "" {
				row.open = func(v tview) tea.Cmd { return v.drillShell(r) }
			}
			rows = append(rows, row)
		}
		return rows
	}
	for _, r := range s.Subagents {
		name := cmp.Or(r.Name, r.Type, "Subagent")
		if r.Desc != "" {
			name += " · " + r.Desc
		}
		rows = append(rows, runningRow{text: name, since: r.Since, open: func(v tview) tea.Cmd { return v.drillRunning(r) }})
	}
	return rows
}

func openRunning(c *ctx, k runningKind) {
	if c.m.runningCount(k) > 0 {
		c.openPopup(runningPopup{kind: k})
	}
}

// runningPopup reads its rows from the live session rather than holding them,
// so it follows every update.
type runningPopup struct {
	kind   runningKind
	cursor int
}

func (p runningPopup) id() string                            { return strings.ToLower(p.kind.title()) }
func (p runningPopup) keySection() string                    { return "" }
func (p runningPopup) spins(*ctx) bool                       { return true } // the elapsed times tick
func (p runningPopup) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }

func (p runningPopup) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.closePopup()
	case "up", "k":
		p.cursor = cursorUp(p.cursor)
	case "down", "j":
		p.cursor = cursorDown(p.cursor, c.m.runningCount(p.kind))
	case "enter":
		return p, p.drill(c)
	}
	return p, nil
}

func (p runningPopup) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	if t.index == p.cursor {
		return p.handleKey(c, enterKey)
	}
	p.cursor = t.index
	return p, nil
}

func (p runningPopup) wheel(c *ctx, d int) (popup, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, c.m.runningCount(p.kind))
	return p, nil
}

// drill opens the picked row in the live transcript, over any open file, with
// the transcript focused. A row with nothing to open keeps the popup up.
func (p runningPopup) drill(c *ctx) tea.Cmd {
	rows := c.m.runningRows(p.kind)
	if p.cursor < len(rows) && rows[p.cursor].open == nil {
		return nil
	}
	c.closePopup()
	i := c.m.transcriptAt(isLive)
	if p.cursor >= len(rows) || i < 0 {
		return nil
	}
	c.closeFile()
	c.focusOn(mainPane)
	return c.m.editTranscript(i, rows[p.cursor].open)
}

func (p runningPopup) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	rows := c.m.runningRows(p.kind)
	f := popupFrame{width: min(area.Dx(), maxCardWidth), title: p.kind.title()}
	f.help = c.m.hints(f.innerWidth(), hint("↑/↓", "move"), hint("enter", "open"), hint("esc", "cancel"))
	iw := f.innerWidth()
	cursor := min(p.cursor, len(rows)-1)
	l := itemLines{key: p.id()}
	for i, r := range rows {
		var elapsed string
		if t, err := time.Parse(time.RFC3339, r.since); err == nil {
			elapsed = formatElapsed(clockNow().Sub(t))
		}
		room := iw
		if elapsed != "" {
			room -= lipgloss.Width(elapsed) + 1
		}
		text := xansi.Truncate(r.text, max(0, room), "…")
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
	avail := max(1, min(len(rows), area.Dy()-popupFrameH-2*f.lines()))
	f.parts = l.window(f.bodyCtx(c), cursor, avail)
	c.hitRect(drawCenter(scr, area, f.render()))
}

// syncRunning keeps an open running popup on the live session's list: it
// closes when none run, and its cursor stays on a row when one ends.
func (m *model) syncRunning() {
	p, ok := m.popups.front().(runningPopup)
	if !ok {
		return
	}
	n := m.runningCount(p.kind)
	if n == 0 {
		m.popups = m.popups.closeFront()
		return
	}
	p.cursor = min(p.cursor, n-1)
	m.popups = m.popups.replaceFront(p)
}
