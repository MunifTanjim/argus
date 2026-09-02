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

// tipMissThreshold is the number of consecutive unreconciled ticks for the
// same tip required before the equivocation flag is set.
const tipMissThreshold = 2

// tipMissState tracks consecutive unreconciled ticks for a single node's tip.
type tipMissState struct {
	tip    []byte
	misses int
}

// E2EClient talks to nodes over end-to-end encrypted channels relayed by a blind
// gateway.
type E2EClient struct {
	peer      *api.Peer
	ready     chan struct{} // closed at end of NewE2EClientWithGate; synchronises m.peer init
	static    e2e.KeyPair
	plaintext bool // when true, openChannel skips Noise and uses an identity cipher

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
	kick   chan struct{} // buffered(1); nudges trustSyncLoop from the Peer read loop without blocking it

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

	// Tip cross-check state (guarded by mu).
	// tipMiss tracks consecutive unreconciled ticks per node; cleared on reconcile.
	// everConnected records identity pubs for which a channel was successfully opened at
	// any point; used by checkTipConsistency to distinguish "once connected, now
	// offline" (skip: stale tip) from "never connected" (check: legitimate tip).
	// authTip maps string(identityPub) to the tip each node reported over its
	// authenticated channel, refreshed once per sync tick.
	tipMiss       map[string]*tipMissState
	everConnected map[string]bool   // string(identityPub) → true, never deleted
	equivocation  bool              // set permanently once divergence is detected
	authTip       map[string][]byte // string(identityPub) → tip learned over the authenticated channel

	knownSetTip []byte          // caches known-set key; guarded by mu
	knownSet    map[string]bool // resolved chain entry-hash set for tip checks
}

// NewE2EClientWithGate is NewE2EClientWithIdentity plus a caller-owned quarantine
// gate. The reconnecting client shares one gate across reconnects so an unpinned
// client cannot be un-quarantined by a dropped connection.
func NewE2EClientWithGate(conn net.Conn, static e2e.KeyPair, genesisHash []byte, chainPath string, gate *trustpin.Gate) (*E2EClient, error) {
	return newE2EClientWithGate(conn, static, genesisHash, chainPath, gate, false)
}

