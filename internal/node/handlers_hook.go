package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// handleHook applies an agent hook event. Blocking hooks park until the user
// answers; the agent's own prompt stays live in parallel (whoever answers first wins).
func (d *Node) handleHook(ctx context.Context, params json.RawMessage) (any, error) {
	ev, err := api.Decode[adapter.HookEvent](params)
	if err != nil {
		return api.HookResult{}, err
	}
	a, ok := d.adapterList[0], true
	if ev.Agent != "" {
		a, ok = d.adapters[ev.Agent]
	}
	if !ok {
		return api.HookResult{}, nil
	}
	s, alive := d.applyHook(ctx, a, ev)
	if alive && a.ShouldBlock(ev) {
		return api.HookResult{Output: d.awaitDecision(ctx, a, s.ID, ev)}, nil
	}
	return api.HookResult{}, nil
}

// applyHook never waits on a decision.
func (d *Node) applyHook(ctx context.Context, a adapter.Adapter, ev adapter.HookEvent) (s session.Session, alive bool) {
	api.LogAttr(ctx, "agent", a.Agent())
	s, alive = a.ProcessHook(d.reg, ev)
	if alive {
		d.adoptSessionWorkspace(ctx, s)
	}
	api.LogAttr(ctx, "event", a.EventName(ev))
	if tool, _ := a.PermissionPayload(ev); tool != "" {
		api.LogAttr(ctx, "tool", tool)
	}
	// Rescan only for lifecycle events; per-tool-call hooks come from known
	// sessions and scanning is pure churn.
	if a.RescanOnHook(ev) {
		go d.scan(context.Background())
	}
	return s, alive
}

// handleSessionRespond resolves the session's parked PermissionRequest with the
// hook decision JSON. When no hook is parked, falls back to the adapter's
// Responder capability for HTTP-service agents (e.g. opencode).
// canRespond reports whether handleSessionRespond has a path that answers s.
func (d *Node) canRespond(s session.Session) bool {
	d.pendingMu.Lock()
	_, parked := d.pending[s.ID]
	d.pendingMu.Unlock()
	if parked {
		return true
	}
	_, ok := d.adapterFor(s.Agent).(adapter.Responder)
	return ok
}

func (d *Node) handleSessionRespond(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.RespondParams](params)
	if err != nil {
		return nil, err
	}
	if pd := d.takePending(p.SessionID); pd != nil {
		pd.ch <- p
		d.log.Info("respond delivered to parked decision", "session", p.SessionID, "kind", p.Kind)
		return nil, nil
	}
	if s, ok := d.reg.Get(p.SessionID); ok {
		if r, ok := d.adapterFor(s.Agent).(adapter.Responder); ok {
			if err := r.Respond(ctx, s, p); err != nil {
				d.log.Warn("responder failed", "session", p.SessionID, "err", err)
				return nil, err
			}
			if o, ok := r.(adapter.InteractionOwner); !ok || !o.OwnsInteraction() {
				d.reg.ClearInteraction(p.SessionID)
			}
			return nil, nil
		}
	}
	d.log.Warn("respond with no parked decision, dropped", "session", p.SessionID, "kind", p.Kind)
	return nil, nil
}
