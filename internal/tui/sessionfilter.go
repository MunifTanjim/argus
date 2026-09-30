package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// sessionMatches reports whether q, case-insensitively, is in a field that s's
// card shows.
func sessionMatches(s session.Session, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	fields := []string{s.Name, s.Repo, s.Branch, s.NodeLabel}
	if s.Summary != nil {
		fields = append(fields, s.Summary.Task)
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func (m *model) setSessionFilter(q string) {
	sel := m.rootSessionID()
	m.sessionFilter = strings.TrimSpace(q)
	m.refilter(sel)
}

// filterPrompt edits the session filter from a session list.
type filterPrompt struct {
	input textinput.Model
	on    bool
}

func (f filterPrompt) start(q string) (filterPrompt, tea.Cmd) {
	f.input = newProjectsInput()
	f.input.SetValue(q)
	f.on = true
	return f, f.input.Focus()
}

func (f filterPrompt) handleKey(c *ctx, msg tea.KeyPressMsg) (filterPrompt, tea.Cmd) {
	switch {
	case msg.Code == tea.KeyEscape:
		f.on = false
		c.setSessionFilter("")
		return f, nil
	case msg.String() == "enter":
		f.on = false
		return f, nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	c.setSessionFilter(f.input.Value())
	return f, cmd
}

func (f filterPrompt) view() string {
	return asstStyle.Render("filter: " + f.input.View() + "  enter keep · esc clear")
}

// sessionFilterTitle is the title suffix that names the session filters on.
func (m model) sessionFilterTitle() string {
	var s string
	if m.activeOnly {
		s += "  active"
	}
	if m.sessionFilter != "" {
		s += "  /" + m.sessionFilter
	}
	return dimStyle.Render(s)
}

// emptyFilterHint is the line a session list shows when its filters hide every
// session; back is the list's key that clears the text filter.
func (m model) emptyFilterHint(noActive string, back binding) string {
	if m.sessionFilter != "" {
		return "no sessions match /" + m.sessionFilter + " · " + m.keyText(back) + " clears"
	}
	return noActive + " · " + m.showsAllHint()
}
