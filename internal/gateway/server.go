package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/selfupgrade"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// Server exposes an Aggregator over /node (node uplinks) and /client (consumers).
// Auth predicates gate each; nil = allow all (local/dev only).
type Server struct {
	agg        *Aggregator
	nodeAuth   func(token string) bool
	clientAuth func(token string) bool
	clientSrv  *api.Server

	clientTokens        *clienttoken.Store
	pushDeliverer       push.Deliverer
	vapidPubKey         string
	pushPortTokenSetter func(string) error
	pushPortStatus      func() bool
	master              string
	version             string
	publicURL           atomic.Pointer[string]

	nodeKeepaliveInterval time.Duration
	nodeKeepaliveTimeout  time.Duration
	nodeKeepaliveFailures int

	pairMu      sync.Mutex
	pairWaiters map[string]<-chan struct{}

	relayMu   sync.Mutex
	channels  map[string]*relayChannel // chan_id -> paired client/node + pumps
	nodePeers map[string]*api.Peer     // node id -> live uplink peer (relay.open target)
	// nodeOpening counts the channels each node uplink is opening, so the
	// channel limit holds while an open is between its check and addChannel.
	nodeOpening map[*api.Peer]int
	// clientConns tracks live /client connections by token hash so revoking a
	// token can close them.
	clientConns map[string]map[net.Conn]struct{}
	// afterClientAccept is a test hook run between WebSocket accept and tracking.
	afterClientAccept func()
	entries           *trustlog.EntryStore // retained trust-log entries (blind)
	nextChan          atomic.Uint64        // chan_id allocator
	log               *slog.Logger         // relay lifecycle logging; nil disables
}

// NewServer builds a gateway Server over agg using the relay path for all connections.
func NewServer(agg *Aggregator, nodeAuth, clientAuth func(token string) bool) *Server {
	s := &Server{
		agg: agg, nodeAuth: nodeAuth, clientAuth: clientAuth,
		pairWaiters: map[string]<-chan struct{}{},
		channels:    map[string]*relayChannel{},
		nodePeers:   map[string]*api.Peer{},
		clientConns: map[string]map[net.Conn]struct{}{},
		entries:     trustlog.NewEntryStore(),

		nodeKeepaliveInterval: api.DefaultKeepaliveInterval,
		nodeKeepaliveTimeout:  api.DefaultKeepaliveTimeout,
		nodeKeepaliveFailures: api.DefaultKeepaliveFailures,
	}
	s.clientSrv = s.buildClientServer()
	s.clientSrv.SetRelayFrameHandler(s.forwardFrame)
	return s
}

// SetClientTokens enables per-client token management; master is the admin token
// gating the clients.* methods. Call before serving.
func (s *Server) SetClientTokens(store *clienttoken.Store, master string) {
	s.clientTokens = store
	s.master = master
}

// SetPushDeliverer wires the egress for the push.deliver node→gateway RPC.
func (s *Server) SetPushDeliverer(d push.Deliverer) { s.pushDeliverer = d }

// SetVAPIDPublicKey publishes the VAPID public key devices fetch via push.vapidKey.
func (s *Server) SetVAPIDPublicKey(key string) { s.vapidPubKey = key }

// SetPushPortTokenSetter injects a callback that persists and live-applies a new
// PushPort instance token. When unset, pushport.setToken returns "not enabled".
func (s *Server) SetPushPortTokenSetter(fn func(string) error) { s.pushPortTokenSetter = fn }

// SetPushPortStatus injects a callback reporting whether the gateway holds a
// PushPort token, surfaced on server.info.
func (s *Server) SetPushPortStatus(fn func() bool) { s.pushPortStatus = fn }

// SetVersion records the server binary's version, served via server.info. Call before serving.
func (s *Server) SetVersion(v string) { s.version = v }

// SetSelfUpgrade serves the server.* upgrade methods to clients. Call before serving.
func (s *Server) SetSelfUpgrade(svc *selfupgrade.Service) { svc.Register(s.clientSrv) }

// SetPublicURL records the gateway's reachable base URL (scheme://host) for the
// pairing QR. Safe to call repeatedly, e.g. once the tunnel URL is known.
func (s *Server) SetPublicURL(u string) { s.publicURL.Store(&u) }

func (s *Server) getPublicURL() string {
	if p := s.publicURL.Load(); p != nil {
		return *p
	}
	return ""
}

