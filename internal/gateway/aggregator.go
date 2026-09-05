package gateway

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// DefaultOfflineGrace is how long a disconnected node stays visible
// (marked Offline) before the aggregator removes it.
const DefaultOfflineGrace = 30 * time.Second

// Aggregator tracks roster liveness across all sources. It emits online/offline/removed
// roster events; session state flows through the relay layer.
type Aggregator struct {
	grace time.Duration

	mu         sync.Mutex
	sources    map[string]*srcState
	rosterSubs map[int]chan api.NodeEvent
	nextRoster int
}

type srcState struct {
	src    Source
	stop   chan struct{}
	halted bool
	online bool
	timer  *time.Timer
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

// SoleNode returns the id of the only connected node, or "" when zero or more than
// one are connected.
func (a *Aggregator) SoleNode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sources) != 1 {
		return ""
	}
	for id := range a.sources {
		return id
	}
	return ""
}

// NodeLabel returns the human label for a registered node id, or "" if unknown.
func (a *Aggregator) NodeLabel(nodeID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st := a.sources[nodeID]; st != nil {
		return st.src.Label()
	}
	return ""
}

// AddSource registers a source and starts a liveness watcher. A reconnect under the
// same node id replaces the prior source (cancelling its pending removal).
func (a *Aggregator) AddSource(src Source) {
	a.mu.Lock()
	evType := api.NodeEventAdded
	if old, ok := a.sources[src.ID()]; ok {
		old.halt()
		evType = api.NodeEventOnline
	}
	st := &srcState{src: src, stop: make(chan struct{}), online: true}
	a.sources[src.ID()] = st
	ev := api.NodeEvent{Type: evType, Node: descriptor(src.ID(), st)}
	a.mu.Unlock()
	a.publishRoster(ev)
	go a.watchLiveness(st)
}

// halt stops a source's goroutine and pending removal timer. Caller holds a.mu.
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

// watchLiveness tracks a source's connection: waits for disconnect, then hands off to
// handleGone (roster Offline + grace → Removed).
func (a *Aggregator) watchLiveness(st *srcState) {
	select {
	case <-st.src.Done():
		a.handleGone(st)
	case <-st.stop:
	}
}

// handleGone marks a disconnected node offline in the roster, then schedules removal
// after the grace period.
func (a *Aggregator) handleGone(st *srcState) {
	nodeID := st.src.ID()
	a.mu.Lock()
	if a.sources[nodeID] != st {
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
	if a.sources[nodeID] != st {
		a.mu.Unlock()
		return
	}
	delete(a.sources, nodeID)
	ev := api.NodeEvent{Type: api.NodeEventRemoved, Node: descriptor(nodeID, st)}
	a.mu.Unlock()
	a.publishRoster(ev)
}

// Roster lists all known nodes (online + within-grace offline) sorted by label.
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

// SubscribeRoster returns the roster event stream and a cancel func.
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

// PublishTrustChanged broadcasts a trust-changed roster event to all subscribers,
// signalling that clients should pull the trust log promptly.
func (a *Aggregator) PublishTrustChanged() {
	a.publishRoster(api.NodeEvent{Type: api.NodeEventTrustChanged})
}

func (a *Aggregator) publishRoster(ev api.NodeEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ch := range a.rosterSubs {
		select {
		case ch <- ev:
		default:
		}
	}
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
		Online:         st.online,
	}
}

// nodeIDFromParams extracts the "node_id" field from raw JSON params.
func nodeIDFromParams(params json.RawMessage) (string, error) {
	var m struct {
		NodeID string `json:"node_id"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return "", err
		}
	}
	return m.NodeID, nil
}
