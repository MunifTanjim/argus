package claudecode

import (
	"context"
)

// PaneController is the slice of *tmux.Client that PrepareTextInput needs. Kept
// as an interface so the input logic is unit-testable without a live tmux server.
type PaneController interface {
	PaneInMode(ctx context.Context, paneID string) (bool, error)
	CancelMode(ctx context.Context, paneID string) error
	SendKeys(ctx context.Context, paneID string, keys ...string) error
}

// PrepareTextInput makes a Claude Code pane ready to receive injected text:
//  1. it exits any tmux copy/view mode so keystrokes reach the program, and
//  2. if Claude's vim mode is on, it leaves the prompt in INSERT.
//
// Step 2 needs no configuration and reads nothing from the screen. It sends `i`
// then BSpace, which lands in INSERT from every starting state:
//   - vim NORMAL: `i` enters INSERT and types nothing; BSpace is a no-op on the
//     empty line. Ends in INSERT.
//   - vim INSERT: `i` is a literal char; BSpace erases it. Stays in INSERT.
//   - non-vim: `i` is a literal char; BSpace erases it.
//
// Every path ends with an empty prompt and, if vim is on, in INSERT. This
// assumes the prompt is empty (true for argus's discrete composer sends).
//
// We deliberately do not detect the vim INSERT indicator from a screen capture:
// Claude renders it as a "-- INSERT --" frame that wraps unpredictably by pane
// width (the dashes and the word land on different rows, interleaved with mode
// text), so no substring match is reliable. Sending `i`+BSpace unconditionally
// is correct regardless of how the prompt is rendered, at the cost of one
// redundant keystroke pair when already in INSERT.
//
// Copy-mode exit and send failures propagate.
func PrepareTextInput(ctx context.Context, pc PaneController, paneID string) error {
	inMode, err := pc.PaneInMode(ctx, paneID)
	if err != nil {
		return err
	}
	if inMode {
		if err := pc.CancelMode(ctx, paneID); err != nil {
			return err
		}
	}
	return pc.SendKeys(ctx, paneID, "i", "BSpace")
}
