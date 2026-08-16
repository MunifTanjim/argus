// Package client is the end-to-end encrypted client transport: it discovers nodes
// through a blind gateway and talks to each over its own Noise channel, decrypting
// everything client-side. The gateway only relays opaque frames.
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

// Tunable timeouts.
var callTimeout = 30 * time.Second

// defaultOfflineGrace is how long a disconnected node's sessions stay visible
// (marked Offline) before the client drops them.
const defaultOfflineGrace = 30 * time.Second

// handshakeTimeoutNs is how long an initiator waits for the responder's msg2.
// Stored as nanoseconds in an atomic so SetHandshakeTimeoutForTest is race-free
// when background goroutines read it concurrently.
var handshakeTimeoutNs atomic.Int64

// nodeChan is one established E2E channel to a node.
type nodeChan struct {
	nodeID      string
	label       string
	identityPub []byte                      // copy of the node's Noise identity public key
	init        *e2e.Initiator              // consumed by the read loop when msg2 arrives
	ch          atomic.Pointer[api.Channel] // set after the handshake; read on the read loop
	sendMu      sync.Mutex                  // serializes Seal+SendRawFrame (enc-nonce order)
	hs          chan error                  // handshake outcome, sent once ch is established
}

type pendingReply struct {
	result json.RawMessage
	rpcErr *api.RPCError
}

// beaconMissThreshold is the number of consecutive unreconciled ticks for the
// same tip required before the equivocation flag is set.
const beaconMissThreshold = 2

// beaconDeliverForceInterval bounds how stale the courier's per-pair dedupe state
// may get: at most this often, a delivery is re-attempted even for a pair already
// recorded as delivered. It is wall-clock rather than a call count because delivery
// is arrival-driven — a call count would fire every few seconds on a busy fleet and
// almost never on an idle one, which is backwards. The window can be long: a target
// that rejects is never recorded as delivered, so genuine failures already retry on
// the next beacon or tick; this only covers a target that accepted and then lost the
// beacon without dropping its channel.
const beaconDeliverForceInterval = 30 * time.Minute

// beaconMissState tracks consecutive unreconciled ticks for a single node's beacon tip.
type beaconMissState struct {
	tip    []byte
	misses int
}

// E2EClient talks to nodes over end-to-end encrypted channels relayed by a blind
// gateway.
type E2EClient struct {
	peer   *api.Peer
	ready  chan struct{} // closed at end of NewE2EClientWithGate; synchronises m.peer init
	static e2e.KeyPair

	mu       sync.Mutex
	byNode   map[string]*nodeChan
	byChanID map[string]*nodeChan
	pending  map[uint64]chan pendingReply
	nextReq  uint64
	subNode  map[string]string // sub_id  -> nodeID (transcript.subscribe)
	termNode map[string]string // term_id -> nodeID (terminal.open)

	// Per-node session mirror, kept so the client can synthesize the events a node
	// that went away can no longer send. nodeSessions maps nodeID -> stamped session
	// id -> the last session seen for it; offlineTimers holds the per-node grace timer.
	nodeSessions  map[string]map[string]session.Session
	offlineTimers map[string]*time.Timer
	offlineGrace  time.Duration
	opening       map[string]chan struct{} // node id -> closed when its in-flight open finishes

	events chan api.Notification

	gate      *trustpin.Gate      // fail-closed state when unpinned on a locked network
	trust     *trustlog.SyncStore // locked-mode trust-log store; nil when off
	genesis   []byte              // this device's pinned trust root; nil when unpinned
	trustPath string              // locked-mode chain persist path; "" = no persistence
	trustCtx  context.Context     // cancelled on Close, stops the sync ticker
	trustStop context.CancelFunc
	// retainedEntries holds raw entries of every received branch, including those
	// that lost fork-choice. Its hashes form the Known offer on every sync.
	retainedEntries    *trustlog.EntryStore // guarded by mu
	lastDisjointLogged bool                 // guarded by mu
	lastUnplacedLogged int                  // last unplaced count that triggered a warning; 0 means no active warning; guarded by mu

	// Beacon cross-check state (guarded by mu).
	// beacons maps string(identityPub) to the latest verified beacon for each node.
	// beaconCtr tracks the last accepted counter per node for replay/stale detection.
	// beaconMiss tracks consecutive unreconciled ticks per node; cleared on reconcile
	// or when the node's counter advances (new beacon supersedes the miss streak).
	// everConnected records identity pubs for which a channel was successfully opened at
	// any point; used by checkBeaconConsistency to distinguish "once connected, now
	// offline" (skip: stale beacon) from "never connected" (check: legitimate beacon).
	beacons       map[string]api.Beacon
	beaconCtr     map[string]uint64
	beaconMiss    map[string]*beaconMissState
	everConnected map[string]bool // string(identityPub) → true, never deleted
	equivocation  bool            // set permanently once divergence is detected

	// delivered[sourceNodeID][targetNodeID] = counter of the last beacon successfully
	// couriered from source to target. Guards against re-delivering the same beacon
	// on every tick; a higher counter resets the skip. A full re-courier is forced
	// once per beaconDeliverForceInterval (lastForcedDeliver stamps it). Guarded by mu.
	delivered         map[string]map[string]uint64
	lastForcedDeliver time.Time

	beaconKnownTip []byte          // caches known-set key; guarded by mu
	beaconKnown    map[string]bool // resolved chain entry-hash set for beacon checks

	// triggerMu guards the rate-limiter state for beacon-triggered work. Kept
	// separate from mu so notification handling never contends with the main lock.
	// triggerWantPull accumulates across coalesced requests: a courier-only trigger
	// must not cancel a pull a suppressed one already asked for.
	triggerMu             sync.Mutex
	lastTriggeredPull     time.Time
	triggeredPullInFlight bool
	triggerPending        bool
	triggerWantPull       bool
	triggerTimer          *time.Timer
}

// NewE2EClientWithGate is NewE2EClientWithIdentity plus a caller-owned quarantine
// gate. The reconnecting client shares one gate across reconnects so an unpinned
// client cannot be un-quarantined by a dropped connection.
func NewE2EClientWithGate(conn net.Conn, static e2e.KeyPair, genesisHash []byte, chainPath string, gate *trustpin.Gate) (*E2EClient, error) {
	m := &E2EClient{
		static:        static,
		gate:          gate,
		ready:         make(chan struct{}),
		byNode:        map[string]*nodeChan{},
		byChanID:      map[string]*nodeChan{},
		pending:       map[uint64]chan pendingReply{},
		subNode:       map[string]string{},
		termNode:      map[string]string{},
		nodeSessions:  map[string]map[string]session.Session{},
		offlineTimers: map[string]*time.Timer{},
		opening:       map[string]chan struct{}{},
		offlineGrace:  defaultOfflineGrace,
		events:        make(chan api.Notification, 256),
		beacons:       map[string]api.Beacon{},
		beaconCtr:     map[string]uint64{},
		beaconMiss:    map[string]*beaconMissState{},
		everConnected: map[string]bool{},
		delivered:     map[string]map[string]uint64{},
	}
	if genesisHash != nil {
		m.trust = trustlog.NewSyncStore(genesisHash)
		m.genesis = append([]byte(nil), genesisHash...)
		m.trustPath = chainPath
		// Seed from a persisted chain so a reconnect resumes from the last verified
		// tip (genesis-pinned Ingest rejects a rolled-back/tampered file).
		if chainPath != "" {
			if b, err := os.ReadFile(chainPath); err == nil && len(b) > 0 {
				_, _ = m.trust.Ingest(b)
			}
		}
	}
	// Keepalive: without it a half-open gateway link (NAT timeout, a phone changing
	// networks) never fires Done, so the supervisor never reconnects and every
	// gateway-native Call blocks forever.
	//
	// api.NewPeer starts its read loop before returning; a node.event arriving on
	// the connection can call onPeerNotify → adoptNode → openChannel before the
	// assignment below completes. close(m.ready) establishes the happens-before
	// that openChannel waits on, so it always reads a fully-set m.peer.
	m.peer = api.NewPeer(conn, api.PeerOptions{
		OnRelayFrame:              m.onRelayFrame,
		OnNotify:                  m.onPeerNotify,
		KeepaliveInterval:         api.DefaultKeepaliveInterval,
		KeepaliveTimeout:          api.DefaultKeepaliveTimeout,
		KeepaliveFailureThreshold: api.DefaultKeepaliveFailures,
	})
	m.trustCtx, m.trustStop = context.WithCancel(context.Background())
	close(m.ready)
	return m, nil
}

