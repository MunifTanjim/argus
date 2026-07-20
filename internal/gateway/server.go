package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// clientRoutedMethods are session-addressed control calls forwarded to the owning
// node by their composite session_id. sessions.spawn routes by node_id instead (no
// session to address yet).
var clientRoutedMethods = []string{
	api.MethodSessionTranscriptView,
	api.MethodSessionToolDetail,
	api.MethodSessionCapture,
	api.MethodSessionInput,
	api.MethodSessionKey,
	api.MethodSessionRespond,
	api.MethodSessionKill,
	api.MethodSessionChangedFiles,
	api.MethodSessionFileDiff,
	api.MethodSessionCommits,
	api.MethodSessionCommitFiles,
}

// subEntry records a client's transcript subscription for routing delta notifications.
type subEntry struct {
	client api.Notifier
	nodeID string
}

// termEntry records a client's open terminal for routing output notifications.
type termEntry struct {
	client api.Notifier
	nodeID string
	relay  *termRelay // fans output out to the client off the node read loop
}

// termRelayBuffer is how many terminal.output frames may queue for one client
// before it is considered non-draining: enough to absorb a momentary lag, bounded
// so a truly stuck viewer is dropped rather than buffered unboundedly.
const termRelayBuffer = 256

// termRelay decouples a client's terminal.output fan-out from the node uplink's
// single read loop: the loop enqueues without blocking, a dedicated goroutine
// writes in order. Without it, one non-draining viewer stalls the read loop and
// freezes every other session and terminal on that node.
type termRelay struct {
	ch       chan termFrame // the last frame has fin=true
	stop     chan struct{}
	stopOnce sync.Once
}

// termFrame is one relayed notification. fin marks the terminating frame
// (terminal.exited): the writer emits it, then returns.
type termFrame struct {
	method string
	params any
	fin    bool
}

// stopRelay ends the writer goroutine (idempotent). Safe to call concurrently
// with a send: the channel is never closed, so a racing send can't panic.
func (r *termRelay) stopRelay() { r.stopOnce.Do(func() { close(r.stop) }) }

// relayTermOutput drains a term's frames to its client in order, stopping on the
// terminating frame, a client write error, or stopRelay.
func relayTermOutput(c api.Notifier, r *termRelay) {
	for {
		select {
		case <-r.stop:
			return
		case f := <-r.ch:
			if err := c.Notify(f.method, f.params); err != nil || f.fin {
				return // client gone or final frame delivered
			}
		}
	}
}

// Server exposes an Aggregator over /node (node uplinks) and /client (consumers).
// Auth predicates gate each; nil = allow all (local/dev only).
type Server struct {
	agg        *Aggregator
	nodeAuth   func(token string) bool
	clientAuth func(token string) bool
	clientSrv  *api.Server

	clientTokens *clienttoken.Store
	pushStore    *push.Store            // nil = push disabled
	pushSender   *push.Dispatcher       // for push.test
	vapidPubKey  string                 // served via push.vapidKey
	master       string                 // a /client conn presenting it is admin
	version      string                 // served via server.info
	publicURL    atomic.Pointer[string] // base URL for pairing QRs

	pairMu      sync.Mutex
	pairWaiters map[string]<-chan struct{} // minted token -> "device connected" signal

	subMu sync.Mutex
	subs  map[string]subEntry // sub_id -> subscriber

	termMu sync.Mutex
	terms  map[string]termEntry // term_id -> open terminal

	relayMu   sync.Mutex
	channels  map[string]*relayChannel // chan_id -> paired client/node + pumps
	nodePeers map[string]*api.Peer     // node id -> live uplink peer (relay.open target)
	nextChan  atomic.Uint64            // chan_id allocator

	trust *trustStore // opaque hold of the network's trust-log chain (blind)
}

// NewServer builds a gateway Server over agg.
func NewServer(agg *Aggregator, nodeAuth, clientAuth func(token string) bool) *Server {
	s := &Server{
		agg: agg, nodeAuth: nodeAuth, clientAuth: clientAuth,
		pairWaiters: map[string]<-chan struct{}{},
		subs:        map[string]subEntry{},
		terms:       map[string]termEntry{},
	}
	s.channels = map[string]*relayChannel{}
	s.nodePeers = map[string]*api.Peer{}
	s.trust = &trustStore{}
	s.clientSrv = s.buildClientServer()
	s.clientSrv.SetRelayFrameHandler(s.forwardFromClient)
	return s
}

// SetClientTokens enables per-client token management; master is the admin token
// gating the clients.* methods. Call before serving.
func (s *Server) SetClientTokens(store *clienttoken.Store, master string) {
	s.clientTokens = store
	s.master = master
}

// SetPush enables the push.register/unregister/test methods. Call before serving.
func (s *Server) SetPush(store *push.Store, dispatcher *push.Dispatcher) {
	s.pushStore = store
	s.pushSender = dispatcher
}

