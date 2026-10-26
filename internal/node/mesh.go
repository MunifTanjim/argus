package node

import (
	"context"
	"fmt"
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
	d.meshCleared = map[string]bool{}
	d.meshPrev = map[string]session.Status{}
	d.meshWorked = map[string]bool{}
	d.meshLive = map[string]bool{}
	d.meshHeld = map[string]time.Time{}
	d.meshSigs = map[string]string{}
	d.meshKickCh = make(chan struct{}, 1)
}

// partyKey is "" until the agent session id is known: a pane id is only a
// delivery address and never names a party.
func partyKey(s session.Session) string {
	if s.AgentSessionID == "" {
		return ""
	}
	return registry.AgentSessionKey(s.Agent, s.AgentSessionID)
}

func notReady(id string) error {
	return fmt.Errorf("%s is not ready yet: its agent session id is unknown", id)
}

func (d *Node) party(s session.Session) api.Party {
	return api.Party{Node: d.id, Key: partyKey(s)}
}

func (d *Node) sessionForKey(key string) (session.Session, bool) {
	agent, id, ok := strings.Cut(key, ":")
	if !ok {
		return session.Session{}, false
	}
	if _, held := d.adapters[agent]; !held {
		return session.Session{}, false
	}
	return d.reg.FindByAgentSession(agent, id)
}

func (d *Node) meshKick() {
	select {
	case d.meshKickCh <- struct{}{}:
	default:
	}
}

func (d *Node) meshChanged() { d.notifyConns(api.MethodMessagesChanged) }

func (d *Node) meshEnded(ctx context.Context, s session.Session, by string) {
	if d.mesh == nil {
		return
	}
	key := partyKey(s)
	if key == "" {
		return
	}
	// The kill is irreversible, so its bookkeeping must survive a disconnecting caller.
	ctx = context.WithoutCancel(ctx)
	if err := d.mesh.SetEndedBy(ctx, key, by); err != nil {
		d.log.Warn("mesh: record ended-by failed", "key", key, "err", err)
	}
	if _, err := d.mesh.FailQueued(ctx, key); err != nil {
		d.log.Warn("mesh: fail queued failed", "key", key, "err", err)
	}
	open, err := d.mesh.OpenNotices(ctx, key)
	if err != nil {
		d.log.Warn("mesh: list notices failed", "subject", key, "err", err)
	}
	for _, n := range open {
		d.withdrawNotice(ctx, n, fmt.Sprintf("%s (%s) was killed by %s; its prompt %s is closed.",
			sessionLabel(s), d.compositeID(s), by, n.ID))
	}
	d.meshChanged()
}

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
	d.meshNotices(ctx, snap)
	d.meshDeliver(ctx, snap)
	d.meshWorked = map[string]bool{}
	if time.Since(d.meshExpiredAt) >= meshExpireEvery {
		d.meshExpiredAt = time.Now()
		cutoff := time.Now().Add(-meshRetention)
		if _, err := d.mesh.Expire(ctx, cutoff); err != nil {
			d.log.Warn("mesh: expire failed", "err", err)
		}
		if _, err := d.mesh.ExpireLinks(ctx, cutoff, func(key string) bool { return d.meshLive[key] }); err != nil {
			d.log.Warn("mesh: expire links failed", "err", err)
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

// meshRekey moves a /clear predecessor's links and messages to its successor.
func (d *Node) meshRekey(ctx context.Context, snap []session.Session) {
	applied := d.meshCleared
	d.meshCleared = make(map[string]bool, len(applied))
	for _, s := range snap {
		to := partyKey(s)
		if s.ClearedFrom == "" || to == "" {
			continue
		}
		from := registry.AgentSessionKey(s.Agent, s.ClearedFrom)
		pair := from + " " + to
		if applied[pair] {
			d.meshCleared[pair] = true
			continue
		}
		if err := d.mesh.Rekey(ctx, from, to); err != nil {
			d.log.Warn("mesh: rekey failed", "from", from, "to", to, "err", err)
			continue
		}
		d.meshCleared[pair] = true
		d.meshChanged()
	}
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
	return sessionLabel(s), d.compositeID(s)
}
