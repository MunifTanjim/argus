package gateway

import (
	"sort"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// DefaultOfflineGrace is how long a disconnected node stays visible (marked
// Offline) before the aggregator removes it from the roster.
const DefaultOfflineGrace = 30 * time.Second

// Aggregator maintains the roster of connected nodes and their liveness state.
// The gateway is blind to sessions: E2E frames flow through the relay layer;
// the aggregator tracks only which nodes are online/offline/removed.
type Aggregator struct {
	grace time.Duration

	mu         sync.Mutex
	sources    map[string]*srcState // node id -> state
	rosterSubs map[int]chan api.NodeEvent
	nextRoster int
}

type srcState struct {
	src    Source
	stop   chan struct{}
	halted bool
	online bool
	timer  *time.Timer // offline-removal timer; non-nil only while disconnected
	beacon *api.Beacon // latest signed HEAD beacon (set on connect + beacon.offer; nil until first received)
}

// New returns an empty Aggregator. grace <= 0 uses DefaultOfflineGrace.
func New(grace time.Duration) *Aggregator {
	if grace <= 0 {
		grace = DefaultOfflineGrace
	}
	return &Aggregator{
		grace:      grace,
		sources:    make(map[string]*srcState),
		rosterSubs: make(map[int]chan api.NodeEvent),
	}
}

// Nodes lists the connected nodes sorted by label, so a client can pick a spawn
// target independent of which nodes already have sessions.
func (a *Aggregator) Nodes() []api.NodeInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]api.NodeInfo, 0, len(a.sources))
	for id, st := range a.sources {
		out = append(out, api.NodeInfo{
			ID:           id,
			Label:        st.src.Label(),
			Version:      st.src.Version(),
			Capabilities: st.src.Capabilities(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// AddSource registers a source and starts watching its liveness. A reconnect
// under the same node id replaces the prior source (cancelling its pending
// removal), never duplicates.
func (a *Aggregator) AddSource(src Source) {
	a.mu.Lock()
	evType := api.NodeEventAdded
	if old, ok := a.sources[src.ID()]; ok {
		old.halt()
		evType = api.NodeEventOnline // reconnect
	}
	st := &srcState{src: src, stop: make(chan struct{}), online: true, beacon: src.LatestBeacon()}
	a.sources[src.ID()] = st
	ev := api.NodeEvent{Type: evType, Node: descriptor(src.ID(), st)}
	a.mu.Unlock()
	a.publishRoster(ev)
	go a.watchLiveness(st)
}

// halt stops a source's watcher goroutine and pending removal timer. Caller holds a.mu.
func (st *srcState) halt() {
	if !st.halted {
		st.halted = true
		close(st.stop)
	}
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
}

// watchLiveness tracks a source's connection: waits for disconnect, then hands
// off to handleGone (roster Offline + grace → Removed). No session state is
// touched — the gateway is blind to sessions.
func (a *Aggregator) watchLiveness(st *srcState) {
	select {
	case <-st.src.Done():
		a.handleGone(st)
	case <-st.stop:
	}
}

// handleGone marks a disconnected node offline and schedules its removal after
// the grace period.
func (a *Aggregator) handleGone(st *srcState) {
	nodeID := st.src.ID()
	a.mu.Lock()
	if a.sources[nodeID] != st { // already replaced by a reconnect
		a.mu.Unlock()
		return
	}
	st.online = false
	st.timer = time.AfterFunc(a.grace, func() { a.removeNode(nodeID, st) })
	ev := api.NodeEvent{Type: api.NodeEventOffline, Node: descriptor(nodeID, st)}
	a.mu.Unlock()
	a.publishRoster(ev)
}

func (a *Aggregator) removeNode(nodeID string, st *srcState) {
	a.mu.Lock()
	if a.sources[nodeID] != st { // reconnected before grace elapsed
		a.mu.Unlock()
		return
	}
	delete(a.sources, nodeID)
	ev := api.NodeEvent{Type: api.NodeEventRemoved, Node: descriptor(nodeID, st)}
	a.mu.Unlock()
	a.publishRoster(ev)
}

// descriptor builds the roster view of a source. Caller holds a.mu.
func descriptor(id string, st *srcState) api.NodeDescriptor {
	return api.NodeDescriptor{
		ID:             id,
		Label:          st.src.Label(),
		Version:        st.src.Version(),
		Capabilities:   st.src.Capabilities(),
		IdentityPubKey: st.src.IdentityPubKey(),
		SignerPubKey:   st.src.SignerPubKey(),
		BeaconPubKey:   st.src.BeaconPubKey(),
		Beacon:         st.beacon,
		Online:         st.online,
	}
}

// UpdateBeacon records nodeID's latest signed HEAD beacon and broadcasts a
// node.event so subscribed clients see the fresh beacon. The gateway never
// verifies the beacon — it relays verbatim (Ed25519 can't be forged).
func (a *Aggregator) UpdateBeacon(nodeID string, b *api.Beacon) {
	a.mu.Lock()
	st, ok := a.sources[nodeID]
	if !ok {
		a.mu.Unlock()
		return
	}
	st.beacon = b
	ev := api.NodeEvent{Type: api.NodeEventBeacon, Node: descriptor(nodeID, st)}
	a.mu.Unlock()
	a.publishRoster(ev)
}

// Roster lists the connected nodes (online + within-grace offline) sorted by label,
// each with its identity pubkey and online flag — the client's E2E discovery view.
func (a *Aggregator) Roster() []api.NodeDescriptor {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]api.NodeDescriptor, 0, len(a.sources))
	for id, st := range a.sources {
		out = append(out, descriptor(id, st))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// SubscribeRoster returns the roster event stream and a cancel func, mirroring
// Subscribe (buffered, drop-on-slow-consumer).
func (a *Aggregator) SubscribeRoster() (<-chan api.NodeEvent, func()) {
	ch := make(chan api.NodeEvent, 64)
	a.mu.Lock()
	id := a.nextRoster
	a.nextRoster++
	a.rosterSubs[id] = ch
	a.mu.Unlock()
	return ch, func() {
		a.mu.Lock()
		if _, ok := a.rosterSubs[id]; ok {
			delete(a.rosterSubs, id)
			close(ch)
		}
		a.mu.Unlock()
	}
}

func (a *Aggregator) publishRoster(ev api.NodeEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ch := range a.rosterSubs {
		select {
		case ch <- ev:
		default: // drop for a slow subscriber
		}
	}
}