// SetVAPIDPublicKey publishes the VAPID public key devices fetch via push.vapidKey.
func (s *Server) SetVAPIDPublicKey(key string) { s.vapidPubKey = key }

// SetVersion records the server binary's version, served via server.info. Call before serving.
func (s *Server) SetVersion(v string) { s.version = v }

// SetPublicURL records the gateway's reachable base URL (scheme://host) for the
// pairing QR. Safe to call repeatedly, e.g. once the tunnel URL is known.
func (s *Server) SetPublicURL(u string) { s.publicURL.Store(&u) }

func (s *Server) getPublicURL() string {
	if p := s.publicURL.Load(); p != nil {
		return *p
	}
	return ""
}

// addSub records a subscription, refusing (ok=false) if the sub_id is already in
// use so a reused id can't orphan the prior owner.
func (s *Server) addSub(subID, nodeID string, client api.Notifier) bool {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if _, exists := s.subs[subID]; exists {
		return false
	}
	s.subs[subID] = subEntry{client: client, nodeID: nodeID}
	return true
}

func (s *Server) dropSub(subID string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	delete(s.subs, subID)
}

func (s *Server) clientForSub(subID string) (api.Notifier, bool) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	e, ok := s.subs[subID]
	return e.client, ok
}

// subRef identifies a subscription and the node it targets.
type subRef struct{ subID, nodeID string }

// subsForClient returns the subs owned by a client (for disconnect cleanup).
func (s *Server) subsForClient(client api.Notifier) []subRef {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	var out []subRef
	for id, e := range s.subs {
		if e.client == client {
			out = append(out, subRef{id, e.nodeID})
		}
	}
	return out
}

// addTerm records an open terminal, refusing (ok=false) if the term_id is already
// in use so a reused/replayed id can't orphan the prior owner (leaking its node
// mirror) or hijack its output routing.
func (s *Server) addTerm(id, nodeID string, c api.Notifier) bool {
	s.termMu.Lock()
	if _, exists := s.terms[id]; exists {
		s.termMu.Unlock()
		return false
	}
	r := &termRelay{ch: make(chan termFrame, termRelayBuffer), stop: make(chan struct{})}
	s.terms[id] = termEntry{client: c, nodeID: nodeID, relay: r}
	s.termMu.Unlock()
	go relayTermOutput(c, r)
	return true
}

func (s *Server) dropTerm(id string) {
	s.termMu.Lock()
	e, ok := s.terms[id]
	delete(s.terms, id)
	s.termMu.Unlock()
	if ok && e.relay != nil {
		e.relay.stopRelay()
	}
}

func (s *Server) clientForTerm(id string) (api.Notifier, bool) {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	e, ok := s.terms[id]
	return e.client, ok
}

// termsForClient returns the open terminals owned by a client (for disconnect cleanup).
func (s *Server) termsForClient(client api.Notifier) []subRef {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	var out []subRef
	for id, e := range s.terms {
		if e.client == client {
			out = append(out, subRef{id, e.nodeID})
		}
	}
	return out
}

// termIDFromParams extracts the term_id field from a JSON params blob.
func termIDFromParams(params json.RawMessage) string {
	var v struct {
		TermID string `json:"term_id"`
	}
	_ = json.Unmarshal(params, &v)
	return v.TermID
}

// mustMarshal marshals v to JSON, ignoring errors (for well-known types only).
func mustMarshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// relayQueueDepth bounds a channel's per-direction pump queue. On overflow the
// channel is torn down (a dropped Noise record would desync the AEAD counter),
// isolated from other channels.
const relayQueueDepth = 64

// relayChannel is one client<->node E2E channel. The gateway forwards opaque
// frames between the two peers by chan_id, never reading the sealed Body. Two pump
// goroutines (one per direction) decouple the peers' read loops.
type relayChannel struct {
	chanID   string
	client   *api.Peer
	node     *api.Peer
	toNode   chan []byte
	toClient chan []byte
	stop     chan struct{}
	stopOnce sync.Once
}

// addChannel records a channel and starts its two pump goroutines.
func (s *Server) addChannel(chanID string, client, node *api.Peer) {
	ch := &relayChannel{
		chanID: chanID, client: client, node: node,
		toNode: make(chan []byte, relayQueueDepth), toClient: make(chan []byte, relayQueueDepth),
		stop: make(chan struct{}),
	}
	s.relayMu.Lock()
	s.channels[chanID] = ch
	s.relayMu.Unlock()
	go s.relayPump(ch, ch.toNode, node)
	go s.relayPump(ch, ch.toClient, client)
}

// dropChannel removes a channel and stops its pumps. It does not close the peers
// (they may host other channels). Idempotent.
func (s *Server) dropChannel(chanID string) {
	s.relayMu.Lock()
	ch, ok := s.channels[chanID]
	if ok {
		delete(s.channels, chanID)
	}
	s.relayMu.Unlock()
	if ok {
		ch.stopOnce.Do(func() { close(ch.stop) })
	}
}

