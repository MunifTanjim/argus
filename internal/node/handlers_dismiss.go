package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func (d *Node) handleSessionDismiss(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	if s.Status == session.StatusWorking {
		return nil, fmt.Errorf("cannot dismiss a running session")
	}
	if s.Interaction != nil && s.Interaction.Kind == session.InteractionPermission {
		return nil, fmt.Errorf("cannot dismiss a session awaiting a permission")
	}
	r, ok := d.adapterFor(s.Agent).(adapter.Dismisser)
	if !ok {
		return nil, fmt.Errorf("dismiss unsupported for agent %s", s.Agent)
	}
	if err := r.Dismiss(ctx, s); err != nil {
		return nil, err
	}
	return nil, nil
}