// NewE2EClientWithIdentity wraps a gateway connection with a caller-provided static
// identity (persisted, for locked mode) and optional pinned genesis. chainPath, if
// non-empty, seeds the trust store from disk on construction and persists it on each
// advance (genesis-pinned Ingest rejects a rolled-back or tampered file).
func NewE2EClientWithIdentity(conn net.Conn, static e2e.KeyPair, genesisHash []byte, chainPath string) (*E2EClient, error) {
	return NewE2EClientWithGate(conn, static, genesisHash, chainPath, &trustpin.Gate{})
}

// Quarantined reports whether this client saw a trust log it has no pin to
// verify, in which case it opens no node channels until `argus lock pin` runs.
func (m *E2EClient) Quarantined() bool { return m.gate.Tripped() }

// NewE2EClient wraps a gateway connection, wiring the relay-frame demux. Generates
// an ephemeral client Noise static key.
func NewE2EClient(conn net.Conn) (*E2EClient, error) {
	static, err := e2e.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return NewE2EClientWithIdentity(conn, static, nil, "")
}

// NewE2EClientWithGenesis is NewE2EClient plus a pinned trust-log genesis hash, so
// the client syncs and verifies the network's trust-log chain. Pass nil hash to
// disable trust-log sync (equivalent to NewE2EClient).
func NewE2EClientWithGenesis(conn net.Conn, genesisHash []byte) (*E2EClient, error) {
	static, err := e2e.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return NewE2EClientWithIdentity(conn, static, genesisHash, "")
}

// Done is closed when the underlying gateway connection drops.
func (m *E2EClient) Done() <-chan struct{} { return m.peer.Done() }

// Events is the aggregated node-notification stream.
func (m *E2EClient) Events() <-chan api.Notification { return m.events }

// Close tears down the gateway connection.
func (m *E2EClient) Close() error {
	if m.trustStop != nil {
		m.trustStop()
	}
	m.mu.Lock()
	for id, t := range m.offlineTimers {
		t.Stop()
		delete(m.offlineTimers, id)
	}
	m.mu.Unlock()
	m.triggerMu.Lock()
	m.triggerPending = false
	if m.triggerTimer != nil {
		m.triggerTimer.Stop()
	}
	m.triggerMu.Unlock()
	return m.peer.Close()
}

// Connect discovers nodes and opens an E2E channel to each authorized node. In
// locked mode it pulls the trust log first and silently skips nodes whose identity
// is not authorized (fail-closed: an empty store opens nothing).
func (m *E2EClient) Connect() error {
	var roster api.NodesListResult
	if err := m.peer.Call(api.MethodNodesList, nil, &roster); err != nil {
		return fmt.Errorf("client: nodes.list: %w", err)
	}
	// Seed the initial beacon map from the roster snapshot (before the trust-log pull
	// so that the first syncTrustLog cross-check already has whatever beacons the
	// gateway advertises on the roster).
	for _, nd := range roster.Nodes {
		m.ingestBeaconFromDescriptor(nd) // the sync below covers the pull and the courier
	}
	// Locked mode: pull the trust log before deciding which nodes to open. The store
	// is already disk-seeded (last verified HEAD), so enforcement is correct even if
	// this pull fails.
	if m.trust != nil {
		m.syncTrustLog()
	} else {
		m.detectUnpinnedChain()
	}
	for _, nd := range roster.Nodes {
		if err := m.openIfEligible(nd); err != nil {
			// One unreachable node must not abort the whole session; skip it and keep
			// aggregating the rest (a later reconnect/refresh retries it).
			log.Printf("client: skipping node %s: open channel failed: %v", nd.ID, err)
		}
	}
	if m.trust != nil {
		go m.trustSyncLoop()
	} else {
		go m.unpinnedWatchLoop()
	}
	return nil
}

// openIfEligible opens a channel to nd unless it is offline, keyless, or (in
// locked mode) unauthorized — the silent-skip cases are not errors. Returns the
// open error otherwise.
func (m *E2EClient) openIfEligible(nd api.NodeDescriptor) error {
	if m.gate.Tripped() {
		return nil
	}
	if !nd.Online {
		return nil // offline within-grace node: no live relay peer, relay.open would fail
	}
	if nd.IdentityPubKey == "" {
		return nil // no key: cannot open an E2E channel to this node
	}
	pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	if err != nil {
		return nil // bad key: skip (fail-closed; also can't open a channel anyway)
	}
	if m.trust != nil && !m.trust.Disabled() && !m.trust.DeviceAuthorized(pub) {
		return nil // unauthorized node: silent exclusion (fail-closed)
	}
	// A node is registered in byNode only once its handshake finishes, so the
	// in-flight map is what keeps a roster event landing mid-handshake from opening
	// a second channel — which would replay the node's whole snapshot. A second
	// caller waits for the winner instead of skipping, so Connect never returns
	// before the channel an adopting goroutine is already opening exists.
	m.mu.Lock()
	if _, open := m.byNode[nd.ID]; open {
		m.mu.Unlock()
		return nil
	}
	if inflight, ok := m.opening[nd.ID]; ok {
		m.mu.Unlock()
		<-inflight
		return nil
	}
	done := make(chan struct{})
	m.opening[nd.ID] = done
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.opening, nd.ID)
		m.mu.Unlock()
		close(done)
	}()
	return m.openChannel(nd, pub)
}

// openChannel runs relay.open + the Noise IK initiator handshake for one node.
// pub is the decoded identity public key, already checked by Connect.
func (m *E2EClient) openChannel(nd api.NodeDescriptor, pub []byte) error {
	// Wait for NewE2EClientWithGate to finish: api.NewPeer starts its read loop
	// before returning, so a node.event can spawn this goroutine via onPeerNotify
	// → adoptNode before m.peer is assigned. The channel close is the
	// happens-before guarantee.
	<-m.ready
	var res api.RelayOpenResult
	if err := m.peer.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: nd.ID}, &res); err != nil {
		return err
	}
	init, msg1, err := e2e.NewInitiator(m.static, pub, api.ChannelPrologue(nd.ID, res.ChanID))
	if err != nil {
		return err
	}
	nc := &nodeChan{nodeID: nd.ID, label: nd.Label, identityPub: append([]byte(nil), pub...), init: init, hs: make(chan error, 1)}
	m.mu.Lock()
	m.byChanID[res.ChanID] = nc
	m.mu.Unlock()

	frame, err := api.MarshalHandshakeFrame(res.ChanID, msg1)
	if err != nil {
		return err
	}
	if err := m.peer.SendRawFrame(frame); err != nil {
		return err
	}
	select {
	case err := <-nc.hs:
		return err
	case <-m.peer.Done():
		return fmt.Errorf("connection closed during handshake")
	case <-time.After(time.Duration(handshakeTimeoutNs.Load())):
		return fmt.Errorf("handshake timeout")
	}
}

