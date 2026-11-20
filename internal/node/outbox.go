package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

const (
	// outboxRetryEvery bounds how long a queued push waits after a failed send.
	outboxRetryEvery = 30 * time.Second
	outboxBatch      = 64
	// outboxParkAfter is how many error replies from the peer park a push, so
	// one push that always fails cannot hold back the pushes behind it.
	outboxParkAfter = 10
	// crossNodeOrchVersion is the node-to-node orch protocol this node speaks.
	crossNodeOrchVersion = 2
)

// peerMethodVersion is the protocol version a peer needs to accept method.
func peerMethodVersion(method string) int {
	if method == api.MethodOrchPeerState {
		return 2
	}
	return 1
}

// outboxLoop sends the pushes queued for other nodes. It flushes on start, on
// every roster change, after each Enqueue, and every outboxEvery.
func (d *Node) outboxLoop(ctx context.Context) {
	events, cancel := d.roster.subscribe()
	defer cancel()
	tick := time.NewTicker(d.outboxEvery)
	defer tick.Stop()
	for {
		d.flushOutbox(ctx)
		select {
		case <-ctx.Done():
			return
		case <-events:
		case <-d.orch.Enqueued():
		case <-tick.C:
		}
		drainEvents(events)
	}
}

func drainEvents(events <-chan api.NodeEvent) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

