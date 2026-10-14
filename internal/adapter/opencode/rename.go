package opencode

import (
	"context"
	"fmt"
)

// renameSession sets an OpenCode session's title. The service reports the
// change back as a session.updated event.
func (d *discoverer) renameSession(ctx context.Context, id, title string) error {
	c, ok := d.dial()
	if !ok {
		return fmt.Errorf("opencode: service unavailable")
	}
	return c.updateTitle(ctx, id, title)
}
