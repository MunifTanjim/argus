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
		return api.HookResult{Output: d.awaitDecision(ctx, a, s.ID, newID(), ev)}, nil
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

// handleSessionRespond resolves a parked PermissionRequest with the hook
// decision JSON: the one p.RequestID names, else the one the session shows.
// When none is parked, falls back to the adapter's Responder capability for
// HTTP-service agents (e.g. opencode).
func (d *Node) handleSessionRespond(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.RespondParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	rid := p.RequestID
	if rid == "" && ok && s.Interaction != nil {
		rid = s.Interaction.RequestID
	}
	if pd := d.claimPending(p.SessionID, rid); pd != nil {
		pd.ch <- p
		d.log.Info("respond delivered to parked decision", "session", p.SessionID, "request", rid, "kind", p.Kind)
		return nil, nil
	}
	if ok {
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
	if p.RequestID != "" {
		d.log.Warn("respond to a request no longer parked", "session", p.SessionID, "request", p.RequestID)
		return nil, errRequestGone
	}
	d.log.Warn("respond with no parked decision, dropped", "session", p.SessionID, "kind", p.Kind)
	return nil, nil
}
