package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/config"
)

type kittyCheckMsg struct{}

func (m model) withKeymaps(cfg config.TUIConfig) model {
	km, errs := buildKeymap(cfg.Keymaps, cfg.KeyTimeout, cfg.LeaderKey)
	m.keys = km
	if len(errs) > 0 {
		m.flash = errs[0]
		if len(errs) > 1 {
			m.flash += fmt.Sprintf(" (+%d more)", len(errs)-1)
		}
	}
	return m
}

// kittyCheckCmd gives the terminal a second to report the Kitty keyboard
// protocol before a mapping that needs it is flagged.
func (m model) kittyCheckCmd() tea.Cmd {
	if m.keys == nil || m.keys.kittyKey == "" {
		return nil
	}
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return kittyCheckMsg{} })
}

func (m model) kittyCheck() model {
	if m.kittyKeys || m.keys == nil || m.keys.kittyKey == "" {
		return m
	}
	warn := fmt.Sprintf("keymap: %s needs a terminal with the Kitty keyboard protocol", m.keys.kittyKey)
	if m.flash != "" {
		warn = m.flash + " · " + warn
	}
	m.flash = warn
	return m
}
