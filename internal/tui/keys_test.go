package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestEveryDefaultParses(t *testing.T) {
	for _, b := range bindingsOf(append(allBindingSets(), screenLeave)...) {
		for _, tok := range strings.Fields(b.defaults) {
			if _, err := parseKeySeq(tok); err != nil {
				t.Errorf("%s: default %q: %v", b.name, tok, err)
			}
		}
	}
}

func TestDefaultKeymapServesModelsWithoutKeymaps(t *testing.T) {
	m := projectsTestModel()
	if m.keys != nil || m.keymap() != defaultKeymap {
		t.Fatal("a model with no keymap uses defaultKeymap")
	}
	if !m.matches(seqKey("z."), projectsKeys.ShowHidden) {
		t.Error("z. still toggles hidden projects through the default keymap")
	}
}

func TestResolverRunsWithoutUserKeymaps(t *testing.T) {
	m := projectsTestModel()
	m, _ = upd(m, keyMsg("j"))
	if len(m.keyBuf) != 0 {
		t.Error("a single default key runs at once")
	}
}

func TestEveryBindingHasACommandName(t *testing.T) {
	for _, set := range allBindingSets() {
		for _, b := range bindingsOf(set) {
			if b.name == "" {
				t.Errorf("binding %q has no command name", b.defaults)
			}
		}
	}
}

func TestFileViewAndRedactListHaveTheirOwnKeys(t *testing.T) {
	if fileViewKeys.Up.name != "scroll up" || fileViewKeys.Wrap.name != "toggle line-wrap" {
		t.Error("the file view scrolls with its own bindings")
	}
	if redactListKeys.Remove.name != "redaction remove" {
		t.Error("the redaction list removes with its own binding")
	}
}

// The home tabs route via the list dispatch table: gt switches to the History
// tab, while gT (the leftmost tab) stays on Sessions.
func TestListDispatchRoutesToAction(t *testing.T) {
	m := testModel() // modeList (Sessions tab) by default
	got, cmd := typeKeysCmd(m, "gt")
	if got.mode != modeHistoryProjects {
		t.Fatalf("gt should open the History tab, got mode %v", got.mode)
	}
	if cmd == nil {
		t.Error("opening history should kick off a fetch command")
	}

	if got := typeKeys(testModel(), "gT"); got.mode != modeList {
		t.Fatalf("gT on Sessions should stay on the list, got mode %v", got.mode)
	}
}

func TestHistoryTabPrevReturnsToSessions(t *testing.T) {
	m := testModel()
	m.mode = modeHistoryProjects
	if got := typeKeys(m, "gT"); got.mode != modeList {
		t.Fatalf("gT on History should return to Sessions, got mode %v", got.mode)
	}
}

func TestScreenNames(t *testing.T) {
	cases := []struct {
		mode   viewMode
		detail bool
		want   string
	}{
		{modeList, false, "home"},
		{modeProjects, false, "projects"},
		{modeSession, false, "session"},
		{modeSession, true, "detail"},
		{modeHistoryTranscript, false, "transcript"},
		{modeHistoryTranscript, true, "detail"},
		{modeHistoryProjects, false, "history"},
		{modeHistorySessions, false, "history"},
		{modeLogs, false, "logs"},
		{modeScreen, false, ""},
	}
	for _, c := range cases {
		m := model{mode: c.mode}
		if c.detail {
			m.historyView = histDetail
		}
		if got := m.screen(); got != c.want {
			t.Errorf("mode %d detail %v: screen %q, want %q", c.mode, c.detail, got, c.want)
		}
	}
}

func TestEveryNamedBindingIsOnAScreen(t *testing.T) {
	for _, b := range bindingsOf(allBindingSets()...) {
		found := false
		for s := range screenBindings {
			found = found || screenHas(s, b.name)
		}
		if !found {
			t.Errorf("command %q is on no screen", b.name)
		}
	}
}

// Footers are derived from the same bindings used for dispatch, so the help text
// stays in sync with the keys.
func TestFooterDerivesFromBindings(t *testing.T) {
	m := testModel()
	foot := ansi.Strip(m.footer(listKeys.TabNext, listKeys.Quit))
	for _, want := range []string{"gT/gt", "tabs", "Q", "quit"} {
		if !strings.Contains(foot, want) {
			t.Errorf("footer %q should contain %q", foot, want)
		}
	}
	// An empty-help binding (dispatch-only) contributes nothing to the footer.
	if got := ansi.Strip(m.footer(listKeys.Down)); strings.TrimSpace(got) != "" {
		t.Errorf("empty-help binding should render no footer text, got %q", got)
	}
}
