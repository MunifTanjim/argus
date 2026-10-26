package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// wsReadLimit admits one full-size frame per message: a CBOR frame's 5-byte
// header plus MaxFrameSize, which also covers a MaxFrameSize JSON line and its
// newline. The default 32 KiB limit would reject large capture/transcript payloads.
const wsReadLimit = MaxFrameSize + 5

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
	// context.Background(): the net.Conn lives until it is explicitly closed.
	return newWSConn(c), nil
}

// AcceptWS upgrades an HTTP request to a WebSocket and adapts it to a net.Conn.
// Origin checking is disabled because access is gated by bearer token, not by
// browser origin.
func AcceptWS(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil, err
	}
	return newWSConn(c), nil
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

// wsConn adapts a websocket.Conn to a net.Conn that accepts both message types.
// Reads concatenate every message into one byte stream; WriteFrame sends JSON
// frames as text messages and CBOR frames as binary messages, one frame each.
type wsConn struct {
	c *websocket.Conn

	rmu           sync.Mutex
	reader        io.Reader
	readCancel    context.CancelFunc
	readEOFed     bool
	wmu           sync.Mutex
	dmu           sync.Mutex // guards the deadlines
	readDeadline  time.Time
	writeDeadline time.Time
}

func newWSConn(c *websocket.Conn) net.Conn {
	c.SetReadLimit(wsReadLimit)
	return &wsConn{c: c}
}

var (
	_ net.Conn    = (*wsConn)(nil)
	_ FrameWriter = (*wsConn)(nil)
)

func (w *wsConn) Read(p []byte) (int, error) {
	w.rmu.Lock()
	defer w.rmu.Unlock()
	for {
		if w.readEOFed {
			return 0, io.EOF
		}
		if w.reader == nil {
			// The context bounds the whole message, so it lives until the reader ends.
			ctx, cancel := deadlineContext(w.deadline(&w.readDeadline))
			_, r, err := w.c.Reader(ctx)
			if err != nil {
				cancel()
				switch websocket.CloseStatus(err) {
				case websocket.StatusNormalClosure, websocket.StatusGoingAway:
					w.readEOFed = true
					return 0, io.EOF
				}
				return 0, err
			}
			w.reader, w.readCancel = r, cancel
		}
		n, err := w.reader.Read(p)
		if err != nil {
			w.readCancel()
			w.reader, w.readCancel = nil, nil
			if errors.Is(err, io.EOF) {
				err = nil
			}
		}
		if n > 0 || err != nil {
			return n, err
		}
	}
}

func (w *wsConn) Write(p []byte) (int, error) {
	if err := w.write(websocket.MessageText, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *wsConn) WriteFrame(enc Encoding, frame []byte) error {
	typ := websocket.MessageText
	if enc == EncodingCBOR {
		typ = websocket.MessageBinary
	}
	return w.write(typ, frame)
}

func (w *wsConn) write(typ websocket.MessageType, p []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	ctx, cancel := deadlineContext(w.deadline(&w.writeDeadline))
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	return w.c.Write(ctx, typ, p)
}

func (w *wsConn) Close() error { return w.c.Close(websocket.StatusNormalClosure, "") }

func (w *wsConn) SetDeadline(t time.Time) error {
	_ = w.SetReadDeadline(t)
	return w.SetWriteDeadline(t)
}

func (w *wsConn) SetReadDeadline(t time.Time) error {
	w.dmu.Lock()
	defer w.dmu.Unlock()
	w.readDeadline = t
	return nil
}

func (w *wsConn) SetWriteDeadline(t time.Time) error {
	w.dmu.Lock()
	defer w.dmu.Unlock()
	w.writeDeadline = t
	return nil
}

func (w *wsConn) deadline(d *time.Time) time.Time {
	w.dmu.Lock()
	defer w.dmu.Unlock()
	return *d
}

func (w *wsConn) LocalAddr() net.Addr  { return wsAddr{} }
func (w *wsConn) RemoteAddr() net.Addr { return wsAddr{} }

type wsAddr struct{}

func (wsAddr) Network() string { return "websocket" }
func (wsAddr) String() string  { return "websocket/unknown-addr" }

func deadlineContext(t time.Time) (context.Context, context.CancelFunc) {
	if t.IsZero() {
		return context.WithCancel(context.Background())
	}
	return context.WithDeadline(context.Background(), t)
}
