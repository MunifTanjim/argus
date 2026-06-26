package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// clientRoutedMethods are the session-addressed control calls the gateway forwards to
// the owning node (each carries a composite session_id). sessions.spawn is routed
// separately (by node_id) since it has no session to address yet.
var clientRoutedMethods = []string{
	api.MethodSessionTranscriptView,
	api.MethodSessionToolDetail,
	api.MethodSessionCapture,
	api.MethodSessionInput,
	api.MethodSessionKey,
	api.MethodSessionRespond,
	api.MethodSessionKill,
}

// subEntry records a client's transcript subscription for routing delta notifications.
type subEntry struct {
	client api.Notifier
	nodeID string
}

// Server exposes an Aggregator over two WebSocket endpoints: /node for node
// uplinks and /client for consumer clients. Auth predicates gate each (nil =
// allow all, local/dev only).
type Server struct {
	agg        *Aggregator
	nodeAuth   func(token string) bool
	clientAuth func(token string) bool
	clientSrv  *api.Server

	clientTokens *clienttoken.Store
	pushStore    *push.Store            // registered device push targets (nil = push disabled)
	pushSender   *push.Dispatcher       // routes a push to its backend (for push.test)
	vapidPubKey  string                 // VAPID public key served to devices (push.vapidKey)
	master       string                 // master token; a /client conn presenting it is admin
	publicURL    atomic.Pointer[string] // gateway's reachable base URL for pairing QRs

	pairMu      sync.Mutex
	pairWaiters map[string]<-chan struct{} // minted token -> "device connected" signal

	subMu sync.Mutex
	subs  map[string]subEntry // sub_id -> subscriber
}

// NewServer builds a gateway Server over agg.
func NewServer(agg *Aggregator, nodeAuth, clientAuth func(token string) bool) *Server {
	s := &Server{
		agg: agg, nodeAuth: nodeAuth, clientAuth: clientAuth,
		pairWaiters: map[string]<-chan struct{}{},
		subs:        map[string]subEntry{},
	}
	s.clientSrv = s.buildClientServer()
	return s
}

// SetClientTokens enables per-client token management: store backs the active
// token set (and pending pairings), and master is the admin token that gates the
// clients.* methods. Call before serving.
func (s *Server) SetClientTokens(store *clienttoken.Store, master string) {
	s.clientTokens = store
	s.master = master
}

// SetPush enables the push.register/unregister/test methods: store records paired
// devices' push targets, and dispatcher routes a push to its backend (used by
// push.test). Call before serving.
func (s *Server) SetPush(store *push.Store, dispatcher *push.Dispatcher) {
	s.pushStore = store
	s.pushSender = dispatcher
}

// SetVAPIDPublicKey publishes the VAPID public key the app fetches (push.vapidKey)
// to register a Web Push subscription bound to it (embedded FCM distributor).
func (s *Server) SetVAPIDPublicKey(key string) { s.vapidPubKey = key }

// SetPublicURL records the gateway's reachable base URL (scheme://host, no path),
// returned by clients.pairStart so the pairing QR points back here. Safe to call
// repeatedly (e.g. once the tunnel URL is known).
func (s *Server) SetPublicURL(u string) { s.publicURL.Store(&u) }

func (s *Server) getPublicURL() string {
	if p := s.publicURL.Load(); p != nil {
		return *p
	}
	return ""
}

func (s *Server) addSub(subID, nodeID string, client api.Notifier) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	s.subs[subID] = subEntry{client: client, nodeID: nodeID}
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

// mustMarshal marshals v to JSON, ignoring errors (for well-known types only).
func mustMarshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// routeByNodeID builds a handler that requires node_id in params and routes the
// call to the owning node. Used by per-machine history methods.
func (s *Server) routeByNodeID(method string) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		nodeID, err := nodeIDFromParams(params)
		if err != nil || nodeID == "" {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: method + " requires node_id"}
		}
		return s.agg.RouteToNode(ctx, nodeID, method, params)
	}
}