// finishHandshake completes the Noise initiator for nc from an inbound msg2 frame
// and establishes its channel. Runs on the Peer read loop.
func (m *E2EClient) finishHandshake(nc *nodeChan, f api.RelayFrame) error {
	msg2, err := api.HandshakeFromFrame(f)
	if err != nil {
		return err
	}
	sess, err := nc.init.Finish(msg2)
	if err != nil {
		return err
	}
	nc.ch.Store(api.NewChannel(f.Route.ChanID, sess))
	return nil
}

// onRelayFrame demuxes inbound relay frames on the Peer read loop. It Opens every
// sealed frame inline in arrival order (shared dec-nonce) and never blocks.
func (m *E2EClient) onRelayFrame(_ *api.Peer, f api.RelayFrame) {
	m.mu.Lock()
	nc := m.byChanID[f.Route.ChanID]
	m.mu.Unlock()
	if nc == nil {
		return
	}
	if f.Method == api.MethodE2EHandshake {
		// Finish inline: a node that pushes state the instant its channel is up
		// (registry snapshot) sends the next frame right behind msg2, and a frame
		// arriving before ch is stored would be dropped — losing that state and
		// desyncing the shared dec-nonce for everything after it.
		err := m.finishHandshake(nc, f)
		if err == nil {
			// Register in byNode here, before signalling nc.hs. The read loop is
			// single-threaded, so byNode is written before the next frame (the node's
			// session snapshot) can be processed — ensuring any consumer that reacts
			// to the resulting session.event already sees this node in byNode.
			m.mu.Lock()
			if m.gate.Tripped() {
				delete(m.byChanID, f.Route.ChanID)
			} else {
				m.byNode[nc.nodeID] = nc
				m.everConnected[string(nc.identityPub)] = true
			}
			m.mu.Unlock()
		}
		select {
		case nc.hs <- err:
		default:
		}
		return
	}
	ch := nc.ch.Load()
	if ch == nil {
		return // frame before the handshake completed
	}
	switch {
	case f.ID != nil && f.Method == "": // response
		result, rpcErr, err := ch.OpenResponse(f)
		if err != nil {
			return // decrypt failure (tamper/desync): drop
		}
		var id uint64
		if err := json.Unmarshal(*f.ID, &id); err != nil {
			return
		}
		m.mu.Lock()
		waiter := m.pending[id]
		delete(m.pending, id)
		m.mu.Unlock()
		if waiter != nil {
			waiter <- pendingReply{result: result, rpcErr: rpcErr}
		}
	case f.Method != "" && f.ID == nil: // notification
		params, err := ch.OpenParams(f)
		if err != nil {
			return
		}
		if f.Method == api.MethodSessionEvent {
			params = stampEvent(params, nc.nodeID, nc.label)
			m.trackSessionEvent(nc.nodeID, params)
		} else if f.Method == api.MethodTasksChanged {
			params = stampTasksChanged(params, nc.nodeID)
		}
		select {
		case m.events <- api.Notification{Method: f.Method, Params: params}:
		default: // buffered; drop for a stalled consumer rather than wedge the read loop
		}
	}
}

func (m *E2EClient) forget(id uint64) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

// Call routes a client RPC over the E2E channels: fanout+stamp for lists,
// composite-split for session-addressed, node_id routing for node-addressed,
// handle routing for terminal calls, per-node fanout for push register/unregister/test,
// and passthrough for gateway-native methods (server.info/nodes.list/push.vapidKey/clients.*).
func (m *E2EClient) Call(method string, params, out any) error {
	raw, err := toRaw(params)
	if err != nil {
		return err
	}
	switch {
	case method == api.MethodSessionsList || method == api.MethodSessionsRefresh:
		return m.fanoutSessions(method, raw, out)
	case method == api.MethodSessionsHistoryProjects:
		return m.fanoutHistoryProjects(raw, out)
	case sessionAddressed[method]:
		return m.routeBySession(method, raw, out)
	case nodeAddressed[method]:
		return m.routeByNode(method, raw, out)
	case method == api.MethodTranscriptUnsubscribe:
		id, _ := subIDFromParams(raw)
		return m.routeByHandle(m.subNode, id, method, raw, out)
	case terminalHandleAddressed[method]:
		id, _ := termIDFromParams(raw)
		return m.routeByHandle(m.termNode, id, method, raw, out)
	case pushFanoutMethods[method]:
		return m.fanoutPush(method, raw, out)
	default: // gateway-native: server.info, nodes.list, ping, push.vapidKey, clients.*
		return m.peer.Call(method, raw, out)
	}
}

func toRaw(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if r, ok := params.(json.RawMessage); ok {
		return r, nil
	}
	return json.Marshal(params)
}

// channelsSnapshot returns the current node channels under the lock.
func (m *E2EClient) channelsSnapshot() []*nodeChan {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*nodeChan, 0, len(m.byNode))
	for _, nc := range m.byNode {
		out = append(out, nc)
	}
	return out
}

// reevaluateChannels removes channels to nodes no longer authorized by the trust log.
// A nil or Disabled store closes nothing. Does not tear down the gateway peer connection.
// Also prunes beacon state for each dropped node so stale cached beacons cannot
// accumulate misses and false-positive the equivocation flag.
func (m *E2EClient) reevaluateChannels() {
	if m.gate.Tripped() {
		m.mu.Lock()
		var drop []*nodeChan
		for _, nc := range m.byNode {
			drop = append(drop, nc)
		}
		for _, nc := range drop {
			delete(m.byNode, nc.nodeID)
			if ch := nc.ch.Load(); ch != nil {
				delete(m.byChanID, ch.ID())
			}
			key := string(nc.identityPub)
			delete(m.beacons, key)
			delete(m.beaconCtr, key)
			delete(m.beaconMiss, key)
			delete(m.delivered, nc.nodeID)
			for srcID := range m.delivered {
				delete(m.delivered[srcID], nc.nodeID)
			}
		}
		m.mu.Unlock()
		return
	}
	if m.trust == nil || m.trust.Disabled() {
		return
	}
	m.mu.Lock()
	var drop []*nodeChan
	for _, nc := range m.byNode {
		if !m.trust.DeviceAuthorized(nc.identityPub) {
			drop = append(drop, nc)
		}
	}
	for _, nc := range drop {
		delete(m.byNode, nc.nodeID)
		if ch := nc.ch.Load(); ch != nil {
			delete(m.byChanID, ch.ID())
		}
		// Prune beacon state so the revoked node's stale tip cannot accumulate misses.
		key := string(nc.identityPub)
		delete(m.beacons, key)
		delete(m.beaconCtr, key)
		delete(m.beaconMiss, key)
		delete(m.delivered, nc.nodeID)
		for srcID := range m.delivered {
			delete(m.delivered[srcID], nc.nodeID)
		}
	}
	m.mu.Unlock()
}

