// Package client is the end-to-end encrypted client transport: it discovers nodes
// through a blind gateway and talks to each over its own Noise channel, decrypting
// everything client-side. The gateway only relays opaque frames.
package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/session"
)

// Tunable timeouts (vars so tests can shorten them).
var (
	handshakeTimeout = 10 * time.Second
	callTimeout      = 30 * time.Second
)

// nodeChan is one established E2E channel to a node.
type nodeChan struct {
	nodeID string
	label  string
	ch     atomic.Pointer[api.Channel] // set after the handshake; read on the read loop
	sendMu sync.Mutex                  // serializes Seal+SendRawFrame (enc-nonce order)
	hs     chan []byte                 // delivers the handshake msg2 during setup
}

type pendingReply struct {
	result json.RawMessage
	rpcErr *api.RPCError
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
}

// NewE2EClient wraps a gateway connection, wiring the relay-frame demux. Generates
// an ephemeral client Noise static key.
func NewE2EClient(conn net.Conn) (*E2EClient, error) {
	static, err := e2e.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	m := &E2EClient{
		static:   static,
		byNode:   map[string]*nodeChan{},
		byChanID: map[string]*nodeChan{},
		pending:  map[uint64]chan pendingReply{},
		subNode:  map[string]string{},
		termNode: map[string]string{},
		events:   make(chan api.Notification, 256),
	}
	m.peer = api.NewPeer(conn, api.PeerOptions{OnRelayFrame: m.onRelayFrame})
	return m, nil
}

// Done is closed when the underlying gateway connection drops.
func (m *E2EClient) Done() <-chan struct{} { return m.peer.Done() }

// Events is the aggregated node-notification stream.
func (m *E2EClient) Events() <-chan api.Notification { return m.events }

// Close tears down the gateway connection.
func (m *E2EClient) Close() error { return m.peer.Close() }

// Connect discovers nodes and opens an E2E channel to each that advertises a key.
func (m *E2EClient) Connect() error {
	var roster api.NodesListResult
	if err := m.peer.Call(api.MethodNodesList, nil, &roster); err != nil {
		return fmt.Errorf("client: nodes.list: %w", err)
	}
	for _, nd := range roster.Nodes {
		if nd.IdentityPubKey == "" {
			continue // no key: cannot open an E2E channel to this node
		}
		if err := m.openChannel(nd); err != nil {
			return fmt.Errorf("client: open channel to %s: %w", nd.ID, err)
		}
	}
	return nil
}

// openChannel runs relay.open + the Noise IK initiator handshake for one node.
func (m *E2EClient) openChannel(nd api.NodeDescriptor) error {
	pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	if err != nil {
		return fmt.Errorf("bad identity pubkey: %w", err)
	}
	var res api.RelayOpenResult
	if err := m.peer.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: nd.ID}, &res); err != nil {
		return err
	}
	init, msg1, err := e2e.NewInitiator(m.static, pub, api.ChannelPrologue(nd.ID, res.ChanID))
	if err != nil {
		return err
	}
	nc := &nodeChan{nodeID: nd.ID, label: nd.Label, hs: make(chan []byte, 1)}
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
		m.mu.Unlock()
		return nil
	case <-m.peer.Done():
		return fmt.Errorf("connection closed during handshake")
	case <-time.After(handshakeTimeout):
		return fmt.Errorf("handshake timeout")
	}
}

// onRelayFrame demuxes inbound relay frames on the Peer read loop. It Opens every
// sealed frame inline in arrival order (shared dec-nonce) and never blocks.
func (m *E2EClient) onRelayFrame(f api.RelayFrame) {
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

// Call routes a client RPC over the E2E channels, reproducing the gateway's
// aggregation: fanout+stamp for lists, composite-split for session-addressed,
// node_id routing for node-addressed, handle routing for terminal calls, and
// passthrough for gateway-native methods (server.info/nodes.list/push.*/clients.*).
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
	default: // gateway-native: server.info, nodes.list, ping, push.*, clients.*
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
				return // one bad node doesn't fail the whole list (mirror Fanout)
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
