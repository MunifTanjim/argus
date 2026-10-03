package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// rpcReadLimit matches the daemon's advertised
// x-codex-websocket-max-unfragmented-message-bytes. coder/websocket defaults to
// 32 KiB, which a thread/resume response easily exceeds.
const rpcReadLimit = 16 << 20

var errConnClosed = errors.New("codex: daemon connection closed")

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("codex rpc %d: %s", e.Code, e.Message) }

// wireMsg is any JSON-RPC message. The daemon omits the "jsonrpc" field.
type wireMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// inbound is a server-to-client message: a notification (ID nil) or a request.
type inbound struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// rpcConn is a JSON-RPC client for the Codex app-server daemon, which speaks
// WebSocket over a Unix socket with one JSON message per text frame.
type rpcConn struct {
	ws     *websocket.Conn
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan wireMsg

	// Inbound messages queue without bound so the read loop never blocks on the
	// consumer; a consumer that issues calls would otherwise deadlock it.
	qmu    sync.Mutex
	queue  []inbound
	qready chan struct{}

	closed chan struct{}
	once   sync.Once
}

func dialRPC(ctx context.Context, sockPath string) (*rpcConn, error) {
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
	}}
	ws, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(rpcReadLimit)
	c := &rpcConn{
		ws:      ws,
		pending: map[int64]chan wireMsg{},
		qready:  make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *rpcConn) readLoop() {
	defer c.shutdown()
	for {
		_, b, err := c.ws.Read(context.Background())
		if err != nil {
			return
		}
		var m wireMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.Method != "" {
			c.qmu.Lock()
			c.queue = append(c.queue, inbound{ID: m.ID, Method: m.Method, Params: m.Params})
			c.qmu.Unlock()
			select {
			case c.qready <- struct{}{}:
			default:
			}
			continue
		}
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		c.mu.Unlock()
		if ch != nil {
			// A duplicate response must not block the read loop.
			select {
			case ch <- m:
			default:
			}
		}
	}
}

func (c *rpcConn) shutdown() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.ws.CloseNow()
	})
}

func (c *rpcConn) close() {
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
	c.shutdown()
}

// next returns the next inbound message, or false once the connection closed or
// ctx is done.
func (c *rpcConn) next(ctx context.Context) (inbound, bool) {
	for {
		c.qmu.Lock()
		if len(c.queue) > 0 {
			m := c.queue[0]
			c.queue = c.queue[1:]
			c.qmu.Unlock()
			return m, true
		}
		c.qmu.Unlock()
		select {
		case <-c.qready:
		case <-c.closed:
			return inbound{}, false
		case <-ctx.Done():
			return inbound{}, false
		}
	}
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}

func (c *rpcConn) write(ctx context.Context, m wireMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *rpcConn) call(ctx context.Context, method string, params, result any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	id := c.nextID.Add(1)
	ch := make(chan wireMsg, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.write(ctx, wireMsg{ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-c.closed:
		return errConnClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *rpcConn) notify(ctx context.Context, method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.write(ctx, wireMsg{Method: method, Params: raw})
}

func (c *rpcConn) reply(ctx context.Context, id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.write(ctx, wireMsg{ID: id, Result: raw})
}

func (c *rpcConn) initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo":   map[string]string{"name": "argus", "title": "argus", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		return err
	}
	return c.notify(ctx, "initialized", nil)
}
