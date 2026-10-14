package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// renamePrompt edits a new name for the item id; cur is its current name.
type renamePrompt struct {
	id    string
	cur   string
	input textinput.Model
}

func (r renamePrompt) on() bool { return r.id != "" }

func (r renamePrompt) start(id, cur string) (renamePrompt, tea.Cmd) {
	r.id, r.cur = id, cur
	r.input = newProjectsInput()
	r.input.SetValue(cur)
	return r, r.input.Focus()
}

// handleKey edits the prompt and returns the item and its trimmed new name
// once enter submits a non-empty, changed one.
func (r renamePrompt) handleKey(msg tea.KeyPressMsg) (renamePrompt, tea.Cmd, string, string) {
	switch {
	case msg.Code == tea.KeyEscape:
		r.id = ""
		return r, nil, "", ""
	case msg.String() == "enter":
		id, name := r.id, strings.TrimSpace(r.input.Value())
		r.id = ""
		if name == "" || name == r.cur {
			return r, nil, "", ""
		}
		return r, nil, id, name
	}
	var cmd tea.Cmd
	r.input, cmd = r.input.Update(msg)
	return r, cmd, "", ""
}

func (r renamePrompt) view() string {
	return asstStyle.Render("rename: " + r.input.View() + "  enter rename · esc cancel")
}
