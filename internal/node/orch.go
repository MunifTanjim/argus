package node

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

const (
	orchExpireEvery = time.Hour
	orchRetention   = 7 * 24 * time.Hour
	// orchDeliverHold bounds the wait for a recipient to start the turn of its
	// last delivered message before the next one goes out.
	orchDeliverHold = 10 * time.Second
	// orchGoneGrace covers a /clear whose hook lands after a scan saw the new
	// agent session id: the old key leaves the snapshot before the rekey.
	orchGoneGrace = 10 * time.Second
	// orchTypingQuiet is how long a pane's clients must be idle before argus
	// types a delivery into it, so it never types over a person.
	orchTypingQuiet = 5 * time.Second
	// orchDeliverWorkers bounds the deliveries that run at the same time.
	orchDeliverWorkers = 8
)

// errPaneBusy marks a delivery skipped because a person may be typing.
var errPaneBusy = errors.New("pane busy")

// orchCallTimeout bounds each agent call of an orch pass: a delivery, or a
// transcript read. A variable so tests can shorten it.
var orchCallTimeout = 15 * time.Second

// callWithin runs fn with a deadline of d. It returns when fn does or when the
// deadline passes; a fn that ignores its context keeps running on its own.
func callWithin[T any](ctx context.Context, d time.Duration, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := fn(ctx)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// SetOrchStore with nil disables orch.* and message.*. Call before Run.
func (d *Node) SetOrchStore(s *orch.Store) {
	d.orch = s
	if s != nil {
		s.SetNodeID(d.id)
	}
	d.orchCleared = map[string]bool{}
	d.orchPrev = map[string]session.Status{}
	d.orchWorked = map[string]bool{}
	d.orchLive = map[string]bool{}
	d.orchGone = map[string]time.Time{}
	d.orchGoneGrace = orchGoneGrace
	d.orchSettled = make(chan struct{})
	d.orchSettleOne = &sync.Once{}
	d.orchHeld = map[string]time.Time{}
	d.orchSigs = map[string]string{}
	d.orchParents = map[string]session.ParentRef{}
	d.orchKickCh = make(chan struct{}, 1)
}

// actorKey is "" until the agent session id is known: a pane id is only a
// delivery address and never names an actor.
func actorKey(s session.Session) string {
	if s.AgentSessionID == "" {
		return ""
	}
	return registry.AgentSessionKey(s.Agent, s.AgentSessionID)
}

func notReady(id string) error {
	return fmt.Errorf("%s is not ready yet: its agent session id is unknown", id)
}

func (d *Node) actor(s session.Session) api.Actor {
	return api.Actor{Node: d.id, Key: actorKey(s)}
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

func (d *Node) orchKick() {
	select {
	case d.orchKickCh <- struct{}{}:
	default:
	}
}

func (d *Node) orchChanged() { d.notifyConns(api.MethodMessageChanged) }

// killTree kills target and the live sessions below it in the parent links,
// deepest first, and returns the composite IDs it killed. A failed kill does
// not stop the others, and a disconnecting caller does not stop the tree.
func (d *Node) killTree(ctx context.Context, target session.Session, by string) ([]string, error) {
	ctx = context.WithoutCancel(ctx)
	below, err := d.descendants(ctx, target)
	if err != nil {
		return nil, err
	}
	var killed []string
	var errs []error
	kill := func(s session.Session, by string) {
		if err := d.killSession(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("kill %s: %w", d.compositeID(s), err))
			return
		}
		d.orchEnded(ctx, s, by)
		killed = append(killed, d.compositeID(s))
	}
	for _, s := range below {
		kill(s, "ancestor")
	}
	kill(target, by)
	return killed, errors.Join(errs...)
}

// descendants returns the live local sessions below root in the parent links,
// each after its own descendants.
func (d *Node) descendants(ctx context.Context, root session.Session) ([]session.Session, error) {
	key := actorKey(root)
	if d.orch == nil || key == "" {
		return nil, nil
	}
	seen := map[api.Actor]bool{{Node: d.id, Key: key}: true}
	var out []session.Session
	var walk func(parentKey string) error
	walk = func(parentKey string) error {
		links, err := d.orch.Children(ctx, api.Actor{Node: d.id, Key: parentKey})
		if err != nil {
			return err
		}
		for _, l := range links {
			if l.Child.Node != d.id || seen[l.Child] {
				continue
			}
			seen[l.Child] = true
			if err := walk(l.Child.Key); err != nil {
				return err
			}
			if s, ok := d.sessionForKey(l.Child.Key); ok {
				out = append(out, s)
			}
		}
		return nil
	}
	return out, walk(key)
}

func (d *Node) orchEnded(ctx context.Context, s session.Session, by string) {
	if d.orch == nil {
		return
	}
	key := actorKey(s)
	if key == "" {
		return
	}
	// The kill is irreversible, so its bookkeeping must survive a disconnecting caller.
	ctx = context.WithoutCancel(ctx)
	if err := d.orch.SetEndedBy(ctx, api.Actor{Node: d.id, Key: key}, by); err != nil {
		d.log.Warn("orch: record ended-by failed", "key", key, "err", err)
	}
	if _, err := d.orch.FailQueued(ctx, api.Actor{Node: d.id, Key: key}); err != nil {
		d.log.Warn("orch: fail queued failed", "key", key, "err", err)
	}
	open, err := d.orch.OpenInteractions(ctx, api.Actor{Node: d.id, Key: key})
	if err != nil {
		d.log.Warn("orch: list interactions failed", "child", key, "err", err)
	}
	for _, n := range open {
		d.withdrawInteraction(ctx, n, fmt.Sprintf("%s (%s) was killed by %s; its interaction %s ended.",
			sessionLabel(s), d.compositeID(s), by, n.ID))
	}
	d.orchChanged()
}

func (d *Node) orchLoop(ctx context.Context) {
	events, cancel := d.reg.Subscribe()
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			d.orchNoteEvent(ev)
		case <-tick.C:
		case <-d.orchKickCh:
		}
		d.drainOrchWakeups(events)
		d.orchPass(ctx)
	}
}