// dropChannelsWhere tears down every channel whose relayChannel matches pred. It
// collects matches under relayMu, then drops them after unlocking (dropChannel
// re-acquires the lock).
func (s *Server) dropChannelsWhere(pred func(*relayChannel) bool) {
	s.relayMu.Lock()
	var toDrop []string
	for cid, ch := range s.channels {
		if pred(ch) {
			toDrop = append(toDrop, cid)
		}
	}
	s.relayMu.Unlock()
	for _, cid := range toDrop {
		s.dropChannel(cid)
	}
}

// relayPump drains q and forwards each frame to dst verbatim. A write error (dst
// gone or WriteTimeout) tears the channel down.
func (s *Server) relayPump(ch *relayChannel, q chan []byte, dst *api.Peer) {
	for {
		select {
		case <-ch.stop:
			return
		case raw := <-q:
			if err := dst.SendRawFrame(raw); err != nil {
				s.dropChannel(ch.chanID)
				return
			}
		}
	}
}

// enqueue hands a frame to a channel's pump. On a full queue it tears the channel
// down (never drops a frame — that would desync the sealed stream).
func (s *Server) enqueue(ch *relayChannel, q chan []byte, raw []byte) {
	select {
	case <-ch.stop:
	case q <- raw:
	default:
		s.dropChannel(ch.chanID)
	}
}

func (s *Server) forwardFromClient(f api.RelayFrame) {
	s.relayMu.Lock()
	ch := s.channels[f.Route.ChanID]
	s.relayMu.Unlock()
	if ch != nil {
		s.enqueue(ch, ch.toNode, f.Raw)
	}
}

func (s *Server) forwardFromNode(f api.RelayFrame) {
	s.relayMu.Lock()
	ch := s.channels[f.Route.ChanID]
	s.relayMu.Unlock()
	if ch != nil {
		s.enqueue(ch, ch.toClient, f.Raw)
	}
}

// addNodePeer records a live node uplink as a relay.open target.
func (s *Server) addNodePeer(id string, peer *api.Peer) {
	s.relayMu.Lock()
	s.nodePeers[id] = peer
	s.relayMu.Unlock()
}

// removeNodePeer drops a node uplink and tears down every channel bound to it.
func (s *Server) removeNodePeer(id string, peer *api.Peer) {
	s.relayMu.Lock()
	if s.nodePeers[id] == peer {
		delete(s.nodePeers, id)
	}
	s.relayMu.Unlock()
	s.dropChannelsWhere(func(ch *relayChannel) bool { return ch.node == peer })
}

// routeByNodeID builds a handler that requires node_id and routes to that node.
func (s *Server) routeByNodeID(method string) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		nodeID, err := nodeIDFromParams(params)
		if err != nil || nodeID == "" {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: method + " requires node_id"}
		}
		return s.agg.RouteToNode(ctx, nodeID, method, params)
	}
}

// routeByNodeIDOrSole defaults to the sole connected node when node_id is omitted,
// so a single-node setup needs no picker.
func (s *Server) routeByNodeIDOrSole(method string) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		nodeID, err := nodeIDFromParams(params)
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: method + " requires node_id"}
		}
		if nodeID == "" {
			if nodeID = s.agg.SoleNode(); nodeID == "" {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: method + " requires node_id"}
			}
		}
		return s.agg.RouteToNode(ctx, nodeID, method, params)
	}
}

// SetLogger enables per-request logging (nil disables).
func (s *Server) SetLogger(l *slog.Logger) { s.clientSrv.SetLogger(l) }

// Handler returns the gateway's HTTP handler with the /node and /client routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/node", s.nodeHandler())
	mux.Handle("/client", s.clientHandler())
	return mux
}

// clientHandler authenticates a /client connection and tags it with a Principal
// (admin when the master token is presented). Mirrors api.Server.WSHandler but
// threads the Principal so clients.* can require admin and a minted token can be
// promoted on its first connection.
func (s *Server) clientHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := api.BearerToken(r)
		if s.clientAuth != nil && !s.clientAuth(tok) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := api.AcceptWS(w, r)
		if err != nil {
			return
		}
		admin := s.master != "" && tok == s.master
		ctx := api.WithPrincipal(context.Background(), api.Principal{Admin: admin})
		s.clientSrv.ServeConnContext(ctx, conn) // blocks until the conn closes
	})
}