// SetLogger enables per-request logging on the client surface and relay lifecycle
// logging (nil disables).
func (s *Server) SetLogger(l *slog.Logger) {
	s.log = l
	s.clientSrv.SetLogger(l)
}

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
		if s.afterClientAccept != nil {
			s.afterClientAccept()
		}
		admin := s.master != "" && tok == s.master
		var hash string
		if tok != "" {
			hash = clienttoken.HashToken(tok)
			s.trackClientConn(hash, conn)
			defer s.untrackClientConn(hash, conn)
			// A removal before tracking is invisible to closeClientsWithToken.
			if !admin && s.clientTokens != nil && !s.clientTokens.Has(hash) {
				_ = conn.Close()
				return
			}
		}
		ctx := api.WithPrincipal(context.Background(), api.Principal{Admin: admin, TokenHash: hash})
		s.clientSrv.ServeConnContext(ctx, conn)
	})
}

func (s *Server) trackClientConn(hash string, c net.Conn) {
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	if s.clientConns[hash] == nil {
		s.clientConns[hash] = map[net.Conn]struct{}{}
	}
	s.clientConns[hash][c] = struct{}{}
}

func (s *Server) untrackClientConn(hash string, c net.Conn) {
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	delete(s.clientConns[hash], c)
	if len(s.clientConns[hash]) == 0 {
		delete(s.clientConns, hash)
	}
}

// closeClientsWithToken closes every /client connection that authenticated with
// the token hash. The connection teardown drops its relay channels.
func (s *Server) closeClientsWithToken(hash string) {
	s.relayMu.Lock()
	conns := make([]net.Conn, 0, len(s.clientConns[hash]))
	for c := range s.clientConns[hash] {
		conns = append(conns, c)
	}
	s.relayMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// registerPush wires the gateway's push surface. Only push.vapidKey is served:
// a device fetches the VAPID key here, then registers its push target on its
// owning node (over relay channels), never on the blind gateway.
func (s *Server) registerPush(srv *api.Server) {
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

func (s *Server) registerPushPortAdmin(srv *api.Server) {
	srv.Handle(api.MethodPushPortSetToken, requireAdmin(func(_ context.Context, params json.RawMessage) (any, error) {
		var p api.PushPortSetTokenParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "pushport.setToken: bad params"}
		}
		if p.Token == "" {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "pushport.setToken: empty token"}
		}
		if s.pushPortTokenSetter == nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "pushport not enabled on this gateway"}
		}
		if err := s.pushPortTokenSetter(p.Token); err != nil {
			return nil, err
		}
		return nil, nil
	}))
}

// registerClientAdmin wires the client-token management methods (admin-only).
func (s *Server) registerClientAdmin(srv *api.Server) {
	unavailable := &api.RPCError{Code: api.CodeInvalidRequest, Message: "client token management not enabled on this gateway"}

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
		s.closeClientsWithToken(p.Token)
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

// SetNodeKeepalive tunes this server's node-uplink heartbeat. Call before serving.
// Pings detect a half-open link (host vanished without a TCP FIN) promptly; closing
// after failures unanswered pings fires Done into the aggregator's offline → grace →
// removal path. Defaults (api.DefaultKeepalive*) ride out a transient blip so a
// briefly busy node is not dropped.
func (s *Server) SetNodeKeepalive(interval, timeout time.Duration, failures int) {
	s.nodeKeepaliveInterval, s.nodeKeepaliveTimeout, s.nodeKeepaliveFailures = interval, timeout, failures
}

// nodeIdentifyTimeout bounds the post-auth identify handshake on a node uplink.
const nodeIdentifyTimeout = 30 * time.Second

var discardLog = slog.New(slog.DiscardHandler)

func (s *Server) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return discardLog
}

// relayQueueDepth bounds a channel's per-direction pump queue. On overflow the
// channel is torn down (a dropped Noise record would desync the AEAD counter),
// isolated from other channels.
const relayQueueDepth = 64

// maxChannelsPerClient caps how many relay channels one client connection may hold
// open at once. Each channel costs two goroutines plus two relayQueueDepth buffers,
// so an uncapped client could exhaust node/gateway resources.
const maxChannelsPerClient = 64

// maxChannelsPerNode caps how many relay channels one node uplink may open to
// other nodes, for the same reason as maxChannelsPerClient.
const maxChannelsPerNode = 64

// relayChannel is one client<->node or node<->node E2E channel. The gateway
// forwards opaque frames between the two peers by chan_id, never reading the
// sealed Body. For a node-opened channel (fromNode), client is the opener's
// node uplink and node is the target's.
type relayChannel struct {
	chanID   string
	client   *api.Peer
	node     *api.Peer
	fromNode bool
	toNode   chan queuedFrame
	toClient chan queuedFrame
	stop     chan struct{}
	stopOnce sync.Once
}

