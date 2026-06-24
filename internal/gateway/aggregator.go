package gateway

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// DefaultOfflineGrace is how long a disconnected node's sessions stay visible
// (marked Offline) before the aggregator removes them.
const DefaultOfflineGrace = 30 * time.Second

// Aggregator maintains the merged session view across all sources and routes
// control calls to the owning source. Its Snapshot/Subscribe pair mirrors the
// registry's pub/sub so a gateway client consumes it exactly like a node's
// registry — the wire protocol is identical.
type Aggregator struct {
	grace time.Duration

	mu       sync.Mutex
	sessions map[string]session.Session // composite id -> session
	sources  map[string]*srcState       // node id -> state
	subs     map[int]chan registry.Event
	nextSub  int
}

type srcState struct {
	src    Source
	stop   chan struct{}
	halted bool
	timer  *time.Timer // offline-removal timer; non-nil only while disconnected
}

// New returns an empty Aggregator. grace <= 0 uses DefaultOfflineGrace.
func New(grace time.Duration) *Aggregator {
	if grace <= 0 {
		grace = DefaultOfflineGrace
	}
	return &Aggregator{
		grace:    grace,
		sessions: make(map[string]session.Session),
		sources:  make(map[string]*srcState),
		subs:     make(map[int]chan registry.Event),
	}
}

// Nodes lists the currently connected nodes (id + label), sorted by label, so a
// client can choose a spawn target independent of which nodes already have
// sessions.
func (a *Aggregator) Nodes() []api.NodeInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]api.NodeInfo, 0, len(a.sources))
	for id, st := range a.sources {
		out = append(out, api.NodeInfo{NodeID: id, NodeLabel: st.src.Label()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].NodeLabel < out[j].NodeLabel })
	return out
}

// SoleNode returns the id of the only connected node, or "" when zero or more
// than one are connected. Lets the gateway default a spawn target when the
// client omits node_id and there's no ambiguity.
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

// AddSource registers a source and starts ingesting it. A source reconnecting
// under the same node id replaces the prior one (and cancels its pending
// removal), never duplicates it.
func (a *Aggregator) AddSource(src Source) {
	a.mu.Lock()
	if old, ok := a.sources[src.ID()]; ok {
		old.halt()
	}
	st := &srcState{src: src, stop: make(chan struct{})}
	a.sources[src.ID()] = st
	a.mu.Unlock()
	go a.ingest(st)
}

// halt stops a source's ingest goroutine and any pending removal timer. Caller
// holds a.mu.
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

func (a *Aggregator) ingest(st *srcState) {
	src := st.src
	nodeID, label := src.ID(), src.Label()
	events, cancel := src.Subscribe() // subscribe before snapshot: at-least-once, no loss
	defer cancel()
	a.reconcile(nodeID, label, src.Snapshot())

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				a.handleGone(st)
				return
			}
			a.applyEvent(nodeID, label, ev)
		case <-src.Done():
			a.handleGone(st)
			return
		case <-st.stop:
			return
		}
	}
}

// reconcile applies a source's full snapshot: upsert everything present, and drop
// any of that node's sessions that are no longer present (handles reconnect).
func (a *Aggregator) reconcile(nodeID, label string, snap []session.Session) {
	present := make(map[string]bool, len(snap))
	var changed []registry.Event

	a.mu.Lock()
	for _, s := range snap {
		s = withOrigin(s, nodeID, label)
		present[s.ID] = true
		a.sessions[s.ID] = s
		// Replay: this restates existing state (a connect/reconnect snapshot), so
		// downstream consumers like the push watcher don't treat it as a fresh
		// transition and re-notify.
		changed = append(changed, registry.Event{Type: registry.EventAdded, Session: s, Replay: true})
	}
	for id, s := range a.sessions {
		if s.NodeID == nodeID && !present[id] {
			delete(a.sessions, id)
			changed = append(changed, registry.Event{Type: registry.EventRemoved, Session: s})
		}
	}
	a.mu.Unlock()

	a.publishAll(changed)
}

func (a *Aggregator) applyEvent(nodeID, label string, ev registry.Event) {
	s := withOrigin(ev.Session, nodeID, label)
	a.mu.Lock()
	if ev.Type == registry.EventRemoved {
		delete(a.sessions, s.ID)
	} else {
		a.sessions[s.ID] = s
	}
	a.mu.Unlock()
	a.publish(registry.Event{Type: ev.Type, Session: s})
}

// handleGone marks a disconnected node's sessions offline and schedules their
// removal after the grace period.
func (a *Aggregator) handleGone(st *srcState) {
	nodeID := st.src.ID()
	a.mu.Lock()
	if a.sources[nodeID] != st { // already replaced by a reconnect
		a.mu.Unlock()
		return
	}
	var updated []session.Session
	for id, s := range a.sessions {
		if s.NodeID == nodeID && !s.Offline {
			s.Offline = true
			a.sessions[id] = s
			updated = append(updated, s)
		}
	}
	st.timer = time.AfterFunc(a.grace, func() { a.removeNode(nodeID, st) })
	a.mu.Unlock()

	for _, s := range updated {
		a.publish(registry.Event{Type: registry.EventUpdated, Session: s})
	}
}

