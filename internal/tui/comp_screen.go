package tui

import (
	"encoding/base64"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/vt"

	"github.com/MunifTanjim/argus/internal/api"
)

// screenComp is the live screen. It takes every key as typed; only the leave
// key pops it.
type screenComp struct {
	sessionID string
	term      *vt.Emulator
	termID    string        // unique per attach; the gateway and node key on it
	stop      chan struct{} // closed to stop the emulator drain goroutine
	err       error         // terminal.open failure, shown in the box
	// gone marks an attach that already ended on the node, so close does not
	// ask the node to close it.
	gone bool
}

func (s screenComp) section() string           { return "" }
func (s screenComp) raw(*ctx) bool             { return true }
func (s screenComp) spins(*ctx) bool           { return false }
func (s screenComp) fullScreen(*ctx) fullLevel { return notFull }
func (s screenComp) footer(*ctx) []binding     { return []binding{screenLeave} }
func (s screenComp) offers(*ctx) []binding     { return nil }
func (s screenComp) commands(*ctx) []binding   { return nil }
func (s screenComp) pageStep(c *ctx) int       { return c.m.listPageStep() }
func (s screenComp) layer() layer              { return screenLayer }

func (s screenComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	if isScreenLeave(msg) {
		c.back()
		return s, nil, true
	}
	if b := ptyBytesFor(msg); b != nil {
		c.m.sendTermKey(s.termID, b)
	}
	return s, nil, true
}

func (s screenComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if s.term == nil {
			return s, nil
		}
		cols, rows := c.m.termDims()
		s.term.Resize(cols, rows)
		return s, c.m.termResizeCmd(s.termID, cols, rows)
	case tea.PasteMsg:
		c.m.sendTermKey(s.termID, []byte(msg.Content))
	case termOpenedMsg:
		if msg.err != nil {
			s.err = msg.err
		}
	case api.TerminalOutput:
		if s.term == nil {
			return s, nil
		}
		if raw, err := base64.StdEncoding.DecodeString(msg.Data); err == nil {
			_, _ = s.term.Write(raw)
		}
	case api.TerminalExited:
		s.gone = true
		c.closeScreen(s.termID)
		if msg.Reason == api.TermExitedEvicted {
			c.setFlash("terminal opened elsewhere")
		} else {
			c.setFlash("terminal exited")
		}
	case connStateMsg:
		s.gone = true
		c.closeScreen(s.termID)
		c.setFlash("terminal detached")
	}
	return s, nil
}

func (s screenComp) close(c *ctx) tea.Cmd {
	if s.stop != nil {
		close(s.stop)
		_, _ = s.term.InputPipe().Write([]byte{0}) // wake drainEmulator's Read so it observes stop
	}
	if s.gone {
		return nil
	}
	return c.m.termCloseCmd(s.termID)
}

func (s screenComp) view(c *ctx, w, h int) string {
	m := c.m
	ss := m.sessions[s.sessionID]
	var b strings.Builder
	b.WriteString(headerStyle.Render(ss.Tmux.SessionName) +
		dimStyle.Render(fmt.Sprintf("  [%s] %s", paneTag(ss), statusWord(ss))) + "\n\n")

	var body string
	switch {
	case s.err != nil:
		body = dimStyle.Render("terminal unavailable: " + s.err.Error())
	case s.term != nil:
		body = s.term.Render()
	}
	cols, visible := termDimsFor(w, h)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) > visible {
		lines = lines[len(lines)-visible:]
	}
	// Lines carry SGR escapes; clip to the interior width and reset so colors
	// don't bleed.
	for i, line := range lines {
		line = truncateLine(line, cols)
		if s.err == nil {
			line += "\x1b[0m"
		}
		lines[i] = line
	}
	// lipgloss Width counts the border, so pass cols+2 to keep the interior at cols
	// (Width(cols) would give a cols-2 interior and wrap every row).
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Width(cols + 2).
		Render(strings.Join(lines, "\n"))
	b.WriteString(box)

	return b.String()
}

func (s screenComp) footerPrompt(c *ctx) string {
	return dimStyle.Render("keys go to the session · ") + c.m.footer(s.footer(c)...)
}

func (m model) liveScreen() (screenComp, bool) {
	s, ok := m.main.top().(screenComp)
	return s, ok
}

func (m model) screenAt(termID string) int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if s, ok := m.main[i].(screenComp); ok && s.termID == termID {
			return i
		}
	}
	return -1
}

func (m model) updateScreen(i int, msg tea.Msg) (model, tea.Cmd) {
	if i < 0 {
		return m, nil
	}
	c := &ctx{m: &m}
	comp, cmd := m.main[i].update(c, msg)
	m.main = m.main.replaceAt(i, comp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

// updateScreens goes top first, so a screen that closes does not move the ones
// under it.
func (m model) updateScreens(msg tea.Msg) (model, tea.Cmd) {
	var cmds []tea.Cmd
	for i := len(m.main) - 1; i >= 0; i-- {
		if _, ok := m.main[i].(screenComp); ok {
			var cmd tea.Cmd
			m, cmd = m.updateScreen(i, msg)
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m model) topScreen() int {
	if _, ok := m.liveScreen(); ok {
		return len(m.main) - 1
	}
	return -1
}

func (m model) handleScreenKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	i := m.topScreen()
	if i < 0 {
		return m, nil
	}
	c := &ctx{m: &m}
	comp, cmd, _ := m.main[i].handleKey(c, msg)
	m.main = m.main.replaceAt(i, comp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

// msg.String() is unreliable for ctrl+] (it prioritizes Text), so match on
// Code+Mod and the raw 0x1d control byte instead.
func isScreenLeave(msg tea.KeyPressMsg) bool {
	if msg.Code == ']' && msg.Mod&tea.ModCtrl != 0 {
		return true
	}
	return msg.Code == 0x1d // GS: ctrl+] as a raw control byte
}

func (s screenComp) workspace(c *ctx) string { return c.m.sessions[s.sessionID].WorkspaceID }