// buildClientServer wires the client-facing JSON-RPC: reads from the merged view,
// control calls routed to the owning node, and a per-connection merged event stream.
func (s *Server) buildClientServer() *api.Server {
	srv := api.NewServer()

	// ping is a no-op latency probe to the gateway itself (not routed to a node).
	srv.Handle(api.MethodPing, func(context.Context, json.RawMessage) (any, error) { return nil, nil })

	srv.Handle(api.MethodSessionsList, func(_ context.Context, _ json.RawMessage) (any, error) {
		return s.agg.Snapshot(), nil
	})
	// Fan the rescan out to every node, then return the merged view. Per-node
	// errors stay in-band so one unreachable node can't fail the whole refresh.
	srv.Handle(api.MethodSessionsRefresh, func(ctx context.Context, params json.RawMessage) (any, error) {
		s.agg.Fanout(ctx, api.MethodSessionsRefresh, params)
		return s.agg.Snapshot(), nil
	})
	for _, method := range clientRoutedMethods {
		m := method
		srv.Handle(m, func(ctx context.Context, params json.RawMessage) (any, error) {
			return s.agg.Route(ctx, m, params)
		})
	}

	// server.info returns version + connected nodes (for settings view and spawn picker).
	srv.Handle(api.MethodServerInfo, func(context.Context, json.RawMessage) (any, error) {
		return api.ServerInfo{Version: s.version, Nodes: s.agg.Nodes()}, nil
	})

	// nodes.list is the E2E discovery view: each node's id/label/caps + Noise pubkey
	// + online flag. Additive alongside server.info.
	srv.Handle(api.MethodNodesList, func(context.Context, json.RawMessage) (any, error) {
		return api.NodesListResult{Nodes: s.agg.Roster()}, nil
	})

	// trustlog.pull serves the current (opaque) trust-log chain to a client. The
	// client verifies it against its pinned genesis; the gateway never does.
	srv.Handle(api.MethodTrustLogPull, func(context.Context, json.RawMessage) (any, error) {
		return api.TrustLogChain{Chain: s.trust.current()}, nil
	})

	// relay.open pairs this client with a node into a chan_id channel for E2E frames.
	srv.Handle(api.MethodRelayOpen, func(ctx context.Context, params json.RawMessage) (any, error) {
		n, ok := api.NotifierFrom(ctx)
		clientPeer, isPeer := n.(*api.Peer)
		if !ok || !isPeer {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no client peer"}
		}
		p, err := api.Decode[api.RelayOpenParams](params)
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
		}
		s.relayMu.Lock()
		nodePeer := s.nodePeers[p.NodeID]
		s.relayMu.Unlock()
		if nodePeer == nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node: " + p.NodeID}
		}
		chanID := "c" + strconv.FormatUint(s.nextChan.Add(1), 10)
		s.addChannel(chanID, clientPeer, nodePeer)
		return api.RelayOpenResult{ChanID: chanID}, nil
	})

	// relay.close tears down a channel this client owns.
	srv.Handle(api.MethodRelayClose, func(ctx context.Context, params json.RawMessage) (any, error) {
		n, _ := api.NotifierFrom(ctx)
		clientPeer, _ := n.(*api.Peer)
		p, err := api.Decode[api.RelayCloseParams](params)
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
		}
		s.relayMu.Lock()
		ch := s.channels[p.ChanID]
		s.relayMu.Unlock()
		if ch != nil && ch.client == clientPeer {
			s.dropChannel(p.ChanID)
		}
		return nil, nil
	})

	srv.Handle(api.MethodSessionSpawn, s.routeByNodeIDOrSole(api.MethodSessionSpawn))
	srv.Handle(api.MethodSessionResume, s.routeByNodeIDOrSole(api.MethodSessionResume))
	srv.Handle(api.MethodAgentsList, s.routeByNodeIDOrSole(api.MethodAgentsList))

	// History projects aggregate across machines: fan out, stamp origin node, order
	// newest-first (RFC3339 UTC sorts lexically).
	srv.Handle(api.MethodSessionsHistoryProjects, func(ctx context.Context, params json.RawMessage) (any, error) {
		all := []session.HistoryProject{} // non-nil so empty marshals as [], not null
		for _, r := range s.agg.Fanout(ctx, api.MethodSessionsHistoryProjects, params) {
			if r.Err != nil {
				continue
			}
			var projects []session.HistoryProject
			if json.Unmarshal(r.Result, &projects) != nil {
				continue
			}
			for i := range projects {
				projects[i].NodeID = r.NodeID
				projects[i].NodeLabel = r.Label
			}
			all = append(all, projects...)
		}
		sort.SliceStable(all, func(i, j int) bool { return all[i].LastActivity > all[j].LastActivity })
		return all, nil
	})
	// History transcript and tool detail are per-machine: route by node_id.
	srv.Handle(api.MethodSessionsHistoryTranscript, s.routeByNodeID(api.MethodSessionsHistoryTranscript))
	srv.Handle(api.MethodSessionHistoryToolDetail, s.routeByNodeID(api.MethodSessionHistoryToolDetail))
	srv.Handle(api.MethodSessionExport, s.routeByNodeIDOrSole(api.MethodSessionExport))
	// History sessions route by node_id, then get stamped with that node's id/label
	// so a client can open a transcript by the session's own node_id.
	srv.Handle(api.MethodSessionsHistorySessions, func(ctx context.Context, params json.RawMessage) (any, error) {
		nodeID, err := nodeIDFromParams(params)
		if err != nil || nodeID == "" {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: api.MethodSessionsHistorySessions + " requires node_id"}
		}
		res, err := s.agg.RouteToNode(ctx, nodeID, api.MethodSessionsHistorySessions, params)
		if err != nil {
			return nil, err
		}
		var page session.HistorySessionPage
		if json.Unmarshal(res, &page) != nil {
			return res, nil // unexpected shape; pass through
		}
		label := s.agg.NodeLabel(nodeID)
		for i := range page.Items {
			page.Items[i].NodeID = nodeID
			page.Items[i].NodeLabel = label
		}
		return page, nil
	})

	// transcript.subscribe: record in the sub table, then route to the owning node
	// (registered explicitly because it needs that bookkeeping).
	srv.Handle(api.MethodTranscriptSubscribe, func(ctx context.Context, params json.RawMessage) (any, error) {
		client, ok := api.NotifierFrom(ctx)
		if !ok {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no client notifier"}
		}
		p, err := api.Decode[api.TranscriptSubscribeParams](params)
		if err != nil {
			return nil, err
		}
		nodeID, _, ok := session.SplitCompositeID(p.SessionID)
		if !ok {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session id is not gateway-qualified: " + p.SessionID}
		}
		// Record before routing so an early delta finds its client. Refuse a
		// sub_id already in flight rather than overwriting the prior subscriber.
		if !s.addSub(p.SubID, nodeID, client) {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "sub_id already in use: " + p.SubID}
		}
		res, err := s.agg.Route(ctx, api.MethodTranscriptSubscribe, params)
		if err != nil {
			s.dropSub(p.SubID)
			return nil, err
		}
		return res, nil
	})

	// terminal.open: record in the term table, then route to the owning node.
	//
	// Authorization: any authenticated /client may open a terminal on any session;
	// the ownership checks below only bind a term_id to its opener. Terminal attach
	// is full read+write shell access, so a client token is a fully privileged
	// credential — same single-user/multi-device model as session.input.
	srv.Handle(api.MethodTerminalOpen, func(ctx context.Context, params json.RawMessage) (any, error) {
		client, ok := api.NotifierFrom(ctx)
		if !ok {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no client notifier"}
		}
		p, err := api.Decode[api.TerminalOpenParams](params)
		if err != nil {
			return nil, err
		}
		nodeID, _, ok := session.SplitCompositeID(p.SessionID)
		if !ok {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session id is not gateway-qualified: " + p.SessionID}
		}
		// Record before routing so early output finds its client. Refuse a term_id
		// already in use rather than overwriting (which would orphan the prior
		// owner and leak its node-side mirror).
		if !s.addTerm(p.TermID, nodeID, client) {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "term_id already in use: " + p.TermID}
		}
		res, err := s.agg.Route(ctx, api.MethodTerminalOpen, params)
		if err != nil {
			s.dropTerm(p.TermID)
			return nil, err
		}
		return res, nil
	})

	// terminal.input / terminal.resize: look up node in the term table and route
	// there — but only for the client that opened the term, so a client that
	// learns another's term_id can't inject input into or resize its PTY.
	for _, method := range []string{api.MethodTerminalInput, api.MethodTerminalResize} {
		m := method
		srv.Handle(m, func(ctx context.Context, params json.RawMessage) (any, error) {
			client, ok := api.NotifierFrom(ctx)
			if !ok {
				return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no client notifier"}
			}
			termID := termIDFromParams(params)
			s.termMu.Lock()
			e, ok := s.terms[termID]
			s.termMu.Unlock()
			if !ok || e.client != client {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown term_id: " + termID}
			}
			return s.agg.RouteToNode(ctx, e.nodeID, m, params)
		})
	}
	// terminal.close: atomic read+delete under one lock hold to prevent a TOCTOU
	// race where two concurrent closes both route to the node. Only the owning
	// client may close its term (a non-owner is rejected and the term is kept).
	srv.Handle(api.MethodTerminalClose, func(ctx context.Context, params json.RawMessage) (any, error) {
		client, ok := api.NotifierFrom(ctx)
		if !ok {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no client notifier"}
		}
		termID := termIDFromParams(params)
		s.termMu.Lock()
		e, owned := s.terms[termID]
		if owned = owned && e.client == client; owned {
			delete(s.terms, termID)
		}
		s.termMu.Unlock()
		if !owned {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown term_id: " + termID}
		}
		if e.relay != nil {
			e.relay.stopRelay()
		}
		return s.agg.RouteToNode(ctx, e.nodeID, api.MethodTerminalClose, params)
	})

	// transcript.unsubscribe: remove from the sub table and route to the node.
	srv.Handle(api.MethodTranscriptUnsubscribe, func(ctx context.Context, params json.RawMessage) (any, error) {
		p, err := api.Decode[api.TranscriptUnsubscribeParams](params)
		if err != nil {
			return nil, err
		}
		s.subMu.Lock()
		e, ok := s.subs[p.SubID]
		delete(s.subs, p.SubID)
		s.subMu.Unlock()
		if ok {
			_, _ = s.agg.RouteToNode(ctx, e.nodeID, api.MethodTranscriptUnsubscribe, params)
		}
		return nil, nil
	})

	// Stream the merged registry AND the node roster to each client: snapshots
	// first, then live events (session.event + node.event) on one goroutine.
	srv.OnConnect(func(n api.Notifier) func() {
		events, cancel := s.agg.Subscribe()
		for _, sess := range s.agg.Snapshot() {
			_ = n.Notify(api.MethodSessionEvent, registry.Event{Type: registry.EventAdded, Session: sess})
		}
		rosterEvents, rosterCancel := s.agg.SubscribeRoster()
		for _, nd := range s.agg.Roster() {
			_ = n.Notify(api.MethodNodeEvent, api.NodeEvent{Type: api.NodeEventAdded, Node: nd})
		}
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-done:
					return
				case ev, ok := <-events:
					if !ok {
						return
					}
					if err := n.Notify(api.MethodSessionEvent, ev); err != nil {
						return
					}
				case ev, ok := <-rosterEvents:
					if !ok {
						return
					}
					if err := n.Notify(api.MethodNodeEvent, ev); err != nil {
						return
					}
				}
			}
		}()
		return func() {
			close(done)
			cancel()
			rosterCancel()
			if clientPeer, ok := n.(*api.Peer); ok {
				s.dropChannelsWhere(func(ch *relayChannel) bool { return ch.client == clientPeer })
			}
			for _, sub := range s.subsForClient(n) {
				s.dropSub(sub.subID)
				_, _ = s.agg.RouteToNode(context.Background(), sub.nodeID,
					api.MethodTranscriptUnsubscribe, mustMarshal(api.TranscriptUnsubscribeParams{SubID: sub.subID}))
			}
			for _, ref := range s.termsForClient(n) {
				s.dropTerm(ref.subID)
				_, _ = s.agg.RouteToNode(context.Background(), ref.nodeID,
					api.MethodTerminalClose, mustMarshal(api.TerminalCloseParams{TermID: ref.subID}))
			}
		}
	})

	s.registerClientAdmin(srv)
	s.registerPush(srv)
	return srv
}

