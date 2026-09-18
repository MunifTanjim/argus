package node

import (
	"context"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// decisionTimeout bounds how long a PermissionRequest hook blocks, kept just under
// the hook's own timeout so we fall back before Claude kills the hook.
var decisionTimeout = (claudecode.PermissionRequestHookTimeoutSeconds - 10) * time.Second

// pendingDecision is a parked PermissionRequest awaiting the user's answer. The
// blocked hook handler waits on ch; MethodSessionRespond sends the user's response.
type pendingDecision struct {
	ch chan api.RespondParams
}

// park registers a pending decision for a session and returns it plus a cleanup.
func (d *Node) park(sid string) (*pendingDecision, func()) {
	pd := &pendingDecision{ch: make(chan api.RespondParams, 1)}
	d.pendingMu.Lock()
	d.pending[sid] = pd
	d.pendingMu.Unlock()
	return pd, func() {
		d.pendingMu.Lock()
		if d.pending[sid] == pd {
			delete(d.pending, sid)
		}
		d.pendingMu.Unlock()
	}
}

// takePending removes and returns a session's parked decision, if any.
func (d *Node) takePending(sid string) *pendingDecision {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	pd := d.pending[sid]
	if pd != nil {
		delete(d.pending, sid)
	}
	return pd
}

// awaitDecision parks a PermissionRequest and blocks until the user answers in
// argus, the hook goes away (dismissed/answered in Claude → ctx cancel), or the
// timeout fires. It resolves the interaction on every exit so no stale prompt
// lingers; non-answered exits return "" so the hook prints nothing and Claude uses
// its own prompt. A cancel interrupts the turn (Claude stops rather than
// continuing), so it surfaces the idle composer instead of clearing to working.
func (d *Node) awaitDecision(ctx context.Context, a adapter.Adapter, sid string, ev adapter.HookEvent) string {
	toolName, toolInput := a.PermissionPayload(ev)
	pd, cancel := d.park(sid)
	defer cancel()
	select {
	case p := <-pd.ch:
		if p.QuestionAction == "cancel" {
			d.reg.SurfaceIdle(sid, session.InteractionQuestion)
		} else {
			d.reg.ClearInteraction(sid)
		}
		return a.FormatDecision(toolName, toolInput, p)
	case <-ctx.Done():
		d.reg.ClearInteraction(sid)
		return ""
	case <-time.After(decisionTimeout):
		d.reg.ClearInteraction(sid)
		return ""
	}
}
