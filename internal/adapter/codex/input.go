package codex

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// The i+BSpace pair enters vim INSERT when Codex's vim mode is on
// (self-cancelling no-op otherwise). Assumes an empty prompt.
func PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
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