// registerPush wires the device push methods. Open to any authenticated /client
// (not admin-only): a device registers its push target keyed by a stable device id.
func (s *Server) registerPush(srv *api.Server) {
	unavailable := &api.RPCError{Code: api.CodeInvalidRequest, Message: "push notifications not enabled on this gateway"}
	badDevice := &api.RPCError{Code: api.CodeInvalidRequest, Message: "push: device_id required"}

	srv.Handle(api.MethodPushRegister, func(_ context.Context, params json.RawMessage) (any, error) {
		if s.pushStore == nil {
			return nil, unavailable
		}
		p, err := api.Decode[api.PushRegisterParams](params)
		if err != nil {
			return nil, err
		}
		if p.DeviceID == "" {
			return nil, badDevice
		}
		if p.Endpoint == "" {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push.register: endpoint required"}
		}
		t := push.Target{Endpoint: p.Endpoint, P256dh: p.P256dh, Auth: p.Auth}
		if err := s.pushStore.Upsert(p.DeviceID, t); err != nil {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
		}
		return nil, nil
	})

	srv.Handle(api.MethodPushUnregister, func(_ context.Context, params json.RawMessage) (any, error) {
		if s.pushStore == nil {
			return nil, unavailable
		}
		p, err := api.Decode[api.PushDeviceRef](params)
		if err != nil {
			return nil, err
		}
		if p.DeviceID == "" {
			return nil, badDevice
		}
		if err := s.pushStore.Remove(p.DeviceID); err != nil {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
		}
		return nil, nil
	})

	// test sends a notification through the real backend to confirm end-to-end delivery.
	srv.Handle(api.MethodPushTest, func(ctx context.Context, params json.RawMessage) (any, error) {
		if s.pushStore == nil || s.pushSender == nil {
			return nil, unavailable
		}
		p, err := api.Decode[api.PushDeviceRef](params)
		if err != nil {
			return nil, err
		}
		if p.DeviceID == "" {
			return nil, badDevice
		}
		n := push.Notification{
			Title: "argus",
			Body:  "Test notification — push is working.",
			Data:  map[string]string{"test": "1"},
		}
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := s.pushSender.SendTo(sctx, p.DeviceID, n); err != nil {
			code := api.CodeInternalError
			if errors.Is(err, push.ErrGone) {
				// Target dead and already pruned; client should mint a fresh endpoint.
				code = api.CodePushGone
			}
			return nil, &api.RPCError{Code: code, Message: err.Error()}
		}
		return nil, nil
	})

	// vapidKey serves the gateway's VAPID public key for Web Push subscription.
	srv.Handle(api.MethodPushVAPIDKey, func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.PushVAPIDKey{Key: s.vapidPubKey}, nil
	})
}

