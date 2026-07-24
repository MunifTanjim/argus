package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"time"
)

// HandlerFunc handles a single request. params is the raw JSON params (may be
// nil); the returned value is marshaled as the result.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Notifier sends a server-initiated notification to one connected client.
type Notifier interface {
	Notify(method string, params any) error
}

// Server dispatches JSON-RPC requests and streams notifications to clients. Each
// accepted connection becomes a Peer routed through the shared handler registry.
type Server struct {
	mu         sync.RWMutex
	handlers   map[string]HandlerFunc
	onConnect  func(n Notifier) (cleanup func())
	relayFrame func(*Peer, RelayFrame) // installed via SetRelayFrameHandler; nil drops relay frames
	log        *slog.Logger            // optional per-request logging; nil disables it
}

// NewServer returns an empty Server.
func NewServer() *Server {
	return &Server{handlers: make(map[string]HandlerFunc)}
}

// SetLogger enables per-request logging through l (nil disables it). Each request
// is logged once on completion (method, duration, error).
func (s *Server) SetLogger(l *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = l
}

// Handle registers a handler for a method.
func (s *Server) Handle(method string, h HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// OnConnect registers a hook invoked once per connection with a Notifier for
// pushing notifications; the returned cleanup runs when the connection closes.
func (s *Server) OnConnect(fn func(n Notifier) (cleanup func())) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onConnect = fn
}

// SetRelayFrameHandler installs a handler invoked for every relay frame (one
// carrying a Route header) on any connection this server serves. The handler
// receives the source *Peer so it can enforce channel ownership (a relay frame
// must come from the peer that owns the target chan_id). Nil (the default) drops
// relay frames — preserving pre-relay behavior.
func (s *Server) SetRelayFrameHandler(fn func(*Peer, RelayFrame)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.relayFrame = fn
}

// Serve accepts connections on l until the listener is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(conn)
	}
}

// ServeConn serves a single established connection until it closes. Exported so
// non-listener transports (e.g. a WebSocket adapted to net.Conn) reuse the dispatch path.
func (s *Server) ServeConn(netConn net.Conn) {
	s.ServeConnContext(context.Background(), netConn)
}

// ServeConnContext is ServeConn with a connection-scoped base context, whose
// values (e.g. an auth Principal) reach every request served on the connection.
func (s *Server) ServeConnContext(ctx context.Context, netConn net.Conn) {
	s.mu.RLock()
	relayFrame := s.relayFrame
	s.mu.RUnlock()
	// The handler receives the source Peer (supplied by the Peer itself) so it can
	// enforce channel ownership. Nil relayFrame keeps the Peer dropping relay frames.
	peer := NewPeer(netConn, PeerOptions{Dispatch: s.dispatch, BaseContext: ctx, OnRelayFrame: relayFrame})
	defer peer.Close()

	s.mu.RLock()
	onConnect := s.onConnect
	s.mu.RUnlock()
	if onConnect != nil {
		if cleanup := onConnect(peer); cleanup != nil {
			defer cleanup()
		}
	}
	<-peer.Done()
}

// DispatchFunc returns a DispatchFunc bound to this server's handler registry, so
// the same handlers serve over a non-listener transport (e.g. the node's gateway uplink).
func (s *Server) DispatchFunc() DispatchFunc { return s.dispatch }

// dispatch routes an inbound request to the registered handler, or returns a
// method-not-found RPCError.
func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	s.mu.RLock()
	h := s.handlers[method]
	log := s.log
	s.mu.RUnlock()

	start := time.Now()
	if h == nil {
		err := &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + method}
		logRequest(log, method, start, err)
		return nil, err
	}
	ctx, la := withLogAttrs(ctx)
	res, err := h(ctx, params)
	logRequest(log, method, start, err, la.kv...)
	return res, err
}

// logRequest emits one per-request line (method, duration, handler-supplied attrs,
// and error if any). No-op when log is nil.
func logRequest(log *slog.Logger, method string, start time.Time, err error, extra ...any) {
	if log == nil {
		return
	}
	args := append([]any{"method", method, "dur", time.Since(start)}, extra...)
	if err != nil {
		args = append(args, "err", err)
	}
	log.Info("rpc", args...)
}
