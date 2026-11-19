package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

var actions = map[string]binding{
	"client.pane.focus.left":     paneKeys.Left,
	"client.pane.focus.down":     paneKeys.Down,
	"client.pane.focus.up":       paneKeys.Up,
	"client.pane.focus.right":    paneKeys.Right,
	"client.pane.focus_next":     paneKeys.Next,
	"client.pane.focus_previous": paneKeys.Prev,
}

type actionResult struct {
	ok bool
}

func (m model) invokeAction(name string) (model, tea.Cmd, actionResult, error) {
	b, ok := actions[name]
	if !ok {
		return m, nil, actionResult{}, fmt.Errorf("unknown action %q", name)
	}
	if !screenHas(m.screen(), b.name) {
		return m, nil, actionResult{}, fmt.Errorf("%s is not available on %s", name, m.screen())
	}
	mm, cmd := m.runKey(cmdMsg(b.name))
	after := mm.(model).repairFocus()
	return after, cmd, actionResult{ok: after.focused != m.focused}, nil
}