// requireAdmin wraps h to run only for connections that presented the master token.
func requireAdmin(h api.HandlerFunc) api.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		if !api.PrincipalFrom(ctx).Admin {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unauthorized: admin token required"}
		}
		return h(ctx, params)
	}
}

// registerClientAdmin wires the client-token management methods (admin-only).
func (s *Server) registerClientAdmin(srv *api.Server) {
	unavailable := &api.RPCError{Code: api.CodeInvalidRequest, Message: "client token management not enabled on this gateway"}

	// pairStart mints a pending token and returns it with the public URL for a QR.
	srv.Handle(api.MethodClientsPairStart, requireAdmin(func(context.Context, json.RawMessage) (any, error) {
		if s.clientTokens == nil {
			return nil, unavailable
		}
		tok, err := clienttoken.GenerateToken()
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
		}
		ch := s.clientTokens.Pend(tok)
		s.pairMu.Lock()
		s.pairWaiters[tok] = ch
		s.pairMu.Unlock()
		return api.PairStartResult{Token: tok, URL: s.getPublicURL()}, nil
	}))

	// pairAwait blocks until the token's device connects (waiter closed on promotion)
	// or the pairing window elapses.
	srv.Handle(api.MethodClientsPairAwait, requireAdmin(func(ctx context.Context, params json.RawMessage) (any, error) {
		if s.clientTokens == nil {
			return nil, unavailable
		}
		p, err := api.Decode[api.PairAwaitParams](params)
		if err != nil {
			return nil, err
		}
		s.pairMu.Lock()
		ch, ok := s.pairWaiters[p.Token]
		delete(s.pairWaiters, p.Token)
		s.pairMu.Unlock()
		if !ok {
			return api.PairAwaitResult{Connected: false}, nil
		}
		select {
		case <-ch:
			return api.PairAwaitResult{Connected: true}, nil
		case <-time.After(clienttoken.PendTTL):
			s.clientTokens.CancelPend(p.Token)
			return api.PairAwaitResult{Connected: false}, nil
		case <-ctx.Done():
			s.clientTokens.CancelPend(p.Token)
			return nil, ctx.Err()
		}
	}))

	// list returns the persisted client tokens.
	srv.Handle(api.MethodClientsList, requireAdmin(func(context.Context, json.RawMessage) (any, error) {
		if s.clientTokens == nil {
			return nil, unavailable
		}
		recs, err := s.clientTokens.List()
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
		}
		out := make([]api.ClientTokenInfo, 0, len(recs))
		for _, r := range recs {
			out = append(out, api.ClientTokenInfo{Token: r.Token, CreatedAt: r.CreatedAt})
		}
		return out, nil
	}))

	// remove revokes a client token by deleting its record.
	srv.Handle(api.MethodClientsRemove, requireAdmin(func(_ context.Context, params json.RawMessage) (any, error) {
		if s.clientTokens == nil {
			return nil, unavailable
		}
		p, err := api.Decode[api.ClientRemoveParams](params)
		if err != nil {
			return nil, err
		}
		if err := s.clientTokens.Remove(p.Token); err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
		}
		return nil, nil
	}))
}

