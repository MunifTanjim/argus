package opencode

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
)

func prepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	inMode, err := pc.PaneInMode(ctx, paneID)
	if err != nil {
		return err
	}
	if inMode {
		return pc.CancelMode(ctx, paneID)
	}
	return nil
}