// flushOutbox sends each online node its queued pushes in order, stopping at
// the first one that fails so a later push never overtakes it. Nodes are sent
// to at the same time, so a node that does not answer delays only its own
// pushes.
func (d *Node) flushOutbox(ctx context.Context) {
	nodes, err := d.orch.PendingNodes(ctx)
	if err != nil {
		d.log.Warn("orch: list outbox nodes failed", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, node := range nodes {
		if n, ok := d.roster.get(node); !ok || !n.Online {
			continue
		}
		wg.Go(func() { d.flushNode(ctx, node) })
	}
	wg.Wait()
}

// retiredRekey is dropped unsent: a peer on an older build would move the link.
const retiredRekey = "orch.peer.rekey"

func (d *Node) flushNode(ctx context.Context, node string) {
	for ctx.Err() == nil {
		items, err := d.orch.Pending(ctx, node, outboxBatch)
		if err != nil {
			d.log.Warn("orch: list outbox failed", "node", node, "err", err)
			return
		}
		for _, it := range items {
			if it.Method == retiredRekey {
				if err := d.orch.Ack(ctx, it.Seq); err != nil {
					d.log.Warn("orch: drop retired push failed", "node", node, "seq", it.Seq, "err", err)
					return
				}
				continue
			}
			if err := d.sendPush(ctx, node, it.Method, it.Params); err != nil {
				switch {
				case rejectedByPeer(err):
					// A retry gets the same answer, and must not hold back the pushes behind it.
					d.log.Warn("orch: push rejected, dropping it", "node", node, "method", it.Method, "seq", it.Seq, "err", err)
				case peerReplied(err):
					parked, ferr := d.orch.Fail(ctx, it.Seq, outboxParkAfter, d.outboxEvery)
					if ferr != nil || !parked {
						d.log.Warn("orch: push failed", "node", node, "method", it.Method, "seq", it.Seq, "attempt", it.Attempts+1, "err", err)
						return
					}
					d.log.Error("orch: push keeps failing, parked it", "node", node, "method", it.Method, "seq", it.Seq, "attempts", it.Attempts+1, "err", err)
					continue
				default:
					d.log.Warn("orch: push failed", "node", node, "method", it.Method, "seq", it.Seq, "err", err)
					return
				}
			}
			if err := d.orch.Ack(ctx, it.Seq); err != nil {
				d.log.Warn("orch: ack push failed", "node", node, "seq", it.Seq, "err", err)
				return
			}
		}
		if len(items) < outboxBatch {
			return
		}
	}
}

// rejectedByPeer reports whether err is the receiver's refusal of the push.
// peerPool.call returns the receiver's reply as a bare *api.RPCError. An RPC
// error from the gateway while opening the channel comes back wrapped with node
// context. Only a bare CodePushRejected or method-not-found reply is a refusal.
// Other error replies count toward parking the push; see peerReplied.
func rejectedByPeer(err error) bool {
	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) || err != error(rpcErr) {
		return false
	}
	return rpcErr.Code == api.CodePushRejected || rpcErr.Code == api.CodeMethodNotFound
}

// peerReplied reports whether err is an error reply from the peer itself, not
// a failure to reach it.
func peerReplied(err error) bool {
	var rpcErr *api.RPCError
	return errors.As(err, &rpcErr) && err == error(rpcErr)
}

// rejectPush marks err as a refusal that a retry cannot change.
func rejectPush(err error) error {
	return &api.RPCError{Code: api.CodePushRejected, Message: err.Error()}
}

func (d *Node) sendPush(ctx context.Context, node, method string, params json.RawMessage) error {
	n, ok := d.roster.get(node)
	if !ok || n.Capabilities.CrossNodeOrch < 1 {
		return fmt.Errorf("node %s does not support cross-node orchestration", node)
	}
	if v := peerMethodVersion(method); n.Capabilities.CrossNodeOrch < v {
		return rejectPush(fmt.Errorf("node %s speaks orch protocol %d; %s needs %d", node, n.Capabilities.CrossNodeOrch, method, v))
	}
	if d.outboxSend != nil {
		return d.outboxSend(ctx, node, method, params)
	}
	return d.peers.Load().call(ctx, node, method, params, nil)
}

// peerPush decodes a push from another node and returns that node's ID. Pushes
// are accepted only on a channel a peer node opened.
func peerPush[P any](ctx context.Context, d *Node, params json.RawMessage) (P, string, error) {
	var p P
	peer, onPeer := peerNodeFrom(ctx)
	if !onPeer {
		return p, "", rejectPush(errRemoteCallerDenied)
	}
	p, err := api.Decode[P](params)
	if err != nil {
		return p, "", rejectPush(err)
	}
	if d.orch == nil {
		return p, "", rejectPush(errOrchDisabled)
	}
	return p, peer, nil
}

func reservedKey(key string) bool {
	return key == api.ActorOwner || key == api.ActorArgus
}

// handleOrchPeerDeliver stores a message the peer created for an actor of this
// node. A message already stored is left as is.
func (d *Node) handleOrchPeerDeliver(ctx context.Context, params json.RawMessage) (any, error) {
	p, peer, err := peerPush[api.OrchPeerDeliverParams](ctx, d, params)
	if err != nil {
		return nil, err
	}
	m := p.Message
	if idNode, _, _ := strings.Cut(m.ID, ":"); idNode != peer || m.From.Node != peer {
		return nil, rejectPush(fmt.Errorf("%w: message %s is not from node %s", errRemoteCallerDenied, m.ID, peer))
	}
	if m.To.Node != d.id {
		return nil, rejectPush(fmt.Errorf("message %s is for node %s, not %s", m.ID, m.To.Node, d.id))
	}
	if m.Child != nil && m.Child.Node != peer {
		return nil, rejectPush(fmt.Errorf("%w: message %s is about a child on node %s, not %s", errRemoteCallerDenied, m.ID, m.Child.Node, peer))
	}
	if !reservedKey(m.To.Key) {
		m.State = api.MessageQueued
	}
	added, err := d.orch.Receive(ctx, m)
	if err != nil || !added {
		return nil, err
	}
	if !reservedKey(m.To.Key) {
		if _, live := d.sessionForKey(m.To.Key); !live {
			// A recipient that ended before this node saw it live is never
			// marked gone by the pass, so the message would stay queued.
			d.orchRecheck.Store(true)
		}
	}
	d.orchKick()
	d.orchChanged()
	return nil, nil
}

// handleOrchPeerWithdraw withdraws an open message the peer created, and gives its
// recipient the interaction_ended message when it already saw the message. A
// withdraw that names who answered an interaction marks the copy answered
// instead. An unknown message, or one that is no longer open, needs nothing.
func (d *Node) handleOrchPeerWithdraw(ctx context.Context, params json.RawMessage) (any, error) {
	p, peer, err := peerPush[api.OrchPeerWithdrawParams](ctx, d, params)
	if err != nil {
		return nil, err
	}
	m, ok, err := d.orch.Get(ctx, p.MessageID)
	if err != nil || !ok {
		return nil, err
	}
	if m.From.Node != peer {
		return nil, rejectPush(fmt.Errorf("%w: message %s is not from node %s", errRemoteCallerDenied, m.ID, peer))
	}
	if m.To.Node != d.id {
		return nil, rejectPush(fmt.Errorf("message %s is for node %s, not %s", m.ID, m.To.Node, d.id))
	}
	if by := p.AnsweredBy; by != nil {
		if by.Node != d.id || by.Key == "" {
			return nil, rejectPush(fmt.Errorf("%s:%s is not an actor on node %s", by.Node, by.Key, d.id))
		}
		d.claimCopy(ctx, m.ID, *by)
		return nil, nil
	}
	ended := p.Ended
	if ended == "" {
		ended = fmt.Sprintf("The interaction %s ended.", m.ID)
	}
	withdrawn, err := d.withdrawLocal(ctx, m, ended)
	if withdrawn {
		d.orchChanged()
	}
	return nil, err
}

// handleOrchPeerLinkEnded records how a child on the peer ended. A push names only
// a child of the peer, and an unknown link needs nothing.
func (d *Node) handleOrchPeerLinkEnded(ctx context.Context, params json.RawMessage) (any, error) {
	p, peer, err := peerPush[api.OrchPeerLinkEndedParams](ctx, d, params)
	if err != nil {
		return nil, err
	}
	child := api.Actor{Node: peer, Key: p.ChildKey}
	if _, ok, err := d.orch.Link(ctx, child); err != nil || !ok {
		return nil, err
	}
	if err := d.orch.SetEndedBy(ctx, child, p.EndedBy); err != nil {
		return nil, err
	}
	d.orchChanged()
	return nil, nil
}

// handleOrchPeerParentEnded records that a parent on the peer ended.
func (d *Node) handleOrchPeerParentEnded(ctx context.Context, params json.RawMessage) (any, error) {
	p, peer, err := peerPush[api.OrchPeerParentEndedParams](ctx, d, params)
	if err != nil {
		return nil, err
	}
	if p.ParentKey == "" || reservedKey(p.ParentKey) {
		return nil, rejectPush(fmt.Errorf("cannot end parent %q", p.ParentKey))
	}
	if err := d.orch.SetParentEnded(ctx, peer, p.ParentKey); err != nil {
		return nil, err
	}
	d.orchKick()
	d.orchChanged()
	return nil, nil
}

// pushState tells the node that created m that its message reached st here.
// A message this node created needs nothing.
func (d *Node) pushState(ctx context.Context, m api.Message, st api.MessageState) {
	if m.From.Node == d.id || m.From.Node == "" {
		return
	}
	if err := d.orch.Enqueue(context.WithoutCancel(ctx), m.From.Node, api.MethodOrchPeerState, api.OrchPeerStateParams{MessageID: m.ID, State: st}); err != nil {
		d.log.Warn("orch: queue state failed", "id", m.ID, "node", m.From.Node, "state", st, "err", err)
	}
}

func (d *Node) pushStates(ctx context.Context, ms []api.Message, st api.MessageState) {
	for _, m := range ms {
		d.pushState(ctx, m, st)
	}
}

// handleOrchPeerState moves this node's message to the state the recipient's
// node reports, forward only. An unknown message needs nothing: its deliver
// can still be on the way.
func (d *Node) handleOrchPeerState(ctx context.Context, params json.RawMessage) (any, error) {
	p, peer, err := peerPush[api.OrchPeerStateParams](ctx, d, params)
	if err != nil {
		return nil, err
	}
	m, ok, err := d.orch.Get(ctx, p.MessageID)
	if err != nil || !ok {
		return nil, err
	}
	if idNode, _, _ := strings.Cut(m.ID, ":"); idNode != d.id || m.From.Node != d.id {
		return nil, rejectPush(fmt.Errorf("%w: message %s was not created on node %s", errRemoteCallerDenied, m.ID, d.id))
	}
	if m.To.Node != peer {
		return nil, rejectPush(fmt.Errorf("%w: message %s is not for node %s", errRemoteCallerDenied, m.ID, peer))
	}
	moved, err := d.orch.Advance(ctx, m.ID, p.State)
	if moved {
		d.orchChanged()
	}
	return nil, err
}
