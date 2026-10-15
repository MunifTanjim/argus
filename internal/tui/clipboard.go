package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// writeClipboard writes the system clipboard; tests replace it.
var writeClipboard = clipboard.WriteAll

// copyText copies text to the system clipboard and, through OSC52, to the
// terminal's, which reaches the local clipboard over SSH. The terminal never
// answers OSC52, so a failed native write is not reported. flash confirms the
// copy.
func copyText(c *ctx, text, flash string) tea.Cmd {
	if text == "" {
		c.setFlash("nothing to copy")
		return nil
	}
	c.setFlash(flash)
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg {
		_ = writeClipboard(text)
		return nil
	})
}
