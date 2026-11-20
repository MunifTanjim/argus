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
	// orchGoneGrace absorbs a session that leaves the registry for a short
	// time: ending its link and failing its messages cannot be undone.
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
	d.outboxEvery = outboxRetryEvery
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
// deepest first, and returns the composite IDs it killed. Each child on another
// node is killed there with its subtree, in parallel and before any local
// session; one that cannot be reached or killed is returned in notKilled. A
// failed kill does not stop the others, and a disconnecting caller does not
// stop the tree.
func (d *Node) killTree(ctx context.Context, target session.Session, by string) (killed, notKilled []string, err error) {
	ctx = context.WithoutCancel(ctx)
	below, remote, err := d.descendants(ctx, target)
	if err != nil {
		return nil, nil, err
	}
	results := make([]api.OrchKillResult, len(remote))
	remoteErrs := make([]error, len(remote))
	var wg sync.WaitGroup
	for i, l := range remote {
		wg.Go(func() { results[i], remoteErrs[i] = d.killRemoteChild(ctx, l) })
	}
	wg.Wait()
	for i, l := range remote {
		killed = append(killed, results[i].Killed...)
		notKilled = append(notKilled, results[i].NotKilled...)
		if err := remoteErrs[i]; err != nil {
			d.log.Warn("orch: kill remote child failed", "node", l.Child.Node, "child", l.Child.Key, "err", err)
			notKilled = append(notKilled, remoteChildID(l))
		}
	}
	var errs []error
	kill := func(s session.Session, by string) {
		if err := d.killSession(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("kill %s: %w", d.agentID(s), err))
			return
		}
		d.orchEnded(ctx, s, by)
		killed = append(killed, d.agentID(s))
	}
	for _, s := range below {
		kill(s, "ancestor")
	}
	kill(target, by)
	return killed, notKilled, errors.Join(errs...)
}

func remoteChildID(l api.Link) string {
	return actorID(l.Child)
}

// killRemoteChild asks the child's node to kill it and its subtree as a child
// of a session this node is killing, and records the mirror as ended. The
// child's node also pushes link_ended; both set the same ended_by.
func (d *Node) killRemoteChild(ctx context.Context, l api.Link) (api.OrchKillResult, error) {
	caller := callerRef{Actor: l.Parent}
	if p, ok := d.sessionForKey(l.Parent.Key); ok {
		caller = d.localCallerRef(p)
	}
	params := api.OrchKillParams{
		OrchTargetParams: api.OrchTargetParams{Caller: caller.remote(), SessionID: actorID(l.Child)},
		Ancestor:         true,
		ChildKey:         l.Child.Key,
	}
	by := "ancestor"
	var res api.OrchKillResult
	if err := d.forward(ctx, l.Child.Node, api.MethodOrchSessionKill, params, &res); err != nil {
		var rpcErr *api.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeUnknownSession {
			return api.OrchKillResult{}, err
		}
		// The child ended on its own before its node told this one.
		by, res = "self", api.OrchKillResult{}
	}
	if err := d.orch.SetEndedBy(ctx, l.Child, by); err != nil {
		d.log.Warn("orch: record ended-by failed", "key", l.Child.Key, "err", err)
	}
	return res, nil
}

