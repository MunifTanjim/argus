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
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// Tunable timeouts.
var callTimeout = 30 * time.Second

// handshakeTimeoutNs is how long an initiator waits for the responder's msg2.
// Stored as nanoseconds in an atomic so SetHandshakeTimeoutForTest is race-free
// when background goroutines read it concurrently.
var handshakeTimeoutNs atomic.Int64

// nodeChan is one established E2E channel to a node.
type nodeChan struct {
	nodeID      string
	label       string
	identityPub []byte                      // copy of the node's Noise identity public key
	ch          atomic.Pointer[api.Channel] // set after the handshake; read on the read loop
	sendMu      sync.Mutex                  // serializes Seal+SendRawFrame (enc-nonce order)
	hs          chan []byte                 // delivers the handshake msg2 during setup
}

type pendingReply struct {
	result json.RawMessage
	rpcErr *api.RPCError
}

// beaconMissThreshold is the number of consecutive unreconciled ticks for the
// same tip required before the equivocation flag is set.
const beaconMissThreshold = 2

// beaconMissState tracks consecutive unreconciled ticks for a single node's beacon tip.
type beaconMissState struct {
	tip    []byte
	misses int
}

// E2EClient talks to nodes over end-to-end encrypted channels relayed by a blind
// gateway.
type E2EClient struct {
	peer   *api.Peer
	static e2e.KeyPair

	mu       sync.Mutex
	byNode   map[string]*nodeChan
	byChanID map[string]*nodeChan
	pending  map[uint64]chan pendingReply
	nextReq  uint64
	subNode  map[string]string    // sub_id  -> nodeID (transcript.subscribe)
	termNode map[string]string    // term_id -> nodeID (terminal.open)

	events chan api.Notification

	trust     *trustlog.SyncStore // locked-mode trust-log store; nil when off
	trustPath string              // locked-mode chain persist path; "" = no persistence
	trustCtx  context.Context     // cancelled on Close, stops the sync ticker
	trustStop context.CancelFunc

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

	beaconKnownTip []byte          // caches known-set key; guarded by mu
	beaconKnown    map[string]bool // resolved chain entry-hash set for beacon checks
}

// NewE2EClientWithIdentity wraps a gateway connection with a caller-provided static
// identity (persisted, for locked mode) and optional pinned genesis. chainPath, if
// non-empty, seeds the trust store from disk on construction and persists it on each
// advance (genesis-pinned Ingest rejects a rolled-back or tampered file).
func NewE2EClientWithIdentity(conn net.Conn, static e2e.KeyPair, genesisHash []byte, chainPath string) (*E2EClient, error) {
	m := &E2EClient{
		static:        static,
		byNode:        map[string]*nodeChan{},
		byChanID:      map[string]*nodeChan{},
		pending:       map[uint64]chan pendingReply{},
		subNode:       map[string]string{},
		termNode:      map[string]string{},
		events:        make(chan api.Notification, 256),
		beacons:       map[string]api.Beacon{},
		beaconCtr:     map[string]uint64{},
		beaconMiss:    map[string]*beaconMissState{},
		everConnected: map[string]bool{},
	}
	if genesisHash != nil {
		m.trust = trustlog.NewSyncStore(genesisHash)
		m.trustPath = chainPath
		// Seed from a persisted chain so a reconnect resumes from the last verified
		// tip (genesis-pinned Ingest rejects a rolled-back/tampered file).
		if chainPath != "" {
			if b, err := os.ReadFile(chainPath); err == nil && len(b) > 0 {
				_, _ = m.trust.Ingest(b)
			}
		}
	}
	m.peer = api.NewPeer(conn, api.PeerOptions{OnRelayFrame: m.onRelayFrame, OnNotify: m.onPeerNotify})
	m.trustCtx, m.trustStop = context.WithCancel(context.Background())
	return m, nil
}

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
		m.ingestBeaconFromDescriptor(nd)
	}
	// Locked mode: pull the trust log before deciding which nodes to open. The store
	// is already disk-seeded (last verified HEAD), so enforcement is correct even if
	// this pull fails.
	if m.trust != nil {
		m.syncTrustLog()
	}
	for _, nd := range roster.Nodes {
		if nd.IdentityPubKey == "" {
			continue // no key: cannot open an E2E channel to this node
		}
		pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil {
			continue // bad key: skip (fail-closed; also can't open a channel anyway)
		}
		if m.trust != nil && !m.trust.Disabled() && !m.trust.DeviceAuthorized(pub) {
			continue // unauthorized node: silent exclusion (fail-closed)
		}
		if err := m.openChannel(nd, pub); err != nil {
			return fmt.Errorf("client: open channel to %s: %w", nd.ID, err)
		}
	}
	if m.trust != nil {
		go m.trustSyncLoop()
	}
	return nil
}