// drainOrchWakeups folds a burst of wakeups into the pass about to run.
func (d *Node) drainOrchWakeups(events <-chan registry.Event) {
	for {
		select {
		case ev := <-events:
			d.orchNoteEvent(ev)
		case <-d.orchKickCh:
		default:
			return
		}
	}
}

// orchNoteEvent keeps a turn whose working and idle updates fold into one pass.
func (d *Node) orchNoteEvent(ev registry.Event) {
	if ev.Session.Status == session.StatusWorking {
		d.orchWorked[ev.Session.ID] = true
	}
}

func (d *Node) orchPass(ctx context.Context) {
	snap := d.reg.Snapshot()
	d.orchRekey(ctx, snap)
	d.orchFailGone(ctx, snap)
	d.orchNotices(ctx, snap)
	d.orchDeliver(ctx, snap)
	d.orchRepublishParents(snap)
	d.orchWorked = map[string]bool{}
	if time.Since(d.orchExpiredAt) >= orchExpireEvery {
		d.orchExpiredAt = time.Now()
		cutoff := time.Now().Add(-orchRetention)
		if _, err := d.orch.Expire(ctx, cutoff); err != nil {
			d.log.Warn("orch: expire failed", "err", err)
		}
		if _, err := d.orch.ExpireLinks(ctx, cutoff, func(c api.Actor) bool { return c.Node == d.id && d.orchLive[c.Key] }); err != nil {
			d.log.Warn("orch: expire links failed", "err", err)
		}
	}
}

// orchFailGone fails the messages queued to sessions that are gone: argus
// cannot tell a crash from a normal end, and a message that cannot be delivered
// must fail.
func (d *Node) orchFailGone(ctx context.Context, snap []session.Session) {
	live := make(map[string]bool, len(snap))
	for _, s := range snap {
		if key := actorKey(s); key != "" {
			live[key] = true
		}
	}
	now := time.Now()
	d.markGone(maps.Keys(d.orchLive), live, now)
	d.orchSeedGone(ctx, live, now)
	for key, since := range d.orchGone {
		if live[key] {
			delete(d.orchGone, key)
			continue
		}
		if now.Sub(since) < d.orchGoneGrace {
			continue
		}
		n, err := d.orch.FailQueued(ctx, api.Actor{Node: d.id, Key: key})
		if err != nil {
			d.log.Warn("orch: fail queued failed", "key", key, "err", err)
			continue
		}
		delete(d.orchGone, key)
		delete(d.orchHeld, key)
		if n > 0 {
			d.orchChanged()
		}
	}
	d.orchLive = live
}

