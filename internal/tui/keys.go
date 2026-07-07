package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Single source of truth for app keybindings: each binding carries both its keys
// (for key.Matches dispatch) and its help text (for footers), so the two can't
// drift. Live screen passthrough keys are not here — they are forwarded directly
// to the PTY, not dispatched as app actions.
//
// Help labels are display-only. Paired actions (up/down, g/G) put the combined
// label on one binding and leave the other's help empty, so the footer shows one entry.

// nb builds a binding from its keys plus a display label and description.
func nb(keys []string, label, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(label, desc))
}

// keyAction applies a matched key to the model. Method expressions (e.g.
// model.actOpen) satisfy this, so tables can name model methods directly.
type keyAction = func(m model, msg tea.KeyPressMsg) (tea.Model, tea.Cmd)

// keyTableEntry pairs a binding with the action it triggers.
type keyTableEntry struct {
	b   key.Binding
	act keyAction
}

// dispatch runs the first table entry whose binding matches msg. ok is false when
// nothing matched (the caller falls back, e.g. to text input).
func (m model) dispatch(msg tea.KeyPressMsg, table []keyTableEntry) (tea.Model, tea.Cmd, bool) {
	for _, e := range table {
		if key.Matches(msg, e.b) {
			mm, cmd := e.act(m, msg)
			return mm, cmd, true
		}
	}
	return m, nil, false
}

// --- binding sets -------------------------------------------------------------

var listKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown                        key.Binding
	Open, Screen, Jump, TabPrev, TabNext, New, Kill, Refresh, Quit key.Binding
}{
	Up:       nb([]string{"up", "k"}, "↑/↓", "move"),
	Down:     nb([]string{"down", "j"}, "", ""),
	Top:      nb([]string{"g"}, "", ""),
	Bottom:   nb([]string{"G"}, "g/G", "ends"),
	HalfUp:   nb([]string{"ctrl+u", "pgup"}, "", ""),
	HalfDown: nb([]string{"ctrl+d", "pgdown"}, "", ""),
	Open:     nb([]string{"enter"}, "enter", "open"),
	Screen:   nb([]string{"s"}, "s", "screen"),
	Jump:     nb([]string{"O"}, "O", "jump"),
	TabPrev:  nb([]string{"left", "h"}, "", ""),
	TabNext:  nb([]string{"right", "l"}, "h/l", "tabs"),
	New:      nb([]string{"n"}, "n", "new"),
	Kill:     nb([]string{"x"}, "x", "kill"),
	Refresh:  nb([]string{"r"}, "r", "refresh"),
	Quit:     nb([]string{"q"}, "q", "quit"),
}

var transcriptKeys = struct {
	ScrollUp, ScrollDown, TurnNext, TurnPrev, CardNext, CardPrev, HalfUp, HalfDown key.Binding
	Top, Bottom, Fold, Detail, ExpandAll, CollapseAll                              key.Binding
	Raw, Answer, Back                                                              key.Binding
}{
	ScrollUp:    nb([]string{"up"}, "↑/↓", "scroll"),
	ScrollDown:  nb([]string{"down"}, "", ""),
	TurnNext:    nb([]string{"j"}, "j/k", "turn"),
	TurnPrev:    nb([]string{"k"}, "", ""),
	CardNext:    nb([]string{"J"}, "", ""), // force jump past an oversized card
	CardPrev:    nb([]string{"K"}, "", ""),
	HalfUp:      nb([]string{"ctrl+u", "pgup"}, "", ""),
	HalfDown:    nb([]string{"ctrl+d", "pgdown"}, "", ""),
	Top:         nb([]string{"g"}, "", ""),
	Bottom:      nb([]string{"G"}, "g/G", "ends"),
	Fold:        nb([]string{" ", "space"}, "space", "fold"),
	Detail:      nb([]string{"enter"}, "enter", "detail"),
	ExpandAll:   nb([]string{"o"}, "", ""),
	CollapseAll: nb([]string{"O"}, "", ""),
	Raw:         nb([]string{"ctrl+s"}, "ctrl+s", "raw"),
	Answer:      nb([]string{"tab"}, "tab", "answer"),
	Back:        nb([]string{"esc", "escape", "q"}, "esc", "back"),
}

var detailKeys = struct {
	Up, Down, HalfUp, HalfDown, Top, Bottom, Fold, Drill, Raw, Back key.Binding
}{
	Up:       nb([]string{"up", "k"}, "↑/↓", "move"),
	Down:     nb([]string{"down", "j"}, "", ""),
	HalfUp:   nb([]string{"ctrl+u", "pgup"}, "", ""),
	HalfDown: nb([]string{"ctrl+d", "pgdown"}, "", ""),
	Top:      nb([]string{"g"}, "", ""),
	Bottom:   nb([]string{"G"}, "", ""),
	Fold:     nb([]string{" ", "space"}, "space", "expand"),
	Drill:    nb([]string{"enter"}, "enter", "drill"),
	Raw:      nb([]string{"ctrl+s"}, "ctrl+s", "raw"),
	Back:     nb([]string{"esc", "escape"}, "esc", "back"),
}