// openChannel runs relay.open + the Noise IK initiator handshake for one node.
// pub is the decoded identity public key, already checked by Connect.
func (m *E2EClient) openChannel(nd api.NodeDescriptor, pub []byte) error {
	var res api.RelayOpenResult
	if err := m.peer.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: nd.ID}, &res); err != nil {
		return err
	}
	init, msg1, err := e2e.NewInitiator(m.static, pub, api.ChannelPrologue(nd.ID, res.ChanID))
	if err != nil {
		return err
	}
	nc := &nodeChan{nodeID: nd.ID, label: nd.Label, identityPub: append([]byte(nil), pub...), hs: make(chan []byte, 1)}
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
	case msg2 := <-nc.hs:
		sess, err := init.Finish(msg2)
		if err != nil {
			return err
		}
		nc.ch.Store(api.NewChannel(res.ChanID, sess))
		m.mu.Lock()
		m.byNode[nd.ID] = nc
		m.everConnected[string(pub)] = true // record for checkBeaconConsistency skip guard
		m.mu.Unlock()
		return nil
	case <-m.peer.Done():
		return fmt.Errorf("connection closed during handshake")
	case <-time.After(time.Duration(handshakeTimeoutNs.Load())):
		return fmt.Errorf("handshake timeout")
	}
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
		if msg2, err := api.HandshakeFromFrame(f); err == nil {
			select {
			case nc.hs <- msg2:
			default:
			}
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
				return // one bad node doesn't fail the whole list
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

// clientTrustSyncInterval is how often the client re-pulls the trust-log chain.
// Stored as nanoseconds in an atomic so SetTrustSyncIntervalForTest is race-free
// when background goroutines read it concurrently.
var clientTrustSyncInterval atomic.Int64

func init() {
	clientTrustSyncInterval.Store(int64(30 * time.Second))
	handshakeTimeoutNs.Store(int64(10 * time.Second))
}

// SetHandshakeTimeoutForTest overrides the Noise handshake timeout. Test-only.
func SetHandshakeTimeoutForTest(d time.Duration) { handshakeTimeoutNs.Store(int64(d)) }

// SetTrustSyncIntervalForTest overrides the client's trust-log sync cadence. Test-only.
func SetTrustSyncIntervalForTest(d time.Duration) { clientTrustSyncInterval.Store(int64(d)) }

// syncTrustLog pulls all competing trust-log branches from the gateway and ingests
// each in order (genesis-pinned; the fork-choice in the store picks the winner;
// rolled-back or tampered branches are silently skipped). After a successful pull it
// cross-checks all collected node beacons against the resolved chain.
func (m *E2EClient) syncTrustLog() {
	var got api.TrustLogPullResult
	if err := m.peer.Call(api.MethodTrustLogPull, nil, &got); err != nil || len(got.Chains) == 0 {
		return
	}
	anyChanged := false
	for _, chain := range got.Chains {
		changed, err := m.trust.Ingest(chain)
		if err != nil {
			continue // rollback/fork/tamper/wrong-genesis: skip this branch
		}
		if changed {
			anyChanged = true
		}
	}
	if anyChanged {
		if m.trustPath != "" {
			// best-effort: a failed persist just means the next reconnect re-pulls + re-persists.
			_ = m.persistTrustChain()
		}
		m.reevaluateChannels()
	}
	// Cross-check all collected node beacons against the resolved chain on every
	// successful pull — regardless of whether the chain advanced this tick.
	m.checkBeaconConsistency()
	// Courier each node's signed beacon to the other connected nodes so nodes can
	// cross-check each other's tips without relying solely on the client's view.
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

// onPeerNotify handles gateway-level notifications. It processes node.event
// beacon updates (ingest the new beacon) and offline/removed events (prune the
// node's beacon state so stale tips cannot accumulate misses after the node
// leaves the roster or goes offline).
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
		m.ingestBeaconFromDescriptor(ev.Node)
	case api.NodeEventOffline, api.NodeEventRemoved:
		m.pruneBeaconForDescriptor(ev.Node)
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
func (m *E2EClient) ingestBeaconFromDescriptor(nd api.NodeDescriptor) {
	if nd.Beacon == nil || nd.IdentityPubKey == "" || nd.BeaconPubKey == "" {
		return
	}
	identityPub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	if err != nil {
		return
	}
	expectedBeaconPub, err := base64.StdEncoding.DecodeString(nd.BeaconPubKey)
	if err != nil {
		return
	}
	b := *nd.Beacon
	if !api.VerifyBeacon(b) {
		return // signature invalid: silently drop
	}
	if !bytes.Equal(b.BeaconPub, expectedBeaconPub) {
		return // beacon's key doesn't match roster-announced key: drop
	}
	key := string(identityPub)
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.Counter <= m.beaconCtr[key] {
		return // stale or replayed: ignore
	}
	m.beacons[key] = b
	m.beaconCtr[key] = b.Counter
	delete(m.beaconMiss, key) // counter advanced: new beacon supersedes any miss streak
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
// back to that same node. Delivery is best-effort (errors silently ignored) and
// sequential — the use case is N=2–5 nodes and a 30-second interval.
func (m *E2EClient) deliverBeacons() {
	m.mu.Lock()
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
			_ = m.callNode(targetID, api.MethodBeaconDeliver, b, nil)
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