// orchSeedGone marks the recipients on this node that ended while the node was
// down, once startup discovery has settled.
func (d *Node) orchSeedGone(ctx context.Context, live map[string]bool, now time.Time) {
	if d.orchSeeded {
		return
	}
	select {
	case <-d.orchSettled:
	default:
		return
	}
	keys, err := d.orch.QueuedRecipients(ctx, d.id)
	if err != nil {
		d.log.Warn("orch: list recipients failed", "err", err)
		return
	}
	d.orchSeeded = true
	d.markGone(slices.Values(keys), live, now)
}

func (d *Node) markGone(keys iter.Seq[string], live map[string]bool, now time.Time) {
	for key := range keys {
		if _, gone := d.orchGone[key]; !gone && !live[key] {
			d.orchGone[key] = now
		}
	}
}

// orchSettle waits for the first scan and the first sync of every discoverer
// that registers sessions asynchronously.
func (d *Node) orchSettle(ctx context.Context) {
	for _, disc := range d.discs {
		s, ok := disc.(adapter.Settler)
		if !ok {
			continue
		}
		select {
		case <-s.Settled():
		case <-ctx.Done():
			return
		}
	}
	d.markOrchSettled()
}

// markOrchSettled closes orchSettled once.
func (d *Node) markOrchSettled() { d.orchSettleOne.Do(func() { close(d.orchSettled) }) }

// orchRepublishParents republishes the sessions whose parent link changed:
// the link and the parent's liveness live outside the registry.
func (d *Node) orchRepublishParents(snap []session.Session) {
	prev := d.orchParents
	d.orchParents = make(map[string]session.ParentRef, len(prev))
	for _, s := range snap {
		var ref session.ParentRef
		if p := d.parentRef(s); p != nil {
			ref = *p
		}
		if ref != (session.ParentRef{}) {
			d.orchParents[s.ID] = ref
		}
		if prev[s.ID] != ref {
			d.reg.Republish(s.ID)
		}
	}
}

// orchRekey moves a /clear predecessor's links and messages to its successor.
func (d *Node) orchRekey(ctx context.Context, snap []session.Session) {
	applied := d.orchCleared
	d.orchCleared = make(map[string]bool, len(applied))
	for _, s := range snap {
		to := actorKey(s)
		if s.ClearedFrom == "" || to == "" {
			continue
		}
		from := registry.AgentSessionKey(s.Agent, s.ClearedFrom)
		pair := from + " " + to
		if applied[pair] {
			d.orchCleared[pair] = true
			continue
		}
		if err := d.orch.Rekey(ctx, from, to); err != nil {
			d.log.Warn("orch: rekey failed", "from", from, "to", to, "err", err)
			continue
		}
		d.orchChanged()
		d.orchCleared[pair] = true
	}
}

// orchDeliver sends each recipient its next queued message. A recipient that
// got a message stays held until it starts a turn, so a second message never
// lands before the agent starts on the first one. A failed delivery holds the
// recipient too, so the retry waits.
func (d *Node) orchDeliver(ctx context.Context, snap []session.Session) {
	now := time.Now()
	for _, s := range snap {
		if key := actorKey(s); key != "" && (s.Status == session.StatusWorking || d.orchWorked[s.ID]) {
			delete(d.orchHeld, key)
		}
	}
	keys, err := d.orch.QueuedRecipients(ctx, d.id)
	if err != nil {
		d.log.Warn("orch: list recipients failed", "err", err)
		return
	}
	type job struct {
		key   string
		s     session.Session
		batch []api.Message
		text  string
	}
	var jobs []job
	for _, key := range keys {
		if until, held := d.orchHeld[key]; held && now.Before(until) {
			continue
		}
		s, live := d.sessionForKey(key)
		if !live || !s.TakesPrompt(false) {
			continue
		}
		queued, err := d.orch.Queued(ctx, api.Actor{Node: d.id, Key: key})
		if err != nil {
			d.log.Warn("orch: list queued failed", "to", key, "err", err)
			continue
		}
		batch := nextBatch(d.id, queued)
		if len(batch) == 0 {
			continue
		}
		ds := make([]orch.Delivery, len(batch))
		for i, m := range batch {
			ds[i] = d.delivery(m)
		}
		jobs = append(jobs, job{key: key, s: s, batch: batch, text: orch.RenderPrompt(ds)})
	}

	// Each delivery is an agent call that can hang, so they run at the same
	// time and each has a deadline. The results are applied here, in order.
	results := make([]error, len(jobs))
	sem := make(chan struct{}, orchDeliverWorkers)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			// One deadline covers the pane check and the delivery.
			_, results[i] = callWithin(ctx, orchCallTimeout, func(ctx context.Context) (struct{}, error) {
				if j.s.Input == session.InputPane && d.paneBusy(ctx, j.s) {
					return struct{}{}, errPaneBusy
				}
				// The loop counts a delivery past its deadline as failed, so a
				// late one must not type: it would arrive twice.
				if err := ctx.Err(); err != nil {
					return struct{}{}, err
				}
				return struct{}{}, d.deliver(ctx, j.s, j.text)
			})
		}()
	}
	wg.Wait()
	for i, j := range jobs {
		err := results[i]
		if errors.Is(err, errPaneBusy) {
			continue
		}
		d.orchHeld[j.key] = time.Now().Add(orchDeliverHold)
		if err != nil {
			d.log.Warn("orch: deliver failed", "id", j.batch[0].ID, "count", len(j.batch), "to", j.key, "err", err)
			continue
		}
		for _, m := range j.batch {
			if _, err := d.orch.Transition(ctx, m.ID, api.MessageQueued, api.MessageDelivered); err != nil {
				d.log.Warn("orch: mark delivered failed", "id", m.ID, "err", err)
			}
		}
		d.orchChanged()
	}
}

