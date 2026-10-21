package opencode

import "context"

// renameSession sets an OpenCode session's title. The service reports the
// change back as a session.updated event.
func (d *discoverer) renameSession(ctx context.Context, id, title string) error {
	c, err := d.liveClient()
	if err != nil {
		return err
	}
	return c.updateTitle(ctx, id, title)
}
