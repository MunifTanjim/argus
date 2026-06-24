package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The home tabs route via the list dispatch table: l/→ switches to the History
// tab, while h/← (the leftmost tab) stays on Sessions.
func TestListDispatchRoutesToAction(t *testing.T) {
	m := testModel() // modeList (Sessions tab) by default
	res, cmd := m.handleKey(tea.KeyPressMsg{Code: 'l'})
	got := res.(model)
	if got.mode != modeHistoryProjects {
		t.Fatalf("l should open the History tab, got mode %v", got.mode)
	}
	if cmd == nil {
		t.Error("opening history should kick off a fetch command")
	}

	// h on the Sessions tab is the no-op leftward move (Sessions is leftmost).
	stay, _ := testModel().handleKey(tea.KeyPressMsg{Code: 'h'})
	if got := stay.(model); got.mode != modeList {
		t.Fatalf("h on Sessions should stay on the list, got mode %v", got.mode)
	}
}

// Pressing h/← on the History tab returns to the Sessions tab.
func TestHistoryTabPrevReturnsToSessions(t *testing.T) {
	m := testModel()
	m.mode = modeHistoryProjects
	res, _ := m.handleKey(tea.KeyPressMsg{Code: 'h'})
	if got := res.(model); got.mode != modeList {
		t.Fatalf("h on History should return to Sessions, got mode %v", got.mode)
	}
}

// Footers are derived from the same bindings used for dispatch, so the help text
// stays in sync with the keys.
func TestFooterDerivesFromBindings(t *testing.T) {
	m := testModel()
	foot := ansi.Strip(m.footer(listKeys.TabNext, listKeys.Quit))
	for _, want := range []string{"h/l", "tabs", "q", "quit"} {
		if !strings.Contains(foot, want) {
			t.Errorf("footer %q should contain %q", foot, want)
		}
	}
	// An empty-help binding (dispatch-only) contributes nothing to the footer.
	if got := ansi.Strip(m.footer(listKeys.Down)); strings.TrimSpace(got) != "" {
		t.Errorf("empty-help binding should render no footer text, got %q", got)
	}
}