// SetLogger enables per-request logging on the client-facing RPC server (nil disables).
func (s *Server) SetLogger(l *slog.Logger) { s.clientSrv.SetLogger(l) }

// Handler returns the gateway's HTTP handler with the /node and /client routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/node", s.nodeHandler())
	mux.Handle("/client", s.clientHandler())
	return mux
}

// clientHandler authenticates a /client connection, tags it with an auth
// Principal (admin when the master token is presented), and serves it. It mirrors
// api.Server.WSHandler but threads the Principal so clients.* methods can require
// admin, and so a minted client token can be promoted on its first connection.
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
		s.clientSrv.ServeConnContext(ctx, conn) // blocks until the connection closes
	})
}

// buildClientServer wires the client-facing JSON-RPC: reads served from the
// merged view, control calls routed to the owning node, and a per-connection
// stream of the merged event feed.
func (s *Server) buildClientServer() *api.Server {
	srv := api.NewServer()

	// ping is a no-op latency probe to the gateway itself (not routed to a node).
	srv.Handle(api.MethodPing, func(context.Context, json.RawMessage) (any, error) { return nil, nil })

	srv.Handle(api.MethodSessionsList, func(_ context.Context, _ json.RawMessage) (any, error) {
		return s.agg.Snapshot(), nil
	})
	// Refresh fans the rescan out to every node, then returns the merged view.
	// Per-node errors are ignored in-band (Fanout) so one unreachable node can't
	// fail the whole refresh; each node's rescan also pushes its fresh sessions to
	// subscribed clients over the event stream.
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

	// nodes.list lets a client enumerate connected nodes (for a spawn target)
	// without first having a session on each.
	srv.Handle(api.MethodNodesList, func(context.Context, json.RawMessage) (any, error) {
		return s.agg.Nodes(), nil
	})

	// sessions.spawn has no session_id to route on; route by an explicit node_id.
	// When the client omits it and exactly one node is connected, spawn there —
	// so a fresh single-node setup can create its first session without a picker.
	srv.Handle(api.MethodSessionSpawn, func(ctx context.Context, params json.RawMessage) (any, error) {
		nodeID, err := nodeIDFromParams(params)
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "sessions.spawn requires node_id"}
		}
		if nodeID == "" {
			if nodeID = s.agg.SoleNode(); nodeID == "" {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "sessions.spawn requires node_id"}
			}
		}
		return s.agg.RouteToNode(ctx, nodeID, api.MethodSessionSpawn, params)
	})

	// History projects aggregate across all machines: fan out, stamp each project
	// with its origin node, and order newest-first (RFC3339 UTC sorts lexically).
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
	// History transcript and tool detail are per-machine: route to the owning
	// node by node_id.
	srv.Handle(api.MethodSessionsHistoryTranscript, s.routeByNodeID(api.MethodSessionsHistoryTranscript))
	srv.Handle(api.MethodSessionHistoryToolDetail, s.routeByNodeID(api.MethodSessionHistoryToolDetail))
	// History sessions route to the owning node, then stamp each session with that
	// node's id/label (as historyProjects does for projects) so a client can open a
	// transcript by the session's own node_id without tracking the project's node.
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
			return res, nil // unexpected shape; pass through unchanged
		}
		label := s.agg.NodeLabel(nodeID)
		for i := range page.Items {
			page.Items[i].NodeID = nodeID
			page.Items[i].NodeLabel = label
		}
		return page, nil
	})

	// transcript.subscribe: record in the sub table, then route to the owning node.
	// Registered explicitly (not via clientRoutedMethods) because it needs bookkeeping.
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
		// Record before routing so an early delta finds its client.
		s.addSub(p.SubID, nodeID, client)
		res, err := s.agg.Route(ctx, api.MethodTranscriptSubscribe, params)
		if err != nil {
			s.dropSub(p.SubID)
			return nil, err
		}
		return res, nil
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

	// Stream the merged registry to each connected client: snapshot first, then
	// live events (mirrors the node's OnConnect).
	srv.OnConnect(func(n api.Notifier) func() {
		events, cancel := s.agg.Subscribe()
		for _, sess := range s.agg.Snapshot() {
			_ = n.Notify(api.MethodSessionEvent, registry.Event{Type: registry.EventAdded, Session: sess})
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
				}
			}
		}()
		return func() {
			close(done)
			cancel()
			for _, sub := range s.subsForClient(n) {
				s.dropSub(sub.subID)
				_, _ = s.agg.RouteToNode(context.Background(), sub.nodeID,
					api.MethodTranscriptUnsubscribe, mustMarshal(api.TranscriptUnsubscribeParams{SubID: sub.subID}))
			}
		}
	})

	s.registerClientAdmin(srv)
	s.registerPush(srv)
	return srv
}