type queuedFrame struct {
	enc api.Encoding
	raw []byte
}

func (s *Server) addChannel(chanID string, client, node *api.Peer, fromNode bool) {
	ch := &relayChannel{
		chanID: chanID, client: client, node: node, fromNode: fromNode,
		toNode: make(chan queuedFrame, relayQueueDepth), toClient: make(chan queuedFrame, relayQueueDepth),
		stop: make(chan struct{}),
	}
	s.relayMu.Lock()
	s.channels[chanID] = ch
	s.relayMu.Unlock()
	go s.relayPump(ch, ch.toNode, node)
	go s.relayPump(ch, ch.toClient, client)
	s.logger().Info("relay channel opened", "chan", chanID)
}

// dropChannel is idempotent.
func (s *Server) dropChannel(chanID, reason string) {
	s.relayMu.Lock()
	ch, ok := s.channels[chanID]
	if ok {
		delete(s.channels, chanID)
	}
	s.relayMu.Unlock()
	if ok {
		ch.stopOnce.Do(func() { close(ch.stop) })
		s.logger().Info("relay channel dropped", "chan", chanID, "reason", reason)
		// The node holds per-channel state (event stream, transcript pollers) that
		// only it can free. Fails harmlessly when the node uplink is what closed.
		go func() { _ = ch.node.Notify(api.MethodRelayClosed, api.RelayCloseParams{ChanID: chanID}) }()
		if ch.fromNode {
			go func() { _ = ch.client.Notify(api.MethodRelayClosed, api.RelayCloseParams{ChanID: chanID}) }()
		}
	}
}

func (s *Server) dropChannelsWhere(reason string, pred func(*relayChannel) bool) {
	s.relayMu.Lock()
	var toDrop []string
	for cid, ch := range s.channels {
		if pred(ch) {
			toDrop = append(toDrop, cid)
		}
	}
	s.relayMu.Unlock()
	for _, cid := range toDrop {
		s.dropChannel(cid, reason)
	}
}

func (s *Server) relayPump(ch *relayChannel, q chan queuedFrame, dst *api.Peer) {
	for {
		select {
		case <-ch.stop:
			return
		case f := <-q:
			if err := dst.SendRawFrame(f.enc, f.raw); err != nil {
				s.dropChannel(ch.chanID, "frame write to peer failed: "+err.Error())
				return
			}
		}
	}
}

// enqueue tears the channel down on a full queue (never drops a frame — that
// would desync the sealed stream).
func (s *Server) enqueue(ch *relayChannel, q chan queuedFrame, f api.RelayFrame) {
	select {
	case <-ch.stop:
	case q <- queuedFrame{f.Enc, f.Raw}:
	default:
		s.dropChannel(ch.chanID, "relay queue overflow: peer is not draining")
	}
}

// forwardFrame routes a relayed frame to the other end of its channel. A frame
// from a peer that is neither end of the channel is dropped.
func (s *Server) forwardFrame(src *api.Peer, f api.RelayFrame) {
	s.relayMu.Lock()
	ch := s.channels[f.Route.ChanID]
	s.relayMu.Unlock()
	switch {
	case ch == nil:
	case src == ch.client:
		s.enqueue(ch, ch.toNode, f)
	case src == ch.node:
		s.enqueue(ch, ch.toClient, f)
	}
}

func (s *Server) addNodePeer(id string, peer *api.Peer) {
	s.relayMu.Lock()
	s.nodePeers[id] = peer
	s.relayMu.Unlock()
}

// removeNodePeer drops a node uplink and tears down every channel bound to it,
// on either end.
func (s *Server) removeNodePeer(id string, peer *api.Peer) {
	s.relayMu.Lock()
	if s.nodePeers[id] == peer {
		delete(s.nodePeers, id)
	}
	s.relayMu.Unlock()
	s.dropChannelsWhere("node uplink closed", func(ch *relayChannel) bool { return ch.node == peer || ch.client == peer })
}

// notifyNodePeers sends a notification to every connected node uplink except
// except (nil sends to all). Node peers only — clients learn trust-log changes
// via node events, so sending both would double-trigger pulls. The originator
// is excluded because it already holds what it just pushed.
func (s *Server) notifyNodePeers(except *api.Peer, method string, params any) {
	s.relayMu.Lock()
	peers := make([]*api.Peer, 0, len(s.nodePeers))
	for _, p := range s.nodePeers {
		if p != except {
			peers = append(peers, p)
		}
	}
	s.relayMu.Unlock()
	for _, p := range peers {
		_ = p.Notify(method, params)
	}
}

