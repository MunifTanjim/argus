package claudecode

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// PaneController is the tmux-pane control surface PrepareTextInput needs. It
// lives in the adapter package (as an interface-method param); aliased here to
// keep the historical claudecode.PaneController spelling working.
type PaneController = adapter.PaneController

// PrepareTextInput readies a Claude Code pane for injected text: it exits any
// tmux copy/view mode so keystrokes reach the program, then sends `i`+BSpace to
// land in vim INSERT (if vim mode is on) from any starting state without erasing
// anything — assumes an empty prompt (true for argus's composer sends).
//
// Screen detection of the INSERT indicator is deliberately avoided: Claude's
// "-- INSERT --" frame wraps unpredictably by pane width, so no substring match
// is reliable. `i`+BSpace is correct regardless, at the cost of one redundant
// keystroke pair when already in INSERT.
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
