package node

import (
	"sort"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// rosterSubBuffer is the per-subscriber channel capacity.
const rosterSubBuffer = 64

// peerRoster mirrors the gateway's node roster as streamed by node.event. It
// is rebuilt from the initial "added" burst on every uplink connect and cleared
// by reset when the uplink closes.
type peerRoster struct {
	self func() string // local node ID; excluded from others()

	mu    sync.Mutex
	nodes map[string]api.NodeDescriptor
	// uplinks counts the added and online events per node. It changes each
	// time a node is seen on a new uplink and is never reset.
	uplinks map[string]uint64
	// seen is when each node was last known online. It survives reset, so an
	// uplink drop on this node does not make every node look long gone.
	seen    map[string]time.Time
	started time.Time
	subs    map[int]chan api.NodeEvent
	next    int
}

func newPeerRoster(self func() string) *peerRoster {
	return &peerRoster{
		self:    self,
		nodes:   map[string]api.NodeDescriptor{},
		uplinks: map[string]uint64{},
		seen:    map[string]time.Time{},
		started: time.Now(),
		subs:    map[int]chan api.NodeEvent{},
	}
}

// apply folds one gateway event into the roster and notifies subscribers.
// Events of unknown type, including trust-changed, are ignored.
func (r *peerRoster) apply(ev api.NodeEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := ev.Node.ID
	if id == "" {
		return
	}
	if prev, ok := r.nodes[id]; (ok && prev.Online) || ev.Node.Online || ev.Type == api.NodeEventOnline {
		r.seen[id] = time.Now()
	}
	switch ev.Type {
	case api.NodeEventAdded:
		n := ev.Node
		r.nodes[id] = n
		r.uplinks[id]++
	case api.NodeEventOnline:
		// A node that reconnects within the grace period comes back with a fresh
		// descriptor: new capabilities, identity key, or label.
		ev.Node.Online = true
		r.nodes[id] = ev.Node
		r.uplinks[id]++
	case api.NodeEventOffline:
		n, ok := r.nodes[id]
		if !ok {
			n = ev.Node
		}
		n.Online = false
		r.nodes[id] = n
		ev.Node = n
	case api.NodeEventRemoved:
		delete(r.nodes, id)
	default:
		return
	}
	r.publishLocked(ev)
}

func (r *peerRoster) get(nodeID string) (api.NodeDescriptor, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[nodeID]
	return n, ok
}

// uplink returns the count of uplinks nodeID was seen on. A different value
// than before means the node is now on a new uplink.
func (r *peerRoster) uplink(nodeID string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uplinks[nodeID]
}

// seenWithin reports whether nodeID is online, or was last known online, or
// this roster started, less than window before now.
func (r *peerRoster) seenWithin(nodeID string, window time.Duration, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[nodeID]; ok && n.Online {
		return true
	}
	at, ok := r.seen[nodeID]
	if !ok {
		at = r.started
	}
	return now.Sub(at) < window
}

// others returns the nodes other than the local one, online or not, sorted
// by ID.
func (r *peerRoster) others() []api.NodeDescriptor {
	self := ""
	if r.self != nil {
		self = r.self()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]api.NodeDescriptor, 0, len(r.nodes))
	for id, n := range r.nodes {
		if id != self {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// withLabel returns the IDs of the nodes labeled label, sorted.
func (r *peerRoster) withLabel(label string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id, n := range r.nodes {
		if n.Label == label {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// subscribe returns a buffered event channel and a cancel func. Publishing
// never blocks: when a subscriber's buffer is full the new event is dropped,
// so a slow subscriber may miss events and must treat each one as "something
// changed" and re-read the roster. The channel is not closed on cancel.
func (r *peerRoster) subscribe() (<-chan api.NodeEvent, func()) {
	ch := make(chan api.NodeEvent, rosterSubBuffer)
	r.mu.Lock()
	id := r.next
	r.next++
	r.subs[id] = ch
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

// reset empties the roster, emitting a removed event per node.
func (r *peerRoster) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := r.nodes[id]
		if n.Online {
			r.seen[id] = time.Now()
		}
		delete(r.nodes, id)
		r.publishLocked(api.NodeEvent{Type: api.NodeEventRemoved, Node: n})
	}
}

func (r *peerRoster) publishLocked(ev api.NodeEvent) {
	for _, ch := range r.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