// descendants returns the live local sessions below root in the parent links,
// each after its own descendants, and the links to the children on other nodes
// that have not ended. A remote child's own subtree is its node's to walk.
func (d *Node) descendants(ctx context.Context, root session.Session) ([]session.Session, []api.Link, error) {
	key := actorKey(root)
	if d.orch == nil || key == "" {
		return nil, nil, nil
	}
	seen := map[api.Actor]bool{{Node: d.id, Key: key}: true}
	var out []session.Session
	var remote []api.Link
	var walk func(parentKey string) error
	walk = func(parentKey string) error {
		links, err := d.orch.Children(ctx, api.Actor{Node: d.id, Key: parentKey})
		if err != nil {
			return err
		}
		for _, l := range links {
			if seen[l.Child] {
				continue
			}
			seen[l.Child] = true
			if l.Child.Node != d.id {
				if l.Parent.Node == d.id && l.EndedBy == "" {
					remote = append(remote, l)
				}
				continue
			}
			if err := walk(l.Child.Key); err != nil {
				return err
			}
			if s, ok := d.sessionForKey(l.Child.Key); ok {
				out = append(out, s)
			}
		}
		return nil
	}
	err := walk(key)
	return out, remote, err
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
	if err := d.endParent(ctx, key); err != nil {
		d.log.Warn("orch: queue parent_ended failed", "key", key, "err", err)
	}
	// The parent's node keeps a mirror of the link.
	if l, ok, err := d.orch.Link(ctx, api.Actor{Node: d.id, Key: key}); err == nil && ok && l.Parent.Node != d.id {
		if err := d.orch.Enqueue(ctx, l.Parent.Node, api.MethodOrchPeerLinkEnded, api.OrchPeerLinkEndedParams{ChildKey: key, EndedBy: by}); err != nil {
			d.log.Warn("orch: queue link_ended failed", "key", key, "node", l.Parent.Node, "err", err)
		}
	}
	if failed, err := d.orch.FailQueued(ctx, api.Actor{Node: d.id, Key: key}); err != nil {
		d.log.Warn("orch: fail queued failed", "key", key, "err", err)
	} else {
		d.pushStates(ctx, failed, api.MessageFailed)
	}
	open, err := d.orch.OpenInteractions(ctx, api.Actor{Node: d.id, Key: key})
	if err != nil {
		d.log.Warn("orch: list interactions failed", "child", key, "err", err)
	}
	for _, n := range open {
		d.withdrawInteraction(ctx, n, fmt.Sprintf("%s (%s) was killed by %s; its interaction %s ended.",
			sessionLabel(s), d.agentID(s), by, n.ID))
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
		if _, err := d.orch.ExpireOutbox(ctx, cutoff); err != nil {
			d.log.Warn("orch: expire outbox failed", "err", err)
		}
		seen := func(node string) bool { return d.roster.seenWithin(node, orchRetention, time.Now()) }
		if _, err := d.orch.ExpireLinks(ctx, cutoff, func(c api.Actor) bool { return c.Node == d.id && d.orchLive[c.Key] }, seen); err != nil {
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
		if err := d.endRemoteLink(ctx, key); err != nil {
			d.log.Warn("orch: end link of gone child failed", "key", key, "err", err)
			continue
		}
		if err := d.endParent(ctx, key); err != nil {
			d.log.Warn("orch: end links of gone parent failed", "key", key, "err", err)
			continue
		}
		failed, err := d.orch.FailQueued(ctx, api.Actor{Node: d.id, Key: key})
		if err != nil {
			d.log.Warn("orch: fail queued failed", "key", key, "err", err)
			continue
		}
		d.pushStates(ctx, failed, api.MessageFailed)
		delete(d.orchGone, key)
		delete(d.orchHeld, key)
		if len(failed) > 0 {
			d.orchChanged()
		}
	}
	d.orchLive = live
}

// endRemoteLink records that the child key, gone from this node, ended by
// itself, and tells its parent's node once: a link already ended is left as is.
func (d *Node) endRemoteLink(ctx context.Context, key string) error {
	l, ok, err := d.orch.Link(ctx, api.Actor{Node: d.id, Key: key})
	if err != nil || !ok || l.Child.Node != d.id || l.Parent.Node == d.id || l.EndedBy != "" {
		return err
	}
	if err := d.orch.Enqueue(ctx, l.Parent.Node, api.MethodOrchPeerLinkEnded, api.OrchPeerLinkEndedParams{ChildKey: key, EndedBy: "self"}); err != nil {
		return err
	}
	return d.orch.SetEndedBy(ctx, api.Actor{Node: d.id, Key: key}, "self")
}

// endParent tells each other node with a child of the parent key, gone from
// this node, that the parent ended, once: the mirror links record it.
func (d *Node) endParent(ctx context.Context, key string) error {
	links, err := d.orch.Children(ctx, api.Actor{Node: d.id, Key: key})
	if err != nil {
		return err
	}
	sent := map[string]bool{}
	for _, l := range links {
		if l.Parent.Node != d.id || l.Child.Node == d.id || l.ParentEnded || sent[l.Child.Node] {
			continue
		}
		if err := d.orch.Enqueue(ctx, l.Child.Node, api.MethodOrchPeerParentEnded, api.OrchPeerParentEndedParams{ParentKey: key}); err != nil {
			return err
		}
		sent[l.Child.Node] = true
	}
	if len(sent) == 0 {
		return nil
	}
	return d.orch.SetParentEnded(ctx, d.id, key)
}

// orchSeedGone marks the recipients on this node that ended while the node was
// down, and the parents whose end the child's node was not told, once startup
// discovery has settled. Later it marks the recipients of the messages peers
// queued for keys that are not live.
func (d *Node) orchSeedGone(ctx context.Context, live map[string]bool, now time.Time) {
	select {
	case <-d.orchSettled:
	default:
		return
	}
	if d.orchSeeded && !d.orchRecheck.Swap(false) {
		return
	}
	if !d.orchSeeded {
		d.orchRecheck.Store(false)
	}
	keys, err := d.orch.QueuedRecipients(ctx, d.id)
	if err != nil {
		if d.orchSeeded {
			d.orchRecheck.Store(true)
		}
		d.log.Warn("orch: list recipients failed", "err", err)
		return
	}
	if !d.orchSeeded {
		links, err := d.orch.Links(ctx)
		if err != nil {
			d.log.Warn("orch: list links failed", "err", err)
			return
		}
		// Seeded only after both reads: a read error retries on the next tick, so
		// no child that ended while the node was down stays unreported.
		d.orchSeeded = true
		for _, l := range links {
			if l.Child.Node == d.id && l.Parent.Node != d.id && l.EndedBy == "" {
				keys = append(keys, l.Child.Key)
			}
			if l.Parent.Node == d.id && l.Child.Node != d.id && !l.ParentEnded {
				keys = append(keys, l.Parent.Key)
			}
		}
	}
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
			ds[i] = d.delivery(ctx, m)
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
			if ok, err := d.orch.Transition(ctx, m.ID, api.MessageQueued, api.MessageDelivered); err != nil {
				d.log.Warn("orch: mark delivered failed", "id", m.ID, "err", err)
			} else if ok {
				d.pushState(ctx, m, api.MessageDelivered)
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
	return sessionLabel(s), d.agentID(s)
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

func (d *Node) delivery(ctx context.Context, m api.Message) orch.Delivery {
	dl := orch.Delivery{Msg: m}
	dl.From, dl.FromSession = d.senderLabel(m.From)
	if m.From.Node != d.id && !reservedKey(m.From.Key) {
		// This node cannot look up a session on another node, so it uses what
		// the creating node recorded, and the link for older messages.
		if m.FromLabel != "" {
			dl.From = m.FromLabel
		}
		dl.FromSession = m.FromSessionID
		if dl.FromSession == "" {
			dl.FromSession = d.remoteSessionID(ctx, m)
		}
	}
	if m.Child != nil {
		child, childSession := d.childActor(ctx, *m.Child)
		dl.Child, dl.ChildSession = d.senderLabel(child)
		if child.Node != d.id {
			dl.ChildSession = childSession
		}
	}
	return dl
}

// remoteSessionID is the composite session ID of m's sender on another node,
// as its link with m's recipient recorded it, or "" when unknown.
func (d *Node) remoteSessionID(ctx context.Context, m api.Message) string {
	if l, ok, err := d.orch.Link(ctx, m.From); err == nil && ok {
		return l.ChildSessionID
	}
	if l, ok, err := d.orch.Link(ctx, m.To); err == nil && ok && l.Parent == m.From {
		return l.ParentSessionID
	}
	return ""
}

// childActor is the child a notice is about, with its composite session ID
// when the child is on another node, as its link recorded it.
func (d *Node) childActor(ctx context.Context, child api.Actor) (api.Actor, string) {
	if l, ok, err := d.orch.Link(ctx, child); err == nil && ok {
		return l.Child, l.ChildSessionID
	}
	return child, ""
}

// insertMessage stores m and, when its recipient is on another node, queues
// its deliver to that node in the same transaction.
func (d *Node) insertMessage(ctx context.Context, m api.Message) (api.Message, error) {
	if m.To.Node == d.id {
		return d.orch.Insert(ctx, m)
	}
	return d.orch.InsertAndEnqueue(ctx, m, m.To.Node, api.MethodOrchPeerDeliver, func(m api.Message) any {
		return api.OrchPeerDeliverParams{Message: m}
	})
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
