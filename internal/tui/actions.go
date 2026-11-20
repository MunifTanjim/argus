package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// builtinAction runs a command. changed is the ok that argus.invoke returns,
// from the model before and after the command.
type builtinAction struct {
	binding
	changed func(before, after model) bool
}

var actions = map[string]builtinAction{
	"client.pane.focus.left":     {paneKeys.Left, focusChanged},
	"client.pane.focus.down":     {paneKeys.Down, focusChanged},
	"client.pane.focus.up":       {paneKeys.Up, focusChanged},
	"client.pane.focus.right":    {paneKeys.Right, focusChanged},
	"client.pane.focus_next":     {paneKeys.Next, focusChanged},
	"client.pane.focus_previous": {paneKeys.Prev, focusChanged},
	"client.pane.tab.next":       {listKeys.TabNext, tabChanged},
	"client.pane.tab.previous":   {listKeys.TabPrev, tabChanged},
}

func focusChanged(before, after model) bool {
	return after.focused != before.focused
}

func tabChanged(before, after model) bool {
	return tabState(after) != tabState(before)
}

// tabbed is a component that switches tabs in place.
type tabbed interface{ tabIndex() int }

// tabState identifies the tab shown in the focused pane. A tab that is its own
// component, as on Home or in a sidebar, shows as a change of type.
func tabState(m model) string {
	c := m.focusedComp()
	if t, ok := c.(tabbed); ok {
		return fmt.Sprintf("%T/%d", c, t.tabIndex())
	}
	return fmt.Sprintf("%T", c)
}

func (m model) invokeAction(name string) (model, tea.Cmd, error) {
	a, ok := actions[name]
	if !ok {
		return m, nil, fmt.Errorf("unknown action %q", name)
	}
	if !screenHas(m.screen(), a.name) {
		return m, nil, fmt.Errorf("%s is not available on %s", name, m.screen())
	}
	mm, cmd := m.runKey(cmdMsg(a.name))
	return mm.(model).repairFocus(), cmd, nil
}