func (s *Server) nodeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.nodeAuth != nil && !s.nodeAuth(api.BearerToken(r)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := api.AcceptWS(w, r)
		if err != nil {
			return
		}
		s.serveNode(conn)
	})
}

// Node uplink keepalive: pings detect a half-open link (host vanished without a
// TCP FIN) promptly. Closing after nodeKeepaliveFailures unanswered pings fires
// Done into the aggregator's offline → grace → removal path; two failures ride out
// a transient blip so a briefly busy node isn't dropped.
const (
	nodeKeepaliveInterval = 15 * time.Second
	nodeKeepaliveTimeout  = 5 * time.Second
	nodeKeepaliveFailures = 2
)

// maxClosedOrphans caps the per-uplink debounce set for orphaned terminal output
// so a long-lived node connection with heavy attach churn can't grow it without
// bound. Far above any plausible count of concurrently-dying orphans.
const maxClosedOrphans = 4096

// nodeDispatch serves the requests a node issues down its uplink. Today that is
// only trust-log distribution (offer + pull); everything else is method-not-found.
// Kept separate from the client server so trustlog.offer is reachable ONLY here —
// clients are supplicants and must not publish trust state.
func (s *Server) nodeDispatch(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case api.MethodTrustLogOffer:
		p, err := api.Decode[api.TrustLogChain](params)
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
		}
		s.trust.offer(p.Chain)
		return nil, nil
	case api.MethodTrustLogPull:
		return api.TrustLogChain{Chain: s.trust.current()}, nil
	default:
		return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
	}
}