// senderLabel names an actor of this node; another node's actor goes by
// <node>:<key>, as a local session never stands for it.
func (d *Node) senderLabel(p api.Actor) (label, sessionID string) {
	if reservedKey(p.Key) {
		return p.Key, ""
	}
	if p.Node != d.id {
		return actorID(p), ""
	}
	s, live := d.sessionForKey(p.Key)
	if !live {
		return p.Key, ""
	}
	return sessionLabel(s), d.compositeID(s)
}

// nextBatch picks the messages for one prompt: a message from the owner of
// node self goes alone, and the other messages go together.
func nextBatch(self string, queued []api.Message) []api.Message {
	owner := api.Actor{Node: self, Key: api.ActorOwner}
	if len(queued) == 0 || queued[0].From == owner {
		return queued[:min(1, len(queued))]
	}
	var batch []api.Message
	for _, m := range queued {
		if m.From != owner {
			batch = append(batch, m)
		}
	}
	return batch
}

func (d *Node) delivery(m api.Message) orch.Delivery {
	dl := orch.Delivery{Msg: m}
	dl.From, dl.FromSession = d.senderLabel(m.From)
	if m.Child != nil {
		dl.Child, dl.ChildSession = d.senderLabel(*m.Child)
	}
	return dl
}

// reservedKey reports whether key names the owner or argus rather than a session.
func reservedKey(key string) bool {
	return key == api.ActorOwner || key == api.ActorArgus
}

func (d *Node) deliver(ctx context.Context, s session.Session, text string) error {
	if d.deliverFn != nil {
		return d.deliverFn(ctx, s, text)
	}
	return d.deliverPrompt(ctx, s, text)
}

// paneBusy reports whether a person may be typing in s's pane: a client of its
// session pressed a key in the last orchTypingQuiet, or the prompt holds a
// draft. A probe that fails counts as not busy.
func (d *Node) paneBusy(ctx context.Context, s session.Session) bool {
	probe := d.paneProbe
	if probe == nil {
		probe = d.probePane
	}
	activity, screen, err := probe(ctx, s)
	if err != nil {
		d.log.Warn("orch: probe pane failed", "session", s.ID, "err", err)
		return false
	}
	if !activity.IsZero() && time.Since(activity) < orchTypingQuiet {
		return true
	}
	dr, ok := d.adapterFor(s.Agent).(adapter.DraftReader)
	return ok && dr.HasDraft(screen)
}

// probePane reads the last client activity of s's tmux session and, when the
// agent can read a draft, the visible pane.
func (d *Node) probePane(ctx context.Context, s session.Session) (time.Time, string, error) {
	_, c, err := d.resolve(s.ID)
	if err != nil {
		return time.Time{}, "", err
	}
	activity, err := c.LastClientActivity(ctx, s.Tmux.PaneID)
	if err != nil {
		return time.Time{}, "", err
	}
	if _, ok := d.adapterFor(s.Agent).(adapter.DraftReader); !ok {
		return activity, "", nil
	}
	screen, err := c.CapturePane(ctx, s.Tmux.PaneID, tmux.CaptureOpts{})
	return activity, screen, err
}
