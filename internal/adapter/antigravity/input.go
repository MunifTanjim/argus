package antigravity

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// PrepareTextInput readies an agy pane for injected text. The i+BSpace enters vim
// INSERT when vim mode is on and is a no-op otherwise.
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
