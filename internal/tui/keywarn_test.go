package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/config"
)

func TestWithKeymapsShowsTheFirstErrorAndACount(t *testing.T) {
	m := projectsTestModel().withKeymaps(config.TUIConfig{Keymaps: map[string]map[string]string{
		"project-tree": {"a": "nope", "b": "nope2", "c": "nope3"},
	}})
	if m.flash != `keymap: project-tree "a": unknown command "nope" (+2 more)` {
		t.Errorf("flash = %q", m.flash)
	}
	if m.keys == nil || m.keys.timeout <= 0 {
		t.Error("a zero timeout falls back to 1s")
	}
}

func TestNoKeymapsKeepsTheDefaults(t *testing.T) {
	m := projectsTestModel().withKeymaps(config.TUIConfig{})
	if m.flash != "" {
		t.Errorf("no keymaps must not produce a flash: %q", m.flash)
	}
	m = typeKeys(m, "z.")
	if !m.left.tree.showHidden {
		t.Error("z. still toggles hidden projects with an empty keymap config")
	}
}

func TestKittyWarningWithoutTheProtocol(t *testing.T) {
	m := projectsTestModel().withKeymaps(config.TUIConfig{Keymaps: map[string]map[string]string{"global": {"<C-S-b>": "toggle left-sidebar"}}})
	m, _ = upd(m, kittyCheckMsg{})
	if !strings.Contains(m.flash, "keymap: <C-S-b> needs a terminal with the Kitty keyboard protocol") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestKittyWarningForTheLeader(t *testing.T) {
	m := projectsTestModel().withKeymaps(config.TUIConfig{LeaderKey: "<C-S-x>"})
	m, _ = upd(m, kittyCheckMsg{})
	if !strings.Contains(m.flash, `keymap: tui.leader-key "<C-S-x>" needs a terminal with the Kitty keyboard protocol`) {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestNoKittyWarningWithTheProtocol(t *testing.T) {
	m := projectsTestModel().withKeymaps(config.TUIConfig{Keymaps: map[string]map[string]string{"global": {"<C-S-b>": "toggle left-sidebar"}}})
	m, _ = upd(m, tea.KeyboardEnhancementsMsg{Flags: 1})
	m, _ = upd(m, kittyCheckMsg{})
	if strings.Contains(m.flash, "Kitty") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestEmptyHomeFooterShowsTheWarningAndPendingKeys(t *testing.T) {
	m := homeTestModel()
	m.order = nil
	m = m.withKeymaps(config.TUIConfig{Keymaps: map[string]map[string]string{
		"home": {"a": "nope"}, "global": {"gr": "refresh"},
	}})
	if f := ansi.Strip(m.View().Content); !strings.Contains(f, `keymap: home "a": unknown command "nope"`) {
		t.Errorf("the empty home list should show the keymap warning:\n%s", f)
	}
	m.flash = ""
	m, _ = upd(m, keyMsg("g"))
	if f := ansi.Strip(m.View().Content); !strings.Contains(f, "g…") {
		t.Errorf("the empty home list should show the pending keys:\n%s", f)
	}
}
