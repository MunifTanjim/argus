package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
)

// handleSessionFocus reveals a session's pane, invoked by `argus _focus <id>` on
// a notification click. The id may be composite "<nodeID>:<localID>" (broadcast)
// or bare (standalone). Only the owning node can focus: a non-local session
// errors, which the CLI exits non-zero on — the expected no-op on non-owning
// desktops.
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
