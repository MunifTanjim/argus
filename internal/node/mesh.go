package node

import (
	"context"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mesh"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

const (
	meshExpireEvery = time.Hour
	meshRetention   = 7 * 24 * time.Hour
	// meshDeliverHold bounds the wait for a recipient to start the turn of its
	// last delivered message before the next one goes out.
	meshDeliverHold = 10 * time.Second
)

// SetMeshStore with nil disables agentctl and messages. Call before Run.
func (d *Node) SetMeshStore(s *mesh.Store) {
	d.mesh = s
	d.meshKeys = map[string]string{}
	d.meshPrev = map[string]session.Status{}
	d.meshWorked = map[string]bool{}
	d.meshLive = map[string]bool{}
	d.meshHeld = map[string]time.Time{}
	d.meshKickCh = make(chan struct{}, 1)
}

func partyKey(s session.Session) string {
	if s.AgentSessionID != "" {
		return registry.AgentSessionKey(s.Agent, s.AgentSessionID)
	}
	return s.ID
}

func (d *Node) party(s session.Session) api.Party {
	return api.Party{Node: d.id, Key: partyKey(s)}
}

func (d *Node) sessionForKey(key string) (session.Session, bool) {
	if agent, id, ok := strings.Cut(key, ":"); ok {
		if _, held := d.adapters[agent]; held {
			return d.reg.FindByAgentSession(agent, id)
		}
	}
	return d.reg.Get(key)
}

func (d *Node) meshKick() {
	select {
	case d.meshKickCh <- struct{}{}:
	default:
	}
}

func (d *Node) meshChanged() { d.notifyConns(api.MethodMessagesChanged) }

func (d *Node) meshLoop(ctx context.Context) {
	events, cancel := d.reg.Subscribe()
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			d.meshNoteEvent(ev)
		case <-tick.C:
		case <-d.meshKickCh:
		}
		d.drainMeshWakeups(events)
		d.meshPass(ctx)
	}
}

// drainMeshWakeups folds a burst of wakeups into the pass about to run.
func (d *Node) drainMeshWakeups(events <-chan registry.Event) {
	for {
		select {
		case ev := <-events:
			d.meshNoteEvent(ev)
		case <-d.meshKickCh:
		default:
			return
		}
	}
}

// meshNoteEvent keeps a turn whose working and idle updates fold into one pass.
func (d *Node) meshNoteEvent(ev registry.Event) {
	if ev.Session.Status == session.StatusWorking {
		d.meshWorked[ev.Session.ID] = true
	}
}

func (d *Node) meshPass(ctx context.Context) {
	snap := d.reg.Snapshot()
	d.meshRekey(ctx, snap)
	d.meshFailGone(ctx, snap)
	d.meshDeliver(ctx, snap)
	d.meshWorked = map[string]bool{}
	if time.Since(d.meshExpiredAt) >= meshExpireEvery {
		d.meshExpiredAt = time.Now()
		if _, err := d.mesh.Expire(ctx, time.Now().Add(-meshRetention)); err != nil {
			d.log.Warn("mesh: expire failed", "err", err)
		}
	}
}

// meshFailGone fails the messages queued to sessions that ended since the last
// pass: argus cannot tell a crash from a normal end, and a message that cannot
// be delivered must fail.
func (d *Node) meshFailGone(ctx context.Context, snap []session.Session) {
	live := make(map[string]bool, len(snap))
	for _, s := range snap {
		if key := partyKey(s); key != "" {
			live[key] = true
		}
	}
	for key := range d.meshLive {
		if live[key] {
			continue
		}
		delete(d.meshHeld, key)
		n, err := d.mesh.FailQueued(ctx, key)
		if err != nil {
			d.log.Warn("mesh: fail queued failed", "key", key, "err", err)
			continue
		}
		if n > 0 {
			d.meshChanged()
		}
	}
	d.meshLive = live
}

func (d *Node) meshRekey(ctx context.Context, snap []session.Session) {
	for _, s := range snap {
		key := partyKey(s)
		old, seen := d.meshKeys[s.ID]
		switch {
		case seen && old != key:
			d.rekey(ctx, s.ID, old, key)
		case !seen && s.AgentSessionID != "":
			d.rekey(ctx, s.ID, s.ID, key)
		case !seen:
			d.meshKeys[s.ID] = key
		}
	}
}

func (d *Node) rekey(ctx context.Context, sessionID, oldKey, newKey string) {
	if err := d.mesh.Rekey(ctx, oldKey, newKey); err != nil {
		d.log.Warn("mesh: rekey failed", "from", oldKey, "to", newKey, "err", err)
		return
	}
	d.meshKeys[sessionID] = newKey
	d.meshChanged()
}

// meshDeliver sends each recipient its next queued message. A recipient that
// got a message stays held until it starts a turn, so a second message never
// lands before the agent starts on the first one.
func (d *Node) meshDeliver(ctx context.Context, snap []session.Session) {
	now := time.Now()
	for _, s := range snap {
		if key := partyKey(s); key != "" && (s.Status == session.StatusWorking || d.meshWorked[s.ID]) {
			delete(d.meshHeld, key)
		}
	}
	keys, err := d.mesh.QueuedRecipients(ctx)
	if err != nil {
		d.log.Warn("mesh: list recipients failed", "err", err)
		return
	}
	for _, key := range keys {
		if until, held := d.meshHeld[key]; held && now.Before(until) {
			continue
		}
		s, live := d.sessionForKey(key)
		if !live || !s.TakesPrompt(false) {
			continue
		}
		m, ok, err := d.mesh.NextQueued(ctx, key)
		if err != nil {
			d.log.Warn("mesh: next queued failed", "to", key, "err", err)
			continue
		}
		if !ok {
			continue
		}
		label, senderID := d.senderLabel(m.From.Key)
		if err := d.deliverPrompt(ctx, s, mesh.Render(m, label, senderID)); err != nil {
			d.log.Warn("mesh: deliver failed", "id", m.ID, "to", key, "err", err)
			continue
		}
		d.meshHeld[key] = now.Add(meshDeliverHold)
		if _, err := d.mesh.Transition(ctx, m.ID, api.MessageQueued, api.MessageDelivered); err != nil {
			d.log.Warn("mesh: mark delivered failed", "id", m.ID, "err", err)
			continue
		}
		d.meshChanged()
	}
}

func (d *Node) senderLabel(key string) (label, sessionID string) {
	if key == api.PartyOwner || key == api.PartyArgus {
		return key, ""
	}
	s, live := d.sessionForKey(key)
	if !live {
		return key, ""
	}
	label = s.Name
	if label == "" {
		label = s.Agent
	}
	return label, d.compositeID(s)
}