// buildClientServer wires the client-facing JSON-RPC: ping, server.info, nodes.list,
// trustlog.sync, relay.open/close, node.event roster stream, and clients.*.
func (s *Server) buildClientServer() *api.Server {
	srv := api.NewServer()

	srv.Handle(api.MethodPing, func(context.Context, json.RawMessage) (any, error) { return nil, nil })

	srv.Handle(api.MethodServerInfo, func(context.Context, json.RawMessage) (any, error) {
		configured := s.pushPortStatus != nil && s.pushPortStatus()
		return api.ServerInfo{Version: s.version, StartedAt: api.StartedAtRFC3339(), Nodes: s.agg.Nodes(), PushPortConfigured: configured, Capabilities: &api.PeerCapabilities{CBOR: true}}, nil
	})

	srv.Handle(api.MethodNodesList, func(context.Context, json.RawMessage) (any, error) {
		return api.NodesListResult{Nodes: s.agg.Roster()}, nil
	})

	srv.Handle(api.MethodTrustLogSync, func(_ context.Context, params json.RawMessage) (any, error) {
		p, err := api.Decode[api.TrustLogSyncParams](params)
		if err != nil {
			return nil, err
		}
		entries, want, disjoint := s.entries.Delta(p.Known)
		return api.TrustLogSyncResult{Entries: entries, Want: want, Disjoint: disjoint && !p.Truncated}, nil
	})

	srv.Handle(api.MethodRelayOpen, func(ctx context.Context, params json.RawMessage) (any, error) {
		n, ok := api.NotifierFrom(ctx)
		clientPeer, isPeer := n.(*api.Peer)
		if !ok || !isPeer {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no client peer"}
		}
		if pr := api.PrincipalFrom(ctx); !pr.Admin && pr.TokenHash != "" && s.clientTokens != nil && !s.clientTokens.Has(pr.TokenHash) {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unauthorized: token revoked"}
		}
		p, err := api.Decode[api.RelayOpenParams](params)
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
		}
		s.relayMu.Lock()
		nodePeer := s.nodePeers[p.NodeID]
		open := 0
		for _, ch := range s.channels {
			if ch.client == clientPeer {
				open++
			}
		}
		s.relayMu.Unlock()
		if nodePeer == nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node: " + p.NodeID}
		}
		if open >= maxChannelsPerClient {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "too many open channels for this client"}
		}
		chanID := "c" + strconv.FormatUint(s.nextChan.Add(1), 10)
		s.addChannel(chanID, clientPeer, nodePeer, false)
		return api.RelayOpenResult{ChanID: chanID}, nil
	})

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
			s.dropChannel(p.ChanID, "closed by client")
		}
		return nil, nil
	})

	srv.OnConnect(func(n api.Notifier) func() {
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
			rosterCancel()
			if clientPeer, ok := n.(*api.Peer); ok {
				s.dropChannelsWhere("client disconnected", func(ch *relayChannel) bool { return ch.client == clientPeer })
			}
		}
	})

	s.registerPush(srv)
	s.registerPushPortAdmin(srv)
	s.registerClientAdmin(srv)
	return srv
}

