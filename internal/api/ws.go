package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// wsMessageType carries newline-delimited JSON-RPC frames as WebSocket text
// messages (human-debuggable; the framing is identical to the unix transport).
const wsMessageType = websocket.MessageText

// wsReadLimit matches the stream scanner's max frame size (16 MiB) so large
// capture/transcript payloads are not rejected by the default 32 KiB limit.
const wsReadLimit = 16 * 1024 * 1024

// DialWS connects to a gateway over WebSocket and returns a consumer Client.
// Non-empty token is sent as a Bearer header. httpClient may be nil (use a custom
// one for a TLS config or pinned cert).
func DialWS(ctx context.Context, url, token string, httpClient *http.Client) (*Client, error) {
	conn, err := dialWS(ctx, url, token, httpClient)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// DialWSPeer connects over WebSocket and returns a symmetric Peer. The node
// uplink uses this so it can both push events up and serve control requests
// coming back down the same connection.
func DialWSPeer(ctx context.Context, url, token string, httpClient *http.Client, opts PeerOptions) (*Peer, error) {
	conn, err := dialWS(ctx, url, token, httpClient)
	if err != nil {
		return nil, err
	}
	return NewPeer(conn, opts), nil
}

// DialWSConn connects over WebSocket and returns the raw net.Conn, for callers
// (e.g. ReconnectingClient) that manage their own Peer over it.
func DialWSConn(ctx context.Context, url, token string, httpClient *http.Client) (net.Conn, error) {
	return dialWS(ctx, url, token, httpClient)
}

// DialAuthError reports that the gateway answered the WebSocket handshake with an
// auth status instead of upgrading. It is distinct from a transport failure: the
// gateway was reached, and it refused this caller's token.
type DialAuthError struct {
	URL        string
	StatusCode int
	TokenSent  bool
	Err        error
}

func (e *DialAuthError) Error() string {
	cause := "no bearer token was sent"
	if e.TokenSent {
		cause = "the bearer token was rejected"
	}
	return fmt.Sprintf("%s refused the connection: HTTP %d %s (%s)",
		e.URL, e.StatusCode, http.StatusText(e.StatusCode), cause)
}

func (e *DialAuthError) Unwrap() error { return e.Err }

func dialWS(ctx context.Context, url, token string, httpClient *http.Client) (net.Conn, error) {
	opts := &websocket.DialOptions{HTTPClient: httpClient}
	if token != "" {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + token}}
	}
	c, resp, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, &DialAuthError{URL: url, StatusCode: resp.StatusCode, TokenSent: token != "", Err: err}
		}
		return nil, err
	}
	c.SetReadLimit(wsReadLimit)
	// context.Background(): the net.Conn lives until it is explicitly closed.
	return websocket.NetConn(context.Background(), c, wsMessageType), nil
}

// AcceptWS upgrades an HTTP request to a WebSocket and adapts it to a net.Conn.
// Origin checking is disabled because access is gated by bearer token, not by
// browser origin.
func AcceptWS(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(wsReadLimit)
	return websocket.NetConn(context.Background(), c, wsMessageType), nil
}

// BearerToken extracts the auth token from an HTTP request: the Authorization
// "Bearer <token>" header, falling back to a "token" query parameter (browsers
// cannot set custom headers on a WebSocket handshake).
func BearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return r.URL.Query().Get("token")
}

// WSHandler returns an http.Handler that authenticates the bearer token, upgrades
// to a WebSocket, and serves it through this Server's registry. nil authorize
// allows all connections (local/dev only). TLS is applied by the enclosing http.Server.
func (s *Server) WSHandler(authorize func(token string) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorize != nil && !authorize(BearerToken(r)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := AcceptWS(w, r)
		if err != nil {
			return
		}
		s.ServeConn(conn) // blocks until the connection closes
	})
}