// serveNode adopts an accepted node uplink: learn its identity, register it as a
// source, and block until it disconnects.
func (s *Server) serveNode(conn net.Conn) {
	events := make(chan registry.Event, 64)
	// peerRef lets the OnNotify closure reference peer before NewPeer returns.
	// Atomic to satisfy the race detector (OnNotify runs in a goroutine).
	var peerRef atomic.Pointer[api.Peer]
	// closedOrphans debounces the terminal.close we send for output on an unknown
	// term: a chatty dead term would otherwise spawn a goroutine + node Call per
	// frame. Only touched from OnNotify, which the peer runs serially, so no lock.
	closedOrphans := map[string]bool{}
	peer := api.NewPeer(conn, api.PeerOptions{
		KeepaliveInterval:         nodeKeepaliveInterval,
		KeepaliveTimeout:          nodeKeepaliveTimeout,
		KeepaliveFailureThreshold: nodeKeepaliveFailures,
		Dispatch:                  s.nodeDispatch,
		OnRelayFrame:              func(f api.RelayFrame) { s.forwardFromNode(f) },
		OnNotify: func(n api.Notification) {
			switch n.Method {
			case api.MethodSessionEvent:
				var ev registry.Event
				if json.Unmarshal(n.Params, &ev) != nil {
					return
				}
				select {
				case events <- ev:
				default: // drop for a slow aggregator
				}
			case api.MethodTranscriptDelta:
				var d api.TranscriptDelta
				if json.Unmarshal(n.Params, &d) != nil {
					return
				}
				if client, ok := s.clientForSub(d.SubID); ok {
					_ = client.Notify(api.MethodTranscriptDelta, d)
				} else {
					// Orphaned poller: node pushing deltas for an untracked sub. Tell it
					// to stop. unsubscribe is a node request handler, so use Call not Notify.
					p := peerRef.Load()
					if p == nil {
						return // setup race: peer not stored yet; poller dies with the link
					}
					go func() {
						_ = p.Call(api.MethodTranscriptUnsubscribe,
							api.TranscriptUnsubscribeParams{SubID: d.SubID}, nil)
					}()
				}
			case api.MethodTerminalOutput:
				var o api.TerminalOutput
				if json.Unmarshal(n.Params, &o) != nil {
					return
				}
				s.termMu.Lock()
				e, ok := s.terms[o.TermID]
				s.termMu.Unlock()
				if ok && e.relay != nil {
					// Hand off to the term's relay goroutine so a viewer that has
					// stopped draining can't stall this node's read loop.
					select {
					case <-e.relay.stop:
						// Term is being torn down elsewhere (e.g. a concurrent
						// close); drop this late frame.
					case e.relay.ch <- termFrame{method: api.MethodTerminalOutput, params: o}:
					default:
						// Buffer full: the client isn't keeping up. Drop its connection
						// so it re-attaches with a clean baseline, rather than blocking
						// the loop or corrupting the stream by dropping chunks.
						if closer, ok := e.client.(interface{ Close() error }); ok {
							_ = closer.Close()
						}
					}
				} else if !ok && !closedOrphans[o.TermID] {
					// Orphaned output: node pushing to an untracked term. Tell it to
					// close, once per term_id (ids are unique per attach, so a closed
					// orphan won't legitimately reappear).
					if len(closedOrphans) >= maxClosedOrphans {
						// Bound memory on a long-lived uplink with churn. Ids are
						// unique per attach, so at worst a still-chatty orphan gets
						// one extra close after the reset.
						closedOrphans = map[string]bool{}
					}
					closedOrphans[o.TermID] = true
					if p := peerRef.Load(); p != nil {
						go func() { _ = p.Call(api.MethodTerminalClose, api.TerminalCloseParams{TermID: o.TermID}, nil) }()
					}
				}
			case api.MethodTerminalExited:
				var o api.TerminalExited
				if json.Unmarshal(n.Params, &o) != nil {
					return
				}
				// PTY ended node-side: drop the dead term and route the exit through
				// the relay as its terminating frame, so it lands after any queued
				// output; fall back to a direct notify if the buffer is full.
				s.termMu.Lock()
				e, ok := s.terms[o.TermID]
				delete(s.terms, o.TermID)
				s.termMu.Unlock()
				if !ok {
					return
				}
				delivered := false
				if e.relay != nil {
					select {
					case e.relay.ch <- termFrame{method: api.MethodTerminalExited, params: o, fin: true}:
						delivered = true
					default:
					}
				}
				if !delivered {
					if e.relay != nil {
						e.relay.stopRelay()
					}
					_ = e.client.Notify(api.MethodTerminalExited, o)
				}
			}
		},
	})
	peerRef.Store(peer)
	defer peer.Close()

	var id api.IdentifyResult
	if err := peer.Call(api.MethodNodeIdentify, nil, &id); err != nil || id.ID == "" {
		return
	}
	s.agg.AddSource(NewRemoteSource(id.ID, id.Label, id.Version, id.IdentityPubKey, id.Capabilities, peer, events))
	s.addNodePeer(id.ID, peer)
	defer s.removeNodePeer(id.ID, peer)
	<-peer.Done()
}
