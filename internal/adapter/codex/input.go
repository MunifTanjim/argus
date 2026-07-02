package codex

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// PrepareTextInput readies a Codex pane for injected text: it exits any tmux
// copy/view mode so keystrokes reach the program. Unlike Claude Code, Codex is
// not a vim-mode composer, so no INSERT-mode keystroke dance is sent.
//
// NOTE (validate against a real Codex): confirm plain text + Enter lands in
// Codex's composer from a fresh prompt; tune here if its input needs priming.
func PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	inMode, err := pc.PaneInMode(ctx, paneID)
	if err != nil {
		return err
	}
	if inMode {
		return pc.CancelMode(ctx, paneID)
	}
	return nil
}
