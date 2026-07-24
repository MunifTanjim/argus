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
)

// Server exposes an Aggregator over /node (node uplinks) and /client (consumers).
// Auth predicates gate each; nil = allow all (local/dev only).
type Server struct {
	agg        *Aggregator
	nodeAuth   func(token string) bool
	clientAuth func(token string) bool
	clientSrv  *api.Server

	clientTokens  *clienttoken.Store
	pushDeliverer push.Deliverer         // gateway blind-relay egress for push.deliver; nil disables
	vapidPubKey   string                 // served via push.vapidKey
	master        string                 // a /client conn presenting it is admin
	version       string                 // served via server.info
	publicURL     atomic.Pointer[string] // base URL for pairing QRs

	pairMu      sync.Mutex
	pairWaiters map[string]<-chan struct{} // minted token -> "device connected" signal

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

// SetVAPIDPublicKey publishes the VAPID public key devices fetch via push.vapidKey.
func (s *Server) SetVAPIDPublicKey(key string) { s.vapidPubKey = key }

// SetPushDeliverer wires the gateway's blind-relay egress for the push.deliver RPC.
// Call before serving.
func (s *Server) SetPushDeliverer(d push.Deliverer) { s.pushDeliverer = d }

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

// mustMarshal marshals v to JSON, ignoring errors (for well-known types only).
func mustMarshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// relayQueueDepth bounds a channel's per-direction pump queue. On overflow the
// channel is torn down (a dropped Noise record would desync the AEAD counter),
// isolated from other channels.
const relayQueueDepth = 64

// maxChannelsPerClient caps how many relay channels one client connection may hold
// open at once. Each channel costs two goroutines plus two relayQueueDepth buffers,
// so an uncapped client could exhaust node/gateway resources.
const maxChannelsPerClient = 64

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

// forwardFromClient relays a client's frame to the channel's node — but only if
// src actually owns the channel. Without this check any authenticated client could
// inject into (or, by overflowing the queue, tear down) another client's channel
// by guessing its sequential chan_id.
func (s *Server) forwardFromClient(src *api.Peer, f api.RelayFrame) {
	s.relayMu.Lock()
	ch := s.channels[f.Route.ChanID]
	s.relayMu.Unlock()
	if ch != nil && ch.client == src {
		s.enqueue(ch, ch.toNode, f.Raw)
	}
}

// forwardFromNode relays a node's frame to the channel's client — but only if src
// owns the channel, so a node cannot inject into another node's client channel.
func (s *Server) forwardFromNode(src *api.Peer, f api.RelayFrame) {
	s.relayMu.Lock()
	ch := s.channels[f.Route.ChanID]
	s.relayMu.Unlock()
	if ch != nil && ch.node == src {
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

// buildClientServer wires the client-facing JSON-RPC: blind methods (ping, server.info,
// nodes.list, trustlog.pull, relay.open/close, clients.*, push.vapidKey) and the
// blind roster (node.event) stream.
func (s *Server) buildClientServer() *api.Server {
	srv := api.NewServer()

	// ping is a no-op latency probe to the gateway itself (not routed to a node).
	srv.Handle(api.MethodPing, func(context.Context, json.RawMessage) (any, error) { return nil, nil })

	// server.info returns version + connected nodes (for settings view and spawn picker).
	srv.Handle(api.MethodServerInfo, func(context.Context, json.RawMessage) (any, error) {
		return api.ServerInfo{Version: s.version, Nodes: s.agg.Nodes()}, nil
	})

	// nodes.list is the E2E discovery view: each node's id/label/caps + Noise pubkey
	// + online flag. Additive alongside server.info.
	srv.Handle(api.MethodNodesList, func(context.Context, json.RawMessage) (any, error) {
		return api.NodesListResult{Nodes: s.agg.Roster()}, nil
	})

	// trustlog.pull serves all retained competing trust-log branches to a client.
	// The client ingests each branch and its genesis-pinned fork-choice picks the
	// winner; the gateway never verifies or interprets the chain internals.
	srv.Handle(api.MethodTrustLogPull, func(context.Context, json.RawMessage) (any, error) {
		return api.TrustLogPullResult{Chains: s.trust.all()}, nil
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

	// Stream the node roster to each client: snapshot first, then live node.event.
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
				s.dropChannelsWhere(func(ch *relayChannel) bool { return ch.client == clientPeer })
			}
		}
	})

	s.registerClientAdmin(srv)
	s.registerPush(srv)
	return srv
}

// registerPush wires the device push methods. Open to any authenticated /client
// (not admin-only): push.vapidKey serves the VAPID key for Web Push subscription.
func (s *Server) registerPush(srv *api.Server) {
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
		return api.TrustLogPullResult{Chains: s.trust.all()}, nil
	case api.MethodPushDeliver:
		if s.pushDeliverer == nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push delivery not enabled on this gateway"}
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

// serveNode adopts an accepted node uplink: learn its identity, register it as a
// source, and block until it disconnects.
func (s *Server) serveNode(conn net.Conn) {
	// nodeID is set (under mu) once identify succeeds; beacon.offer handling reads it.
	var mu sync.Mutex
	var nodeID string

	nodeDispatch := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == api.MethodBeaconOffer {
			mu.Lock()
			id := nodeID
			mu.Unlock()
			if id == "" {
				return nil, nil // not yet identified; drop
			}
			b, err := api.Decode[api.Beacon](params)
			if err != nil {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid beacon: " + err.Error()}
			}
			s.agg.UpdateBeacon(id, &b)
			return nil, nil
		}
		return s.nodeDispatch(ctx, method, params)
	}

	peer := api.NewPeer(conn, api.PeerOptions{
		KeepaliveInterval:         nodeKeepaliveInterval,
		KeepaliveTimeout:          nodeKeepaliveTimeout,
		KeepaliveFailureThreshold: nodeKeepaliveFailures,
		Dispatch:                  nodeDispatch,
		OnRelayFrame:              s.forwardFromNode,
	})
	defer peer.Close()

	var id api.IdentifyResult
	if err := peer.Call(api.MethodNodeIdentify, nil, &id); err != nil || id.ID == "" {
		return
	}
	mu.Lock()
	nodeID = id.ID
	mu.Unlock()

	s.agg.AddSource(NewRemoteSource(id.ID, id.Label, id.Version, id.IdentityPubKey, id.SignerPubKey, id.BeaconPubKey, id.Capabilities, peer, id.Beacon))
	s.addNodePeer(id.ID, peer)
	defer s.removeNodePeer(id.ID, peer)
	<-peer.Done()
}