func (a *Aggregator) removeNode(nodeID string, st *srcState) {
	a.mu.Lock()
	if a.sources[nodeID] != st { // reconnected before grace elapsed
		a.mu.Unlock()
		return
	}
	var removed []session.Session
	for id, s := range a.sessions {
		if s.NodeID == nodeID {
			delete(a.sessions, id)
			removed = append(removed, s)
		}
	}
	delete(a.sources, nodeID)
	a.mu.Unlock()

	for _, s := range removed {
		a.publish(registry.Event{Type: registry.EventRemoved, Session: s})
	}
}

// Snapshot returns the merged set of sessions across all sources.
func (a *Aggregator) Snapshot() []session.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]session.Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		out = append(out, s)
	}
	return out
}

// Subscribe returns the merged event stream and a cancel function, mirroring
// registry.Subscribe (buffered, drop-on-slow-consumer).
func (a *Aggregator) Subscribe() (<-chan registry.Event, func()) {
	ch := make(chan registry.Event, 64)
	a.mu.Lock()
	id := a.nextSub
	a.nextSub++
	a.subs[id] = ch
	a.mu.Unlock()
	return ch, func() {
		a.mu.Lock()
		if _, ok := a.subs[id]; ok {
			delete(a.subs, id)
			close(ch)
		}
		a.mu.Unlock()
	}
}

// Route forwards a control call to the source owning the composite session id in
// params, rewriting that id to the node-local form first.
func (a *Aggregator) Route(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	composite, err := sessionIDFromParams(params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	nodeID, localID, ok := session.SplitCompositeID(composite)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session id is not gateway-qualified: " + composite}
	}
	a.mu.Lock()
	st := a.sources[nodeID]
	a.mu.Unlock()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node: " + nodeID}
	}
	local, err := rewriteSessionID(params, localID)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	return st.src.Call(ctx, method, local)
}

// RouteToNode forwards a non-session-addressed control call (e.g. sessions.spawn)
// to a specific node by id, then rewrites any "session_id" in the result to its
// composite (nodeID:id) form so the client can address the new session.
func (a *Aggregator) RouteToNode(ctx context.Context, nodeID, method string, params json.RawMessage) (json.RawMessage, error) {
	a.mu.Lock()
	st := a.sources[nodeID]
	a.mu.Unlock()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node: " + nodeID}
	}
	res, err := st.src.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	localID, idErr := sessionIDFromParams(res)
	if idErr != nil || localID == "" {
		return res, nil // no session_id to composite; pass result through
	}
	return rewriteSessionID(res, session.CompositeID(nodeID, localID))
}

// FanoutResult is one source's reply to a broadcast Call.
type FanoutResult struct {
	NodeID string
	Label  string
	Result json.RawMessage
	Err    error
}

// Fanout calls method on every source and collects their raw results, tagged with
// the source's id and label. Used for reads that aggregate across machines (e.g.
// history projects). Sources are called sequentially; per-source errors are returned
// in-band so one unreachable node doesn't fail the whole call.
func (a *Aggregator) Fanout(ctx context.Context, method string, params json.RawMessage) []FanoutResult {
	a.mu.Lock()
	type ref struct {
		id, label string
		src       Source
	}
	refs := make([]ref, 0, len(a.sources))
	for id, st := range a.sources {
		refs = append(refs, ref{id: id, label: st.src.Label(), src: st.src})
	}
	a.mu.Unlock()

	out := make([]FanoutResult, 0, len(refs))
	for _, r := range refs {
		res, err := r.src.Call(ctx, method, params)
		out = append(out, FanoutResult{NodeID: r.id, Label: r.label, Result: res, Err: err})
	}
	return out
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

func (a *Aggregator) publish(ev registry.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ch := range a.subs {
		select {
		case ch <- ev:
		default: // drop for a slow subscriber
		}
	}
}

func (a *Aggregator) publishAll(evs []registry.Event) {
	for _, ev := range evs {
		a.publish(ev)
	}
}

// withOrigin stamps a session with its node origin and the composite id, and
// clears the offline flag (the session is, by definition, currently reported).
func withOrigin(s session.Session, nodeID, label string) session.Session {
	s.ID = session.CompositeID(nodeID, s.ID)
	s.NodeID = nodeID
	s.NodeLabel = label
	s.Offline = false
	return s
}

// sessionIDFromParams extracts the "session_id" field from raw JSON params.
func sessionIDFromParams(params json.RawMessage) (string, error) {
	var m struct {
		SessionID string `json:"session_id"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return "", err
		}
	}
	return m.SessionID, nil
}

// rewriteSessionID replaces only the "session_id" field, preserving every other
// field's raw bytes verbatim.
func rewriteSessionID(params json.RawMessage, id string) (json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return nil, err
		}
	}
	idRaw, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	m["session_id"] = idRaw
	return json.Marshal(m)
}
