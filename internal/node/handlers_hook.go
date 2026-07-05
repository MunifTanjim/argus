package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
)

// handleHook applies an agent hook event. Blocking hooks park until the user
// answers; the agent's own prompt stays live in parallel (whoever answers first wins).
func (d *Node) handleHook(ctx context.Context, params json.RawMessage) (any, error) {
	ev, err := api.Decode[adapter.HookEvent](params)
	if err != nil {
		return nil, err
	}
	a := adapters.ByAgent(ev.Agent)
	if a == nil {
		return api.HookResult{}, nil
	}
	api.LogAttr(ctx, "agent", ev.Agent)
	s, alive := a.ProcessHook(d.reg, ev)
	event := a.EventName(ev)
	api.LogAttr(ctx, "event", event)
	if tool, _ := a.PermissionPayload(ev); tool != "" {
		api.LogAttr(ctx, "tool", tool)
	}
	// Rescan only for lifecycle events; per-tool-call hooks come from known
	// sessions and scanning is pure churn.
	if a.RescanOnHook(ev) {
		go d.scan(context.Background())
	}

	if alive && a.ShouldBlock(ev) {
		return api.HookResult{Output: d.awaitDecision(ctx, a, s.ID, ev)}, nil
	}
	return api.HookResult{}, nil
}

// handleSessionRespond resolves the session's parked PermissionRequest with the
// hook decision JSON. Anything not parked is a no-op (raw screen view is the
// manual fallback).
func (d *Node) handleSessionRespond(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.RespondParams](params)
	if err != nil {
		return nil, err
	}
	if pd := d.takePending(p.SessionID); pd != nil {
		pd.ch <- pd.format(p)
	}
	return nil, nil
}