// sessionKeys are the composite-screen keys handled before the focused region:
// focus toggle and the raw-screen switch.
var sessionKeys = struct {
	Focus, Raw key.Binding
}{
	Focus: nb([]string{"tab"}, "tab", "answer"),
	Raw:   nb([]string{"ctrl+s"}, "ctrl+s", "raw"),
}

// Prompt bindings (dock): drive dock footers; the prompt sub-views are modal text editors.
var promptKeys = struct {
	Up, Down, HalfUp, HalfDown, TabPrev, TabNext, Submit, Next, Toggle, Read key.Binding
}{
	Up:       nb([]string{"up", "ctrl+p"}, "↑/↓", "select"),
	Down:     nb([]string{"down", "ctrl+n"}, "", ""),
	HalfUp:   nb([]string{"ctrl+u", "pgup"}, "ctrl+u/ctrl+d", "scroll"), // combined label; HalfDown's stays empty
	HalfDown: nb([]string{"ctrl+d", "pgdown"}, "", ""),
	TabPrev:  nb([]string{"left"}, "←/→", "tabs"),
	TabNext:  nb([]string{"right"}, "", ""),
	Submit:   nb([]string{"enter"}, "enter", "submit"),
	Next:     nb([]string{"enter"}, "enter", "next"), // footer label for multi-question advance
	Toggle:   nb([]string{" ", "space"}, "space", "toggle"),
	Read:     nb([]string{"tab", "esc", "escape"}, "tab/esc", "read"),
}

var historyProjectsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, Open, Refresh, Back key.Binding
}{
	Up:       nb([]string{"up", "k"}, "↑/↓", "move"),
	Down:     nb([]string{"down", "j"}, "", ""),
	Top:      nb([]string{"g"}, "", ""),
	Bottom:   nb([]string{"G"}, "g/G", "ends"),
	HalfUp:   nb([]string{"ctrl+u", "pgup"}, "", ""),
	HalfDown: nb([]string{"ctrl+d", "pgdown"}, "", ""),
	Open:     nb([]string{"enter"}, "enter", "open"),
	Refresh:  nb([]string{"r"}, "r", "refresh"),
	Back:     nb([]string{"esc", "escape", "q"}, "esc", "back"),
}

var historySessionsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, Open, More, Back key.Binding
}{
	Up:       nb([]string{"up", "k"}, "↑/↓", "move"),
	Down:     nb([]string{"down", "j"}, "", ""),
	Top:      nb([]string{"g"}, "", ""),
	Bottom:   nb([]string{"G"}, "g/G", "ends"),
	HalfUp:   nb([]string{"ctrl+u", "pgup"}, "", ""),
	HalfDown: nb([]string{"ctrl+d", "pgdown"}, "", ""),
	Open:     nb([]string{"enter"}, "enter", "open"),
	More:     nb([]string{"m"}, "m", "more"),
	Back:     nb([]string{"esc", "escape", "q"}, "esc", "back"),
}

var logsKeys = struct {
	Up, Down, HalfUp, HalfDown, Top, Bottom, Back key.Binding
}{
	Up:       nb([]string{"up", "k"}, "↑/↓", "scroll"),
	Down:     nb([]string{"down", "j"}, "", ""),
	HalfUp:   nb([]string{"ctrl+u", "pgup"}, "", ""),
	HalfDown: nb([]string{"ctrl+d", "pgdown"}, "", ""),
	Top:      nb([]string{"g"}, "", ""),
	Bottom:   nb([]string{"G"}, "g/G", "ends"),
	Back:     nb([]string{"esc", "escape", "q"}, "esc", "back"),
}

// screenLeave is the only app binding in live-screen passthrough; every other key
// is forwarded to the pane.
var screenLeave = nb([]string{"ctrl+]"}, "ctrl+]", "leave")

// --- footers ------------------------------------------------------------------

// footer renders a one-line help view from the given bindings (empty-help ones are
// skipped). Built per call so it needs no model state and works in tests with a
// model literal.
func (m model) footer(bindings ...key.Binding) string {
	h := help.New()
	h.Styles = help.DefaultStyles(m.hasDark)
	h.Styles.ShortKey = StyleSecondary
	h.Styles.ShortDesc = StyleDim
	h.Styles.ShortSeparator = StyleDim
	h.ShortSeparator = " · "
	w := m.width
	if w <= 0 {
		w = 200 // no viewport yet (e.g. tests): don't truncate
	}
	h.SetWidth(w)
	return h.ShortHelpView(bindings)
}