func newE2EClientWithGate(conn net.Conn, static e2e.KeyPair, genesisHash []byte, chainPath string, gate *trustpin.Gate, plaintext bool) (*E2EClient, error) {
	m := &E2EClient{
		static:        static,
		plaintext:     plaintext,
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
		kick:          make(chan struct{}, 1),
		tipMiss:       map[string]*tipMissState{},
		everConnected: map[string]bool{},
		authTip:       map[string][]byte{},
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

// newEphemeralE2EClient is the shared base for NewE2EClient and NewE2EClientPlain.
// It generates a throwaway static key and sets the plaintext flag before the peer
// read loop starts, so openChannel always sees a consistent plaintext value.
func newEphemeralE2EClient(conn net.Conn, plaintext bool) (*E2EClient, error) {
	static, err := e2e.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return newE2EClientWithGate(conn, static, nil, "", &trustpin.Gate{}, plaintext)
}

// NewE2EClient wraps a gateway connection, wiring the relay-frame demux. Generates
// an ephemeral client Noise static key.
func NewE2EClient(conn net.Conn) (*E2EClient, error) {
	return newEphemeralE2EClient(conn, false)
}

// NewE2EClientPlain wraps a gateway connection for plaintext relay (no Noise
// encryption). The ephemeral static key generated internally is unused.
func NewE2EClientPlain(conn net.Conn) (*E2EClient, error) {
	return newEphemeralE2EClient(conn, true)
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
	var pub []byte
	if nd.IdentityPubKey == "" {
		if !m.plaintext {
			return nil // no key: cannot open an E2E channel to this node
		}
		// plaintext: no Noise key needed; pub stays nil and is ignored by openChannel
	} else {
		var err error
		pub, err = base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil {
			return nil // bad key: skip (fail-closed; also can't open a channel anyway)
		}
		if m.trust != nil && !m.trust.Disabled() && !m.trust.DeviceAuthorized(pub) {
			return nil // unauthorized node: silent exclusion (fail-closed)
		}
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
	if err := m.openChannel(nd, pub); err != nil {
		return err
	}
	var res api.IdentifyResult
	if err := m.callNode(nd.ID, api.MethodNodeIdentify, nil, &res); err != nil {
		log.Printf("client: node %s identify failed: %v", nd.ID, err)
	} else {
		m.mu.Lock()
		m.authTip[string(pub)] = res.Tip
		m.mu.Unlock()
	}
	return nil
}

// openChannel runs relay.open then either establishes a plaintext identity channel
// (when m.plaintext is set) or performs the Noise IK initiator handshake.
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
	if m.plaintext {
		nc := &nodeChan{nodeID: nd.ID, label: nd.Label, hs: make(chan error, 1)}
		nc.ch.Store(api.NewPlainChannel(res.ChanID))
		m.mu.Lock()
		m.byNode[nd.ID] = nc
		m.byChanID[res.ChanID] = nc
		m.mu.Unlock()
		nc.hs <- nil
		return nil
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
// Also prunes tip state for each dropped node so a stale tip cannot accumulate misses
// and false-positive the equivocation flag.
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
			delete(m.tipMiss, key)
			delete(m.authTip, key)
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
		// Prune tip state so the revoked node's stale tip cannot accumulate misses.
		key := string(nc.identityPub)
		delete(m.tipMiss, key)
		delete(m.authTip, key)
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

// clientTrustSyncInterval is the BACKSTOP for trust-log convergence, not the primary
// path: a change normally arrives via trustlog.changed (node) within milliseconds. It
// also bounds how long an UNPINNED device stays open on a locked network before
// quarantining, so shortening it is safe and lengthening it widens that window. Do not
// tune this without reading detectUnpinnedChain.
// Stored as nanoseconds in an atomic so SetTrustSyncIntervalForTest is race-free
// when background goroutines read it concurrently.
var clientTrustSyncInterval atomic.Int64

func init() {
	clientTrustSyncInterval.Store(int64(5 * time.Minute))
	handshakeTimeoutNs.Store(int64(10 * time.Second))
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
// refreshes each connected node's tip over its authenticated channel and cross-checks
// those tips against the resolved chain.
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
	m.refreshAuthTips()
	m.checkTipConsistency()
}

// refreshAuthTips re-reads each connected node's trust-log tip over its authenticated
// Noise channel and records it in authTip. The tip is bound to the trust-log-authorized
// identity, unlike the forgeable gateway roster; it is the only source the equivocation
// check trusts. Runs in the trust-sync goroutine, never the read loop, so the RPCs are
// safe. A node that fails to answer is logged and left on its previously-known tip.
func (m *E2EClient) refreshAuthTips() {
	type target struct {
		nodeID      string
		identityPub []byte
	}
	m.mu.Lock()
	targets := make([]target, 0, len(m.byNode))
	for nodeID, nc := range m.byNode {
		targets = append(targets, target{nodeID: nodeID, identityPub: nc.identityPub})
	}
	m.mu.Unlock()
	for _, tg := range targets {
		var res api.IdentifyResult
		if err := m.callNode(tg.nodeID, api.MethodNodeIdentify, nil, &res); err != nil {
			log.Printf("client: node %s identify (tip refresh) failed: %v", tg.nodeID, err)
			continue
		}
		m.mu.Lock()
		m.authTip[string(tg.identityPub)] = res.Tip
		m.mu.Unlock()
	}
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
		case <-m.kick:
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
	// The gateway does not serve trustlog.sync until locked mode lands (later slice); in TOFU this returns method-not-found and syncTrustChains degrades to "no chains" — not a bug.
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
// (open on join/return, drop on loss) and the synthesized session events a departed
// node can no longer send.
func (m *E2EClient) onPeerNotify(n api.Notification) {
	if n.Method != api.MethodNodeEvent {
		return
	}
	var ev api.NodeEvent
	if err := json.Unmarshal(n.Params, &ev); err != nil {
		return
	}
	switch ev.Type {
	case api.NodeEventAdded, api.NodeEventOnline:
		// Off the read loop: openChannel calls the gateway and waits for msg2, both
		// of which are answered on this very loop.
		go m.adoptNode(ev.Node)
	case api.NodeEventOffline:
		m.loseNode(ev.Node.ID, false)
	case api.NodeEventRemoved:
		m.loseNode(ev.Node.ID, true)
	case api.NodeEventTrustChanged:
		// Runs on the Peer read loop — must not block or make RPCs inline.
		select {
		case m.kick <- struct{}{}:
		default:
		}
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
		key := string(nc.identityPub)
		delete(m.authTip, key)
		delete(m.tipMiss, key) // stale tip must not accumulate misses while the node is gone
	}
	if t := m.offlineTimers[nodeID]; t != nil {
		t.Stop()
		delete(m.offlineTimers, nodeID)
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

// checkTipConsistency cross-checks each connected node's authenticated tip against the
// current resolved trust-log chain. A tip not present in the client's linear chain
// history is tracked per-node: if the same unreconciled tip persists for
// tipMissThreshold consecutive ticks (meaning it cannot be attributed to propagation
// lag, which reconciles on the next pull), equivocation is flagged. A tip that appears
// in the chain on any tick resets that node's miss streak. Tips for nodes not currently
// connected are skipped (belt-and-suspenders: a legitimate fork that orphans an offline
// node's cached tip must not trigger the flag). No-op when the trust store is absent or
// the chain is empty.
func (m *E2EClient) checkTipConsistency() {
	if m.trust == nil {
		return
	}
	chainBytes, tip := m.trust.BytesAndTip()
	if len(chainBytes) == 0 {
		return // not yet synced; nothing to compare
	}
	m.checkTipConsistencyWithChain(chainBytes, tip)
}

// checkTipConsistencyWithChain is the inner implementation of checkTipConsistency,
// exposed for testing with caller-supplied chain bytes and tip (both from a single
// BytesAndTip snapshot, or test-supplied, including corrupt bytes to verify the
// parse-failure skip path).
// The resolved chain is parsed once per tick (cached, keyed on the trust-log tip);
// when the chain bytes cannot be parsed the tick is skipped entirely — miss state and
// the equivocation flag are left untouched, avoiding both a false positive and a
// spurious miss-streak reset.
func (m *E2EClient) checkTipConsistencyWithChain(chainBytes, tip []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.authTip) == 0 {
		return // no tips yet: skip the chain parse/hash entirely
	}
	// Parse the resolved chain once per tick; cache keyed on the trust-log tip so
	// the expensive UnmarshalChain+hash is skipped when the chain has not advanced.
	known := m.knownSet
	if known == nil || !bytes.Equal(tip, m.knownSetTip) {
		var err error
		known, err = buildChainHashSet(chainBytes)
		if err != nil {
			// Unparseable chain: cannot evaluate consistency this tick. Leave miss
			// state and equivocation flag untouched to avoid a false positive or a
			// spurious miss-streak reset.
			log.Printf("client: warn: resolved chain unparseable, skipping tip consistency check: %v", err)
			return
		}
		m.knownSet = known
		m.knownSetTip = tip
	}
	if known == nil {
		return // no chain yet; cannot compare
	}
	// Build the set of currently connected identity pubs so we can skip tips from
	// nodes that have gone offline or been de-rostered.
	connected := make(map[string]bool, len(m.byNode))
	for _, nc := range m.byNode {
		connected[string(nc.identityPub)] = true
	}
	for key, tipBytes := range m.authTip {
		// Belt-and-suspenders: skip tips for nodes that WERE connected (had an open
		// channel) but are no longer connected. A legitimate fork that orphans an
		// offline node's stale cached tip must not accumulate misses and trigger the
		// flag. Nodes that reported a tip but were NEVER connected are still checked.
		if m.everConnected[key] && !connected[key] {
			continue
		}
		if len(tipBytes) == 0 {
			delete(m.tipMiss, key) // no tip yet: clear any prior miss
			continue
		}
		if known[string(tipBytes)] {
			delete(m.tipMiss, key) // tip reconciled: reset miss streak
			continue
		}
		// Tip not in resolved chain: track per-node consecutive misses.
		ms := m.tipMiss[key]
		if ms == nil || !bytes.Equal(ms.tip, tipBytes) {
			// Different tip than the recorded miss (or first miss): start fresh.
			ms = &tipMissState{tip: append([]byte(nil), tipBytes...), misses: 1}
			m.tipMiss[key] = ms
		} else {
			ms.misses++
		}
		if ms.misses >= tipMissThreshold && !m.equivocation {
			log.Printf("client: equivocation detected — node tip diverges from resolved chain: key=%x tip=%x", []byte(key), tipBytes)
			m.equivocation = true
		}
	}
}

// Equivocation reports whether the client has detected a trust-log equivocation:
// one or more nodes reported a tip over its authenticated channel that could not be
// reconciled with the client's resolved chain after a pull. Once set, this flag is
// never cleared — equivocation evidence persists for the lifetime of the client session.
func (m *E2EClient) Equivocation() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.equivocation
}

// authTipEntry returns the tip learned over the authenticated channel for the
// given identity public key and whether an entry exists. Test-only in intent.
func (m *E2EClient) authTipEntry(identityPub []byte) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tip, ok := m.authTip[string(identityPub)]
	return append([]byte(nil), tip...), ok
}