// registerPush wires the device push methods. They are open to any authenticated
// /client connection (not admin-only): the paired device records the target its
// push distributor handed it, keyed by a stable device id it supplies.
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

	// test sends a notification to the device's registered target through the real
	// backend so it can confirm end-to-end delivery. Surfaces failures to the caller.
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
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
		}
		return nil, nil
	})

	// vapidKey serves the gateway's VAPID public key so a device can register a
	// Web Push subscription bound to it (e.g. the embedded FCM distributor).
	srv.Handle(api.MethodPushVAPIDKey, func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.PushVAPIDKey{Key: s.vapidPubKey}, nil
	})
}

// requireAdmin wraps h so it only runs for connections that presented the master
// token (see clientHandler); other callers get an unauthorized error.
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

	// pairStart mints a temporary token, holds it pending, and returns it with the
	// gateway's public base URL so the caller can render a pairing QR.
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

	// pairAwait blocks until the minted token's device connects (its waiter channel
	// is closed on promotion) or the pairing window elapses.
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

// Node uplink keepalive: the gateway pings each node on this cadence so a
// half-open link (node host vanished without a TCP FIN) is detected promptly
// instead of lingering until an OS keepalive or a failed write. Closing the peer
// after nodeKeepaliveFailures consecutive unanswered pings fires Done and flows
// into the aggregator's normal offline → grace → removal path; requiring two
// failures rides out a transient blip so a briefly busy node isn't dropped.
const (
	nodeKeepaliveInterval = 15 * time.Second
	nodeKeepaliveTimeout  = 5 * time.Second
	nodeKeepaliveFailures = 2
)

// serveNode adopts an accepted node uplink: decode its event notifications,
// learn its identity, register it as a source, and block until it disconnects.
func (s *Server) serveNode(conn net.Conn) {
	events := make(chan registry.Event, 64)
	// peerRef lets the OnNotify closure reference peer before NewPeer returns.
	// Stored atomically to satisfy the race detector (OnNotify runs in a goroutine).
	var peerRef atomic.Pointer[api.Peer]
	peer := api.NewPeer(conn, api.PeerOptions{
		KeepaliveInterval:         nodeKeepaliveInterval,
		KeepaliveTimeout:          nodeKeepaliveTimeout,
		KeepaliveFailureThreshold: nodeKeepaliveFailures,
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
					// Orphaned poller: the node is pushing deltas for a sub the gateway
					// no longer tracks. Tell the node to stop. The node registers
					// transcript.unsubscribe as a request handler (srv.Handle), so we
					// must use Call, not Notify.
					p := peerRef.Load()
					if p == nil {
						return // setup race: peer not stored yet; node's poller dies with the link
					}
					go func() {
						_ = p.Call(api.MethodTranscriptUnsubscribe,
							api.TranscriptUnsubscribeParams{SubID: d.SubID}, nil)
					}()
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
	s.agg.AddSource(NewRemoteSource(id.ID, id.Label, peer, events))
	<-peer.Done()
}
