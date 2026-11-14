package registry

import (
	"slices"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

// decisionQueue holds a session's pending decisions, oldest first. since is
// when the first of them was pushed onto an empty queue.
type decisionQueue struct {
	items []session.Interaction
	since string
}

// PushDecision queues a pending decision on session id. While any is queued,
// the session the registry hands out shows the oldest as its interaction and
// awaits input. ix.RequestID identifies it for DropDecision; pushing a request
// already queued replaces it in place. No-op for an unknown session or an
// empty RequestID.
func (r *Registry) PushDecision(id string, ix session.Interaction) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[id]
	if s == nil || ix.RequestID == "" {
		return
	}
	q := r.decisions[id]
	if q == nil {
		q = &decisionQueue{since: now().UTC().Format(time.RFC3339)}
		r.decisions[id] = q
	}
	if i := q.index(ix.RequestID); i >= 0 {
		q.items[i] = ix
	} else {
		q.items = append(q.items, ix)
	}
	r.publish(EventUpdated, s)
}

// DropDecision removes the pending decision requestID from session id.
func (r *Registry) DropDecision(id, requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.decisions[id]
	if q == nil {
		return
	}
	i := q.index(requestID)
	if i < 0 {
		return
	}
	q.items = slices.Delete(q.items, i, i+1)
	if len(q.items) == 0 {
		delete(r.decisions, id)
	}
	if s := r.sessions[id]; s != nil {
		r.publish(EventUpdated, s)
	}
}

// BaseInteractionKind reports the kind of session id's own interaction ("" when
// it has none), ignoring pending decisions.
func (r *Registry) BaseInteractionKind(id string) (session.InteractionKind, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[id]
	if s == nil {
		return "", false
	}
	if s.Interaction == nil {
		return "", true
	}
	return s.Interaction.Kind, true
}

func (q *decisionQueue) index(requestID string) int {
	return slices.IndexFunc(q.items, func(ix session.Interaction) bool { return ix.RequestID == requestID })
}

// view copies s with its oldest pending decision laid over the stored status
// and interaction. Caller holds r.mu.
func (r *Registry) view(s *session.Session) session.Session {
	v := *s
	q := r.decisions[s.ID]
	if q == nil || len(q.items) == 0 {
		return v
	}
	head := q.items[0]
	v.Interaction = &head
	v.Status = session.StatusAwaitingInput
	v.StatusSince = q.since
	return v
}
