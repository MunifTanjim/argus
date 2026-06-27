package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
)

// handleSessionFocus brings the user's tmux client to the pane of the session
// that needs attention. It is invoked by `argus _focus <id>` on a notification
// click. The id may be a composite "<nodeID>:<localID>" (gateway-broadcast
// notifications) or a bare local id (standalone). Only the owning node can focus:
// a session not local to this node resolves to an error, which the CLI surfaces
// as a non-zero exit (the expected broadcast no-op on non-owning desktops).
func (d *Node) handleSessionFocus(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolveLocal(p.SessionID)
	if err != nil {
		return nil, err
	}
	if err := d.revealFn(ctx, c, s.Tmux.PaneID); err != nil {
		return nil, err
	}
	return nil, nil
}