// serveNode adopts an accepted node uplink: identify, register as a source, record
// as a relay target, and block until it disconnects.
func (s *Server) serveNode(conn net.Conn) {
	var self atomic.Pointer[api.Peer]
	var selfID atomic.Pointer[string]

	nodeDispatch := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case api.MethodNodesList:
			return api.NodesListResult{Nodes: s.agg.Roster()}, nil
		case api.MethodTrustLogSync:
			p, err := api.Decode[api.TrustLogSyncParams](params)
			if err != nil {
				return nil, err
			}
			entries, want, disjoint := s.entries.Delta(p.Known)
			return api.TrustLogSyncResult{Entries: entries, Want: want, Disjoint: disjoint && !p.Truncated}, nil
		case api.MethodTrustLogPush:
			p, err := api.Decode[api.TrustLogPushParams](params)
			if err != nil {
				return nil, err
			}
			added, _ := s.entries.PutAll(p.Entries)
			if added > 0 {
				s.notifyNodePeers(self.Load(), api.MethodTrustLogChanged, api.TrustLogChangedParams{Heads: s.entries.Heads()})
				s.agg.PublishTrustChanged()
			}
			return nil, nil
		case api.MethodRelayOpen:
			idp := selfID.Load()
			if idp == nil {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "node not identified"}
			}
			p, err := api.Decode[api.RelayOpenParams](params)
			if err != nil {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
			}
			return s.openNodeChannel(*idp, self.Load(), p.NodeID)
		case api.MethodRelayClose:
			p, err := api.Decode[api.RelayCloseParams](params)
			if err != nil {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
			}
			s.relayMu.Lock()
			ch := s.channels[p.ChanID]
			s.relayMu.Unlock()
			if ch != nil && ch.fromNode && ch.client == self.Load() {
				s.dropChannel(p.ChanID, "closed by opener node")
			}
			return nil, nil
		case api.MethodPushDeliver:
			if s.pushDeliverer == nil {
				return nil, &api.RPCError{Code: api.CodeInternalError, Message: "push delivery not configured"}
			}
			p, err := api.Decode[api.PushDeliverParams](params)
			if err != nil {
				return nil, err
			}
			body, err := base64.StdEncoding.DecodeString(p.Ciphertext)
			if err != nil {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push.deliver: bad ciphertext: " + err.Error()}
			}
			dctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			derr := s.pushDeliverer.Deliver(dctx, p.Endpoint, body, p.TTL, p.Urgency)
			if errors.Is(derr, push.ErrGone) {
				return api.PushDeliverResult{Gone: true}, nil
			}
			if derr != nil {
				return nil, &api.RPCError{Code: api.CodeInternalError, Message: derr.Error()}
			}
			return api.PushDeliverResult{}, nil
		default:
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
	}

	peer := api.NewPeer(conn, api.PeerOptions{
		KeepaliveInterval:         s.nodeKeepaliveInterval,
		KeepaliveTimeout:          s.nodeKeepaliveTimeout,
		KeepaliveFailureThreshold: s.nodeKeepaliveFailures,
		Dispatch:                  nodeDispatch,
		OnRelayFrame:              s.forwardFrame,
	})
	self.Store(peer)
	defer peer.Close()

	idCtx, idCancel := context.WithTimeout(context.Background(), nodeIdentifyTimeout)
	defer idCancel()
	var id api.IdentifyResult
	if err := peer.CallContext(idCtx, api.MethodNodeIdentify, api.IdentifyParams{Capabilities: &api.PeerCapabilities{CBOR: true}}, &id); err != nil || id.ID == "" {
		return
	}

	s.agg.AddSource(NewRemoteSource(id, peer))
	selfID.Store(&id.ID)
	s.addNodePeer(id.ID, peer)
	defer s.removeNodePeer(id.ID, peer)

	// Nodes get the same roster stream as clients so they can address peers.
	rosterEvents, rosterCancel := s.agg.SubscribeRoster()
	defer rosterCancel()
	for _, nd := range s.agg.Roster() {
		_ = peer.Notify(api.MethodNodeEvent, api.NodeEvent{Type: api.NodeEventAdded, Node: nd})
	}
	for {
		select {
		case <-peer.Done():
			return
		case ev, ok := <-rosterEvents:
			if !ok {
				<-peer.Done()
				return
			}
			if err := peer.Notify(api.MethodNodeEvent, ev); err != nil {
				<-peer.Done()
				return
			}
		}
	}
}

// openNodeChannel opens a relay channel from the opener node uplink to the
// node targetID. relay.peer_opened is written to the target before the channel
// exists: Notify writes synchronously under the peer's write lock and the relay
// pumps start in addChannel, so the target reads it before any relayed frame.
func (s *Server) openNodeChannel(openerID string, opener *api.Peer, targetID string) (any, error) {
	if targetID == openerID {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "cannot open a channel to self"}
	}
	s.relayMu.Lock()
	target := s.nodePeers[targetID]
	open := s.nodeOpening[opener]
	for _, ch := range s.channels {
		if ch.fromNode && ch.client == opener {
			open++
		}
	}
	if target == nil {
		s.relayMu.Unlock()
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown node: " + targetID}
	}
	if open >= maxChannelsPerNode {
		s.relayMu.Unlock()
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "too many open channels for this node"}
	}
	if s.nodeOpening == nil {
		s.nodeOpening = map[*api.Peer]int{}
	}
	s.nodeOpening[opener]++ // reserved until the channel is added or the open fails
	s.relayMu.Unlock()
	defer func() {
		s.relayMu.Lock()
		if s.nodeOpening[opener]--; s.nodeOpening[opener] <= 0 {
			delete(s.nodeOpening, opener)
		}
		s.relayMu.Unlock()
	}()
	chanID := "c" + strconv.FormatUint(s.nextChan.Add(1), 10)
	if err := target.Notify(api.MethodRelayPeerOpened, api.RelayPeerOpenedParams{ChanID: chanID, FromNode: openerID}); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "notify target node: " + err.Error()}
	}
	s.addChannel(chanID, opener, target, true)
	return api.RelayOpenResult{ChanID: chanID}, nil
}
