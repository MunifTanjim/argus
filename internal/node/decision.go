package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
	"github.com/MunifTanjim/argus/internal/session"
)

var errRequestGone = errors.New("the request was already answered or withdrawn")

// pendingDecision is a parked PermissionRequest awaiting the user's answer. The
// blocked hook handler waits on ch; MethodSessionRespond sends the user's response.
type pendingDecision struct {
	id, sessionID string
	ch            chan api.RespondParams
}

func (d *Node) park(pd *pendingDecision) {
	d.pendingMu.Lock()
	d.pending[pd.id] = pd
	d.pendingMu.Unlock()
}

func (d *Node) unpark(pd *pendingDecision) {
	d.pendingMu.Lock()
	if d.pending[pd.id] == pd {
		delete(d.pending, pd.id)
	}
	d.pendingMu.Unlock()
}

// claimPending removes and returns the parked decision requestID only if it
// belongs to sessionID.
func (d *Node) claimPending(sessionID, requestID string) *pendingDecision {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	pd := d.pending[requestID]
	if pd == nil || pd.sessionID != sessionID {
		return nil
	}
	delete(d.pending, requestID)
	return pd
}

// awaitDecision parks a PermissionRequest under id and blocks until the user
// answers in argus or the hook goes away (dismissed/answered in Claude → ctx
// cancel). Non-answered exits return "" so the hook prints nothing and Claude
// uses its own prompt. A subagent's request leaves the session's own state
// alone: the main thread is still running. A cancel interrupts the turn, so it
// surfaces the idle composer instead of clearing to working.
//
// Requests of an agent thread session go through the approval policy first:
// an allow, deny or draft is answered at once, and an ask parks as an approval
// card.
func (d *Node) awaitDecision(ctx context.Context, a adapter.Adapter, sid, id string, ev adapter.HookEvent) string {
	toolName, toolInput := a.PermissionPayload(ev)
	ix, agentID := a.PermissionPrompt(ev)
	var (
		card   *approvalCard
		closed <-chan string
	)
	if ix != nil && ix.Kind == session.InteractionPermission {
		if s, ok := d.reg.Get(sid); ok {
			if sc, ok := d.approvalScopeFor(ctx, s); ok {
				act, dec := d.approvalDecide(sc, toolName, toolInput)
				if dec.Outcome != policy.OutcomeAsk {
					return a.FormatDecision(toolName, toolInput, d.approvalImmediate(ctx, sc, act, dec))
				}
				card = d.openCard(sc, id, act, false)
				defer d.takeCard(card)
				closed = card.closed
				enrichCard(ix, card)
			}
		}
	}
	pd := &pendingDecision{id: id, sessionID: sid, ch: make(chan api.RespondParams, 1)}
	d.park(pd)
	if ix != nil {
		ix.RequestID = id
		d.reg.PushDecision(sid, *ix)
	}
	// Dropped after any base change so the stale base never shows in between.
	defer d.reg.DropDecision(sid, id)
	defer d.unpark(pd)
	var (
		p        api.RespondParams
		answered bool
		byArgus  bool
	)
	select {
	case p = <-pd.ch:
		answered = true
	case <-ctx.Done():
	case reason := <-closed:
		d.unpark(pd)
		p, answered, byArgus = api.RespondParams{Behavior: "deny", Reason: reason}, true, true
	}
	if agentID == "" {
		switch {
		case !answered:
			d.reg.ClearPendingDecision(sid)
		case p.QuestionAction == "cancel":
			kind, _ := d.reg.BaseInteractionKind(sid)
			d.reg.SurfaceIdle(sid, kind)
		default:
			d.reg.ClearInteraction(sid)
		}
	}
	if !answered {
		return ""
	}
	if card != nil && !byArgus {
		p = d.answerCard(ctx, card, p)
	}
	return a.FormatDecision(toolName, toolInput, p)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