// fanoutSessions calls method on every node channel, stamps composite origin, merges.
func (m *E2EClient) fanoutSessions(method string, raw json.RawMessage, out any) error {
	chans := m.channelsSnapshot()
	type res struct {
		sessions []session.Session
		nodeID   string
		label    string
	}
	results := make([]res, len(chans))
	var wg sync.WaitGroup
	for i, nc := range chans {
		i, nc := i, nc
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ss []session.Session
			if err := m.callNode(nc.nodeID, method, raw, &ss); err != nil {
				// One bad node doesn't fail the whole list, but it must not vanish
				// silently either: an all-nodes failure is otherwise indistinguishable
				// from an empty fleet.
				log.Printf("client: warn: %s on node %s failed: %v", method, nc.nodeID, err)
				return
			}
			results[i] = res{sessions: ss, nodeID: nc.nodeID, label: nc.label}
		}()
	}
	wg.Wait()
	merged := []session.Session{}
	for _, r := range results {
		for _, s := range r.sessions {
			merged = append(merged, withOrigin(s, r.nodeID, r.label))
		}
	}
	return assign(out, merged)
}

// fanoutHistoryProjects fans out, stamps NodeID/NodeLabel, newest-first.
func (m *E2EClient) fanoutHistoryProjects(raw json.RawMessage, out any) error {
	chans := m.channelsSnapshot()
	all := []session.HistoryProject{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, nc := range chans {
		nc := nc
		wg.Add(1)
		go func() {
			defer wg.Done()
			var projects []session.HistoryProject
			if err := m.callNode(nc.nodeID, api.MethodSessionsHistoryProjects, raw, &projects); err != nil {
				log.Printf("client: warn: sessions.historyProjects on node %s failed: %v", nc.nodeID, err)
				return
			}
			for i := range projects {
				projects[i].NodeID = nc.nodeID
				projects[i].NodeLabel = nc.label
			}
			mu.Lock()
			all = append(all, projects...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.SliceStable(all, func(i, j int) bool { return all[i].LastActivity > all[j].LastActivity })
	return assign(out, all)
}

// fanoutPush fans out a push.register/unregister/test call to every connected
// node channel (each node holds its own device store). Succeeds if at least one
// node accepted; returns an aggregated error if all fail. For push.test,
// surfaces CodePushGone only when every node reported gone.
func (m *E2EClient) fanoutPush(method string, raw json.RawMessage, out any) error {
	chans := m.channelsSnapshot()
	if len(chans) == 0 {
		return m.peer.Call(method, raw, out)
	}
	type nodeResult struct {
		result json.RawMessage
		err    error
	}
	results := make([]nodeResult, len(chans))
	var wg sync.WaitGroup
	for i, nc := range chans {
		i, nc := i, nc
		wg.Add(1)
		go func() {
			defer wg.Done()
			var res json.RawMessage
			err := m.callNode(nc.nodeID, method, raw, &res)
			results[i] = nodeResult{result: res, err: err}
		}()
	}
	wg.Wait()

	var lastResult json.RawMessage
	successCount := 0
	var errs []error
	goneCount := 0
	for _, r := range results {
		if r.err == nil {
			successCount++
			lastResult = r.result
		} else {
			errs = append(errs, r.err)
			if rpcErr, ok := r.err.(*api.RPCError); ok && rpcErr.Code == api.CodePushGone {
				goneCount++
			}
		}
	}

	if successCount > 0 {
		return assignRaw(out, lastResult)
	}
	// All nodes failed.
	if method == api.MethodPushTest && goneCount == len(results) {
		return &api.RPCError{Code: api.CodePushGone, Message: "push target gone"}
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return fmt.Errorf("push fan-out: all %d nodes failed: %w", len(errs), errs[0])
}

// routeBySession splits the composite session_id, routes to that node with the
// node-local id, and records sub_id/term_id -> node for subscribe/open.
func (m *E2EClient) routeBySession(method string, raw json.RawMessage, out any) error {
	composite, err := sessionIDFromParams(raw)
	if err != nil {
		return err
	}
	nodeID, localID, ok := session.SplitCompositeID(composite)
	if !ok {
		return &api.RPCError{Code: api.CodeInvalidRequest, Message: "session id is not gateway-qualified: " + composite}
	}
	local, err := rewriteSessionID(raw, localID)
	if err != nil {
		return err
	}
	if err := m.callNode(nodeID, method, local, out); err != nil {
		return err
	}
	// Remember the handle -> node so later handle-addressed calls route correctly.
	switch method {
	case api.MethodTranscriptSubscribe:
		if id, _ := subIDFromParams(raw); id != "" {
			m.mu.Lock()
			m.subNode[id] = nodeID
			m.mu.Unlock()
		}
	case api.MethodTerminalOpen:
		if id, _ := termIDFromParams(raw); id != "" {
			m.mu.Lock()
			m.termNode[id] = nodeID
			m.mu.Unlock()
		}
	}
	return nil
}

// routeByNode routes by an explicit node_id (or the sole node) and composites any
// session_id in the result for spawn/resume; stamps history-session pages.
func (m *E2EClient) routeByNode(method string, raw json.RawMessage, out any) error {
	nodeID, _ := nodeIDFromParams(raw)
	if nodeID == "" {
		if nodeID = m.soleNode(); nodeID == "" {
			return &api.RPCError{Code: api.CodeInvalidRequest, Message: method + " requires node_id"}
		}
	}
	if compositeResultMethods[method] {
		var res json.RawMessage
		if err := m.callNode(nodeID, method, raw, &res); err != nil {
			return err
		}
		if localID, e := sessionIDFromParams(res); e == nil && localID != "" {
			rewritten, err := rewriteSessionID(res, session.CompositeID(nodeID, localID))
			if err != nil {
				return err
			}
			return assignRaw(out, rewritten)
		}
		return assignRaw(out, res)
	}
	if method == api.MethodSessionsHistorySessions {
		var page session.HistorySessionPage
		if err := m.callNode(nodeID, method, raw, &page); err != nil {
			return err
		}
		label := m.nodeLabel(nodeID)
		for i := range page.Items {
			page.Items[i].NodeID = nodeID
			page.Items[i].NodeLabel = label
		}
		return assign(out, page)
	}
	return m.callNode(nodeID, method, raw, out)
}

// routeByHandle routes a terminal/transcript handle call to the node the handle
// was opened/subscribed on.
func (m *E2EClient) routeByHandle(table map[string]string, id, method string, raw json.RawMessage, out any) error {
	if id == "" {
		return &api.RPCError{Code: api.CodeInvalidRequest, Message: method + " requires a handle id"}
	}
	m.mu.Lock()
	nodeID := table[id]
	m.mu.Unlock()
	if nodeID == "" {
		return &api.RPCError{Code: api.CodeInvalidRequest, Message: method + ": unknown handle " + id}
	}
	return m.callNode(nodeID, method, raw, out)
}

func (m *E2EClient) soleNode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.byNode) != 1 {
		return ""
	}
	for id := range m.byNode {
		return id
	}
	return ""
}

func (m *E2EClient) nodeLabel(nodeID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if nc := m.byNode[nodeID]; nc != nil {
		return nc.label
	}
	return ""
}

// assign marshals v and unmarshals into out (out may be nil).
func assign(out any, v any) error {
	if out == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// assignRaw unmarshals raw JSON into out (out may be nil).
func assignRaw(out any, raw json.RawMessage) error {
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// callNode issues a sealed request to a node's channel and waits for the correlated
// response.
func (m *E2EClient) callNode(nodeID, method string, params, out any) error {
	m.mu.Lock()
	nc := m.byNode[nodeID]
	m.mu.Unlock()
	if nc == nil {
		return fmt.Errorf("client: no channel to node %q", nodeID)
	}
	ch := nc.ch.Load()
	if ch == nil {
		return fmt.Errorf("client: channel to node %q not established", nodeID)
	}

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		paramsRaw = b
	}

	id := atomic.AddUint64(&m.nextReq, 1)
	idRaw := json.RawMessage(strconv.FormatUint(id, 10))
	replyCh := make(chan pendingReply, 1)
	m.mu.Lock()
	m.pending[id] = replyCh
	m.mu.Unlock()

	nc.sendMu.Lock()
	frame, err := ch.SealRequestFrame(&idRaw, method, nodeID, paramsRaw)
	if err == nil {
		err = m.peer.SendRawFrame(frame)
	}
	nc.sendMu.Unlock()
	if err != nil {
		m.forget(id)
		return err
	}

	select {
	case reply := <-replyCh:
		if reply.rpcErr != nil {
			return reply.rpcErr
		}
		if out != nil && len(reply.result) > 0 {
			return json.Unmarshal(reply.result, out)
		}
		return nil
	case <-m.peer.Done():
		m.forget(id)
		return fmt.Errorf("client: connection closed")
	case <-time.After(callTimeout):
		m.forget(id)
		return fmt.Errorf("client: call timeout")
	}
}

// clientTriggerIntervalNs bounds how often an arriving beacon can cause a trust-log
// pull and a courier run. Beacons are relayed by an untrusted gateway, so they must
// not be able to amplify into unbounded work: suppressed arrivals are coalesced into
// one deferred run, never queued.
// Stored as nanoseconds in an atomic so setTriggerIntervalForTest is race-free when
// background goroutines read it concurrently.
var clientTriggerIntervalNs atomic.Int64

func minClientTriggeredPullInterval() time.Duration {
	return time.Duration(clientTriggerIntervalNs.Load())
}

// setTriggerIntervalForTest overrides the beacon-trigger rate-limit window. Test-only.
func setTriggerIntervalForTest(d time.Duration) { clientTriggerIntervalNs.Store(int64(d)) }

// clientTrustSyncInterval is the BACKSTOP for trust-log convergence, not the primary
// path: a change normally arrives via trustlog.changed (node) or NodeEventBeacon
// (client) within milliseconds. It also bounds how long an UNPINNED device stays
// open on a locked network before quarantining, so shortening it is safe and
// lengthening it widens that window. Do not tune this without reading
// detectUnpinnedChain.
// Stored as nanoseconds in an atomic so SetTrustSyncIntervalForTest is race-free
// when background goroutines read it concurrently.
var clientTrustSyncInterval atomic.Int64

func init() {
	clientTrustSyncInterval.Store(int64(5 * time.Minute))
	handshakeTimeoutNs.Store(int64(10 * time.Second))
	clientTriggerIntervalNs.Store(int64(5 * time.Second))
}

// SetHandshakeTimeoutForTest overrides the Noise handshake timeout. Test-only.
func SetHandshakeTimeoutForTest(d time.Duration) { handshakeTimeoutNs.Store(int64(d)) }

// SetTrustSyncIntervalForTest overrides the client's trust-log sync cadence. Test-only.
func SetTrustSyncIntervalForTest(d time.Duration) { clientTrustSyncInterval.Store(int64(d)) }

// knownHashes lists every entry hash this client retains, for a sync offer. The
// entry store is the only source, so the offer can never claim more than is held.
func (m *E2EClient) knownHashes() (hashes [][]byte, truncated bool) {
	m.mu.Lock()
	re := m.retainedEntries
	m.mu.Unlock()
	if re == nil {
		return nil, false
	}
	return re.Hashes()
}

// syncTrustChains exchanges known entry hashes with the gateway and returns the
// assembled chains it served. The gateway computes the delta by set subtraction,
// so the client never needs to infer ancestry. Want is ignored — the client is a
// supplicant and must not publish trust state.
func (m *E2EClient) syncTrustChains() ([][]byte, bool) {
	// Initialize retainedEntries and seed it from the trust chain so the offer
	// always reflects what is locally held, including on the first call.
	// PutAll is idempotent for already-present entries.
	m.mu.Lock()
	if m.retainedEntries == nil {
		m.retainedEntries = trustlog.NewEntryStore()
	}
	re := m.retainedEntries
	m.mu.Unlock()
	if m.trust != nil {
		if mine := m.trust.Bytes(); mine != nil {
			if raw, err := trustlog.ChainEntries(mine); err == nil {
				re.PutAll(raw)
			}
		}
	}

	known, truncated := m.knownHashes()
	var got api.TrustLogSyncResult
	if err := m.peer.Call(api.MethodTrustLogSync, api.TrustLogSyncParams{Known: known, Truncated: truncated}, &got); err != nil {
		return nil, false
	}

	m.mu.Lock()
	prevDisjoint := m.lastDisjointLogged
	m.lastDisjointLogged = got.Disjoint
	m.mu.Unlock()
	if got.Disjoint && !prevDisjoint {
		log.Printf("client: trust log: this device shares no history with the network's; it is likely pinned to a different trust root")
	}

	merged := append([][]byte{}, got.Entries...)
	if m.trust != nil {
		if mine := m.trust.Bytes(); mine != nil {
			if raw, err := trustlog.ChainEntries(mine); err == nil {
				merged = append(merged, raw...)
			}
		}
	}
	if retained := re.All(); len(retained) > 0 {
		merged = append(merged, retained...)
	}
	chains, unplaced := trustlog.AssembleChainsReport(merged)

	m.mu.Lock()
	prevUnplaced := m.lastUnplacedLogged
	m.lastUnplacedLogged = unplaced
	m.mu.Unlock()
	if unplaced > 0 && unplaced != prevUnplaced {
		log.Printf("client: warn: trust-log sync has %d unplaced entries; gateway may hold an incomplete branch", unplaced)
	}

	refused := 0
	for _, chain := range chains {
		raw, err := trustlog.ChainEntries(chain)
		if err != nil {
			continue
		}
		_, r := re.PutAll(raw)
		refused += r
	}
	if refused > 0 {
		log.Printf("client: warn: trust-log entry store at ceiling; entries refused: %d", refused)
	}

	return chains, true
}

// pullTrustChain fetches and ingests trust-log branches from the gateway without
// running the beacon consistency check or delivering beacons. Beacon-triggered
// calls use this path so they do not count as equivocation miss ticks: the branch
// a beacon announces may not yet have propagated to the gateway, and miss
// accumulation is the responsibility of the periodic timer only.
func (m *E2EClient) pullTrustChain() {
	chains, ok := m.syncTrustChains()
	if !ok {
		return
	}
	anyChanged := false
	for _, chain := range chains {
		changed, err := m.trust.Ingest(chain)
		if err != nil {
			continue
		}
		if changed {
			anyChanged = true
		}
	}
	m.detectSupersedingChain(chains)
	if anyChanged {
		if m.trustPath != "" {
			_ = m.persistTrustChain()
		}
		m.reevaluateChannels()
	}
}

// detectSupersedingChain quarantines this client when its own chain is disabled and
// the network serves a different trust root. Same rule as the node's: a disabled log
// enforces nothing and can never be re-enabled, so the pin holding it is not
// protection, only a refusal to see the live network.
func (m *E2EClient) detectSupersedingChain(chains [][]byte) {
	if m.trust == nil || !m.trust.Disabled() {
		return
	}
	genesis := trustlog.SupersedingGenesis(chains, m.genesis)
	if genesis == nil || bytes.Equal(genesis, m.gate.Genesis()) {
		return
	}
	// Observe, not Trip: a second relock moves the root again, and the fingerprint
	// this device reports is the one the operator pins.
	m.gate.Observe(genesis)
	log.Printf("client: this device's trust log is disabled and the network moved to a different root; refusing all node channels (run: argus lock pin, then restart argus)")
	m.reevaluateChannels()
}

// syncTrustLog pulls all competing trust-log branches from the gateway and ingests
// each in order (genesis-pinned; the fork-choice in the store picks the winner;
// rolled-back or tampered branches are silently skipped). After a successful pull it
// cross-checks all collected node beacons against the resolved chain.
func (m *E2EClient) syncTrustLog() {
	chains, ok := m.syncTrustChains()
	if !ok {
		return
	}
	anyChanged := false
	for _, chain := range chains {
		changed, err := m.trust.Ingest(chain)
		if err != nil {
			continue // rollback/fork/tamper/wrong-genesis: skip this branch
		}
		if changed {
			anyChanged = true
		}
	}
	m.detectSupersedingChain(chains)
	if anyChanged {
		if m.trustPath != "" {
			_ = m.persistTrustChain()
		}
		m.reevaluateChannels()
	}
	m.checkBeaconConsistency()
	m.deliverBeacons()
}

// persistTrustChain atomically writes the current chain to trustPath.
func (m *E2EClient) persistTrustChain() error {
	return atomicfile.Write(m.trustPath, m.trust.Bytes())
}

func (m *E2EClient) trustSyncLoop() {
	t := time.NewTicker(time.Duration(clientTrustSyncInterval.Load()))
	defer t.Stop()
	for {
		select {
		case <-m.trustCtx.Done():
			return
		case <-m.peer.Done():
			return
		case <-t.C:
			m.syncTrustLog()
		}
	}
}

// detectUnpinnedChain quarantines this client when the network has a trust log
// and the client holds no pin. Decode only — a self-consistent chain proves
// nothing about its author, so verification would add no safety here.
func (m *E2EClient) detectUnpinnedChain() {
	if m.gate.Tripped() {
		return
	}
	chains, ok := m.syncTrustChains()
	if !ok {
		return
	}
	for _, chain := range chains {
		entries, err := trustlog.UnmarshalChain(chain)
		if err != nil || len(entries) == 0 {
			continue
		}
		m.gate.Trip(trustlog.HashEntry(&entries[0]))
		log.Printf("client: network has a trust log but this device is unpinned; refusing all node channels (run: argus lock pin)")
		m.reevaluateChannels()
		return
	}
}

// unpinnedWatchLoop keeps checking for a trust log so a client that was running
// when the network got locked converges to quarantine within a tick.
func (m *E2EClient) unpinnedWatchLoop() {
	t := time.NewTicker(time.Duration(clientTrustSyncInterval.Load()))
	defer t.Stop()
	for {
		select {
		case <-m.trustCtx.Done():
			return
		case <-t.C:
			if m.gate.Tripped() {
				return
			}
			m.detectUnpinnedChain()
		}
	}
}

// DeviceAuthorized reports whether pub is authorized by the synced trust log.
// Always false when trust-log sync is off.
func (m *E2EClient) DeviceAuthorized(pub []byte) bool {
	return m.trust != nil && m.trust.DeviceAuthorized(pub)
}

// TrustTip returns the current trust-log tip (nil when off / not yet synced).
func (m *E2EClient) TrustTip() []byte {
	if m.trust == nil {
		return nil
	}
	return m.trust.Tip()
}

// onPeerNotify handles gateway-level notifications. node.event is the only signal
// a blind gateway can give about node reachability, so it drives channel lifecycle
// (open on join/return, drop on loss), the synthesized session events a departed
// node can no longer send, and beacon state hygiene.
func (m *E2EClient) onPeerNotify(n api.Notification) {
	if n.Method != api.MethodNodeEvent {
		return
	}
	var ev api.NodeEvent
	if err := json.Unmarshal(n.Params, &ev); err != nil {
		return
	}
	switch ev.Type {
	case api.NodeEventBeacon:
		// Every accepted beacon owes a courier run: node↔node beacon delivery has no
		// other path, so leaving it to the tick puts each node's equivocation
		// cross-check a whole backstop behind the client's.
		if accepted, wantPull := m.ingestBeaconFromDescriptor(ev.Node); accepted {
			m.requestBeaconTrigger(wantPull)
		}
	case api.NodeEventAdded, api.NodeEventOnline:
		// Off the read loop: openChannel calls the gateway and waits for msg2, both
		// of which are answered on this very loop.
		go m.adoptNode(ev.Node)
	case api.NodeEventOffline:
		m.pruneBeaconForDescriptor(ev.Node)
		m.loseNode(ev.Node.ID, false)
	case api.NodeEventRemoved:
		m.pruneBeaconForDescriptor(ev.Node)
		m.loseNode(ev.Node.ID, true)
	}
}

// adoptNode opens a channel to a node that joined (or came back) after Connect.
// Its sessions arrive on their own: the node streams its registry snapshot as
// soon as the channel is up.
func (m *E2EClient) adoptNode(nd api.NodeDescriptor) {
	if err := m.openIfEligible(nd); err != nil {
		log.Printf("client: node %s came online but the channel failed: %v", nd.ID, err)
	}
}

// loseNode drops the channel to a node that is no longer reachable and accounts
// for its sessions: gone for good (removed from the roster) means remove them now,
// otherwise grey them and sweep once the grace window passes. Nothing else can
// report those sessions — the node that owned them is exactly what went away.
func (m *E2EClient) loseNode(nodeID string, gone bool) {
	m.mu.Lock()
	if nc := m.byNode[nodeID]; nc != nil {
		delete(m.byNode, nodeID)
		if ch := nc.ch.Load(); ch != nil {
			delete(m.byChanID, ch.ID())
		}
	}
	if t := m.offlineTimers[nodeID]; t != nil {
		t.Stop()
		delete(m.offlineTimers, nodeID)
	}
	delete(m.delivered, nodeID)
	for srcID := range m.delivered {
		delete(m.delivered[srcID], nodeID)
	}
	var events []registry.Event
	sessions := m.nodeSessions[nodeID]
	if gone {
		delete(m.nodeSessions, nodeID)
		for _, s := range sessions {
			events = append(events, registry.Event{Type: registry.EventRemoved, Session: s})
		}
	} else {
		for id, s := range sessions {
			s.Offline = true
			sessions[id] = s
			events = append(events, registry.Event{Type: registry.EventUpdated, Session: s})
		}
		if len(sessions) > 0 {
			m.offlineTimers[nodeID] = time.AfterFunc(m.offlineGrace, func() { m.sweepOfflineNode(nodeID) })
		}
	}
	m.mu.Unlock()
	m.emitSessionEvents(events)
}

// sweepOfflineNode drops the sessions still marked offline when the grace window
// expires. A session the node re-reported after coming back is no longer marked,
// so it survives — no timer cancellation needed.
func (m *E2EClient) sweepOfflineNode(nodeID string) {
	m.mu.Lock()
	delete(m.offlineTimers, nodeID)
	sessions := m.nodeSessions[nodeID]
	var events []registry.Event
	for id, s := range sessions {
		if !s.Offline {
			continue
		}
		delete(sessions, id)
		events = append(events, registry.Event{Type: registry.EventRemoved, Session: s})
	}
	if len(sessions) == 0 {
		delete(m.nodeSessions, nodeID)
	}
	m.mu.Unlock()
	m.emitSessionEvents(events)
}

// trackSessionEvent mirrors an already-stamped session.event so the client can
// synthesize the offline/removal events for these sessions later.
func (m *E2EClient) trackSessionEvent(nodeID string, stamped json.RawMessage) {
	var ev registry.Event
	if json.Unmarshal(stamped, &ev) != nil || ev.Session.ID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ev.Type == registry.EventRemoved {
		if sessions := m.nodeSessions[nodeID]; sessions != nil {
			delete(sessions, ev.Session.ID)
			if len(sessions) == 0 {
				delete(m.nodeSessions, nodeID)
			}
		}
		return
	}
	sessions := m.nodeSessions[nodeID]
	if sessions == nil {
		sessions = map[string]session.Session{}
		m.nodeSessions[nodeID] = sessions
	}
	sessions[ev.Session.ID] = ev.Session
}

// emitSessionEvents pushes synthesized session.events onto the event stream, with
// the same drop-on-stalled-consumer policy as relayed ones.
func (m *E2EClient) emitSessionEvents(events []registry.Event) {
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		select {
		case m.events <- api.Notification{Method: api.MethodSessionEvent, Params: b}:
		default:
		}
	}
}

// pruneBeaconForDescriptor removes beacon state for the node described by nd.
// Used when a node goes offline or is removed from the roster so its stale
// cached beacon tip cannot accumulate misses and false-positive the equivocation flag.
func (m *E2EClient) pruneBeaconForDescriptor(nd api.NodeDescriptor) {
	if nd.IdentityPubKey == "" {
		return
	}
	pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	if err != nil {
		return
	}
	key := string(pub)
	m.mu.Lock()
	delete(m.beacons, key)
	delete(m.beaconCtr, key)
	delete(m.beaconMiss, key)
	m.mu.Unlock()
}

// ingestBeaconFromDescriptor validates and stores the beacon from a NodeDescriptor.
// Guards applied in order:
//  1. nd.Beacon must be non-nil and nd.IdentityPubKey + nd.BeaconPubKey must be set.
//  2. api.VerifyBeacon must pass (Ed25519 signature check).
//  3. b.BeaconPub must equal the roster-announced BeaconPubKey (attribution check).
//  4. b.Counter must be strictly greater than the last accepted counter for this node.
//
// A beacon that fails any guard is silently dropped (not flagged as equivocation).
//
// accepted reports that a new beacon was stored, which is what the courier owes a
// run for; wantPull additionally reports that the beacon's tip is one this client
// cannot yet place, which is what a trust-log pull (or, unpinned, a quarantine
// check) owes a run for. Acting on them is the caller's job: on the Connect path
// an explicit sync follows immediately, so only the event path triggers.
func (m *E2EClient) ingestBeaconFromDescriptor(nd api.NodeDescriptor) (accepted, wantPull bool) {
	if nd.Beacon == nil || nd.IdentityPubKey == "" || nd.BeaconPubKey == "" {
		return false, false
	}
	identityPub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	if err != nil {
		return false, false
	}
	expectedBeaconPub, err := base64.StdEncoding.DecodeString(nd.BeaconPubKey)
	if err != nil {
		return false, false
	}
	b := *nd.Beacon
	if !api.VerifyBeacon(b) {
		return false, false // signature invalid: silently drop
	}
	if !bytes.Equal(b.BeaconPub, expectedBeaconPub) {
		return false, false // beacon's key doesn't match roster-announced key: drop
	}
	key := string(identityPub)
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.Counter <= m.beaconCtr[key] {
		return false, false // stale or replayed: ignore
	}
	m.beacons[key] = b
	m.beaconCtr[key] = b.Counter
	delete(m.beaconMiss, key)
	if len(b.Tip) == 0 {
		return true, false
	}
	if m.trust == nil {
		// Unpinned: any tip at all is evidence the network has a trust log, which is
		// what detectUnpinnedChain quarantines on. Without this the unpinned client
		// has no event path at all and waits out the whole backstop.
		return true, !m.gate.Tripped()
	}
	// Pull only when the tip is absent from our known chain history.
	// beaconKnown is nil before the first consistency check; pull conservatively.
	return true, m.beaconKnown == nil || !m.beaconKnown[string(b.Tip)]
}

// requestBeaconTrigger runs the arrival-driven beacon work now when the rate-limit
// window is clear, otherwise defers a single run to the end of the window. Deferring
// rather than dropping keeps a second change landing seconds after the first from
// waiting for the backstop; coalescing into one pending run keeps the bound that a
// beacon flood costs at most one run per minClientTriggeredPullInterval window.
func (m *E2EClient) requestBeaconTrigger(wantPull bool) {
	m.triggerMu.Lock()
	defer m.triggerMu.Unlock()
	m.triggerWantPull = m.triggerWantPull || wantPull
	if m.triggerPending {
		return
	}
	if m.triggeredPullInFlight {
		m.triggerPending = true // re-armed by the running trigger's completion
		return
	}
	if wait := minClientTriggeredPullInterval() - time.Since(m.lastTriggeredPull); wait > 0 {
		m.triggerPending = true
		if m.triggerTimer != nil {
			m.triggerTimer.Stop()
		}
		m.triggerTimer = time.AfterFunc(wait, m.firePendingBeaconTrigger)
		return
	}
	m.startBeaconTriggerLocked()
}

func (m *E2EClient) firePendingBeaconTrigger() {
	m.triggerMu.Lock()
	defer m.triggerMu.Unlock()
	if !m.triggerPending || m.triggeredPullInFlight {
		return
	}
	m.triggerPending = false
	m.startBeaconTriggerLocked()
}

// startBeaconTriggerLocked launches the trigger off the read loop: beacons arrive as
// notifications dispatched on it, and every RPC below needs that loop to answer.
// Caller holds triggerMu.
func (m *E2EClient) startBeaconTriggerLocked() {
	m.lastTriggeredPull = time.Now()
	m.triggeredPullInFlight = true
	pull := m.triggerWantPull
	m.triggerWantPull = false
	go func() {
		defer m.finishBeaconTrigger()
		if pull {
			if m.trust == nil {
				m.detectUnpinnedChain()
			} else {
				m.pullTrustChain()
			}
		}
		m.deliverBeacons()
	}()
}

func (m *E2EClient) finishBeaconTrigger() {
	m.triggerMu.Lock()
	m.triggeredPullInFlight = false
	pending := m.triggerPending
	m.triggerPending = false
	m.triggerMu.Unlock()
	if pending {
		m.requestBeaconTrigger(false)
	}
}

// buildChainHashSet parses chainBytes and returns the set of all entry hashes
// present in the linear chain (every position from genesis through head). Returns
// nil, nil when chainBytes is empty (no chain yet). Returns nil and a non-nil error
// when the bytes cannot be parsed; callers must handle that case explicitly rather
// than treating it as "consistent".
func buildChainHashSet(chainBytes []byte) (map[string]bool, error) {
	if len(chainBytes) == 0 {
		return nil, nil
	}
	entries, err := trustlog.UnmarshalChain(chainBytes)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	known := make(map[string]bool, len(entries))
	for i := range entries {
		known[string(trustlog.HashEntry(&entries[i]))] = true
	}
	return known, nil
}

// consistentTips checks whether each collected node beacon's Tip is present in the
// client's linear chain history represented by known (the prebuilt hash set from
// buildChainHashSet). A nil known means no chain is available yet; all beacons are
// treated as consistent (nothing to compare against). A nil or empty Tip is skipped —
// the node has no chain yet and cannot be blamed for divergence. Length is not checked
// (lenient: a tip/length TOCTOU is possible; tip presence is authoritative).
// Returns (true, "") when all beacons reconcile; (false, detail) otherwise.
func consistentTips(beacons map[string]api.Beacon, known map[string]bool) (bool, string) {
	if known == nil {
		return true, "" // no chain yet; cannot compare
	}
	var misses []string
	for key, b := range beacons {
		if len(b.Tip) == 0 {
			continue // node has no chain tip yet; not an equivocation
		}
		if !known[string(b.Tip)] {
			misses = append(misses, fmt.Sprintf("key=%x tip=%x", []byte(key), b.Tip))
		}
	}
	if len(misses) > 0 {
		return false, strings.Join(misses, "; ")
	}
	return true, ""
}

// checkBeaconConsistency cross-checks all collected node beacons against the current
// resolved trust-log chain. A beacon whose Tip is not present in the client's linear
// chain history is tracked per-node: if the same unreconciled tip persists for
// beaconMissThreshold consecutive ticks (meaning it cannot be attributed to propagation
// lag, which reconciles on the next pull), equivocation is flagged. A tip that appears
// in the chain on any tick resets that node's miss streak. Beacons for nodes not
// currently connected are skipped (belt-and-suspenders: a legitimate fork that
// orphans an offline node's cached tip must not trigger the flag). No-op when the
// trust store is absent or the chain is empty.
// The resolved chain is parsed once per tick (O(1) parse, not O(beacons)); when the
// chain bytes cannot be parsed the tick is skipped entirely — miss state and the
// equivocation flag are left untouched, avoiding both a false positive and a spurious
// miss-streak reset.
func (m *E2EClient) checkBeaconConsistency() {
	if m.trust == nil {
		return
	}
	chainBytes, tip := m.trust.BytesAndTip()
	if len(chainBytes) == 0 {
		return // not yet synced; nothing to compare
	}
	m.checkBeaconConsistencyWithChain(chainBytes, tip)
}

// checkBeaconConsistencyWithChain is the inner implementation of
// checkBeaconConsistency, exposed for testing with caller-supplied chain bytes
// and tip (both from a single BytesAndTip snapshot, or test-supplied).
// Including corrupt bytes to verify the parse-failure skip path.
func (m *E2EClient) checkBeaconConsistencyWithChain(chainBytes, tip []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.beacons) == 0 {
		return // no beacons yet: skip the chain parse/hash entirely
	}
	// Parse the resolved chain once per tick; cache keyed on the trust-log tip so
	// the expensive UnmarshalChain+hash is skipped when the chain has not advanced.
	known := m.beaconKnown
	if known == nil || !bytes.Equal(tip, m.beaconKnownTip) {
		var err error
		known, err = buildChainHashSet(chainBytes)
		if err != nil {
			// Unparseable chain: cannot evaluate consistency this tick. Leave miss
			// state and equivocation flag untouched to avoid a false positive or a
			// spurious miss-streak reset.
			log.Printf("client: warn: resolved chain unparseable, skipping beacon consistency check: %v", err)
			return
		}
		m.beaconKnown = known
		m.beaconKnownTip = tip
	}
	// Build the set of currently connected identity pubs so we can skip
	// beacons from nodes that have gone offline or been de-rostered.
	connected := make(map[string]bool, len(m.byNode))
	for _, nc := range m.byNode {
		connected[string(nc.identityPub)] = true
	}
	for key, b := range m.beacons {
		// Belt-and-suspenders: skip beacons for nodes that WERE connected (had an
		// open channel) but are no longer connected. A legitimate fork that orphans
		// an offline node's stale cached tip must not accumulate misses and trigger
		// the flag. Nodes that report beacons but were NEVER connected (e.g. their
		// identity key isn't authorized by the local chain) are NOT skipped — those
		// beacons are still checked for equivocation.
		if m.everConnected[key] && !connected[key] {
			continue
		}
		if len(b.Tip) == 0 {
			delete(m.beaconMiss, key) // no tip yet: clear any prior miss
			continue
		}
		// Check tip-membership against the prebuilt hash set (parsed once above).
		if ok, _ := consistentTips(map[string]api.Beacon{key: b}, known); ok {
			delete(m.beaconMiss, key) // tip reconciled: reset miss streak
			continue
		}
		// Tip not in resolved chain: track per-node consecutive misses.
		ms := m.beaconMiss[key]
		if ms == nil || !bytes.Equal(ms.tip, b.Tip) {
			// Different tip than the recorded miss (or first miss): start fresh.
			ms = &beaconMissState{tip: append([]byte(nil), b.Tip...), misses: 1}
			m.beaconMiss[key] = ms
		} else {
			ms.misses++
		}
		if ms.misses >= beaconMissThreshold && !m.equivocation {
			log.Printf("client: equivocation detected — node beacons diverge from resolved chain: key=%x tip=%x", []byte(key), b.Tip)
			m.equivocation = true
		}
	}
}

// deliverBeacons couriers each collected node beacon to every OTHER connected
// node via the beacon.deliver E2E method. A node's own beacon is never delivered
// back to that same node. Each (source, target) pair is deduped by the counter
// last successfully delivered: a beacon is only sent when its counter advances or
// the target rejected a prior attempt. Once per beaconDeliverForceInterval a full
// re-courier is forced regardless, bounding the window in which a target that
// accepted and then lost a beacon stays without it. Delivery is sequential — the
// use case is N=2–5 nodes, and the driver is beacon arrival, not the trust tick.
func (m *E2EClient) deliverBeacons() {
	m.mu.Lock()
	forceAll := time.Since(m.lastForcedDeliver) >= beaconDeliverForceInterval
	if forceAll {
		m.lastForcedDeliver = time.Now()
	}
	// Build identity pub → nodeID index from current channels.
	identToNode := make(map[string]string, len(m.byNode))
	for nodeID, nc := range m.byNode {
		identToNode[string(nc.identityPub)] = nodeID
	}
	type entry struct {
		beacon   api.Beacon
		sourceID string // nodeID that owns this beacon
	}
	var todo []entry
	for key, b := range m.beacons {
		srcID := identToNode[key]
		if srcID == "" {
			continue // node disconnected since beacon was collected; skip
		}
		todo = append(todo, entry{beacon: b, sourceID: srcID})
	}
	targetIDs := make([]string, 0, len(m.byNode))
	for nodeID := range m.byNode {
		targetIDs = append(targetIDs, nodeID)
	}
	m.mu.Unlock()

	for _, e := range todo {
		b := e.beacon
		for _, targetID := range targetIDs {
			if targetID == e.sourceID {
				continue // don't deliver a node's own beacon back to itself
			}
			if !forceAll {
				m.mu.Lock()
				skip := m.delivered[e.sourceID][targetID] >= b.Counter
				m.mu.Unlock()
				if skip {
					continue
				}
			}
			if m.callNode(targetID, api.MethodBeaconDeliver, b, nil) == nil {
				m.mu.Lock()
				if m.delivered[e.sourceID] == nil {
					m.delivered[e.sourceID] = make(map[string]uint64)
				}
				m.delivered[e.sourceID][targetID] = b.Counter
				m.mu.Unlock()
			}
		}
	}
}

// Equivocation reports whether the client has detected a trust-log equivocation:
// one or more nodes reported a HEAD beacon whose tip could not be reconciled with
// the client's resolved chain after a pull. Once set, this flag is never cleared —
// equivocation evidence persists for the lifetime of the client session.
func (m *E2EClient) Equivocation() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.equivocation
}
