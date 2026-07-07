package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// DispatchFunc handles an incoming request. params is the raw JSON params (may be
// nil); the returned value is marshaled as the result. ctx is cancelled when the
// peer's connection closes.
type DispatchFunc func(ctx context.Context, method string, params json.RawMessage) (any, error)

// PeerOptions configures a Peer's inbound behavior.
type PeerOptions struct {
	// Dispatch handles requests the remote end issues. Nil means this peer serves
	// no methods (every inbound request gets method-not-found).
	Dispatch DispatchFunc
	// OnNotify receives notifications the remote end sends. Nil drops them.
	OnNotify func(Notification)
	// BaseContext is the parent of each served request's context (so values like
	// an auth Principal flow to handlers). Defaults to context.Background().
	BaseContext context.Context
	// KeepaliveInterval > 0 pings the remote every interval; used to detect a
	// half-open link that never errors on read. Zero disables keepalive.
	KeepaliveInterval time.Duration
	// KeepaliveTimeout bounds each ping's wait for a reply. Defaults to KeepaliveInterval.
	KeepaliveTimeout time.Duration
	// KeepaliveFailureThreshold is how many consecutive failed pings close the
	// peer; an answered ping resets the count. Defaults to 1.
	KeepaliveFailureThreshold int
	// WriteTimeout bounds how long a single frame write may block, so one stuck
	// consumer can't wedge a shared writer (e.g. a slow terminal viewer blocking
	// the node uplink). Exceeding it errors and closes the peer. Zero uses
	// defaultWriteTimeout; a negative value disables the deadline.
	WriteTimeout time.Duration
}

// defaultWriteTimeout bounds a blocked frame write when WriteTimeout is unset:
// generous enough not to hit a slow-but-live consumer, short enough to drop a
// truly stuck one before it starves the keepalive path.
const defaultWriteTimeout = 10 * time.Second

// writeDeadliner is the optional deadline support on the underlying transport
// (net.Conn satisfies it; an in-memory pipe used in tests does too).
type writeDeadliner interface{ SetWriteDeadline(time.Time) error }

// Peer is one end of a symmetric JSON-RPC 2.0 connection: it both issues and
// serves requests/notifications over a single stream. The gateway↔node uplink uses
// a Peer directly so both sides can call each other.
type Peer struct {
	rwc io.ReadWriteCloser

	wmu sync.Mutex // serializes writes so frames never interleave
	bw  *bufio.Writer

	mu      sync.Mutex
	nextID  int
	pending map[int]chan message

	dispatch     DispatchFunc
	onNotify     func(Notification)
	writeTimeout time.Duration

	ctx     context.Context
	cancel  context.CancelFunc
	closeMu sync.Once
	closed  chan struct{}
}

// NewPeer wraps an established connection and starts its read loop.
func NewPeer(rwc io.ReadWriteCloser, opts PeerOptions) *Peer {
	base := opts.BaseContext
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	writeTimeout := opts.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = defaultWriteTimeout
	}
	p := &Peer{
		rwc:          rwc,
		bw:           bufio.NewWriter(rwc),
		pending:      make(map[int]chan message),
		dispatch:     opts.Dispatch,
		onNotify:     opts.OnNotify,
		writeTimeout: writeTimeout,
		ctx:          ctx,
		cancel:       cancel,
		closed:       make(chan struct{}),
	}
	go p.readLoop()
	if opts.KeepaliveInterval > 0 {
		go p.keepalive(opts.KeepaliveInterval, opts.KeepaliveTimeout, opts.KeepaliveFailureThreshold)
	}
	return p
}

// keepalive pings the remote every interval and closes the peer after threshold
// consecutive failed pings (an answered ping resets the streak). Catches a
// half-open connection whose read side never errors. Stops when the peer closes.
func (p *Peer) keepalive(interval, timeout time.Duration, threshold int) {
	if timeout <= 0 {
		timeout = interval
	}
	if threshold < 1 {
		threshold = 1
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	fails := 0
	for {
		select {
		case <-p.closed:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(p.ctx, timeout)
			err := p.CallContext(ctx, MethodPing, nil, nil)
			cancel()
			if err == nil {
				fails = 0
				continue
			}
			if fails++; fails >= threshold {
				_ = p.Close()
				return
			}
		}
	}
}

// Done is closed when the peer's read loop ends (connection closed or errored).
func (p *Peer) Done() <-chan struct{} { return p.closed }

// Close terminates the connection and unblocks in-flight calls.
func (p *Peer) Close() error {
	p.closeMu.Do(func() { p.cancel() })
	return p.rwc.Close()
}

// Call issues a request and unmarshals the result into out (which may be nil).
// It blocks until the reply arrives or the connection closes; use CallContext to
// bound the wait.
func (p *Peer) Call(method string, params, out any) error {
	return p.CallContext(context.Background(), method, params, out)
}

// CallContext is Call with a context: on cancel/deadline it abandons the request
// and returns ctx.Err(), reclaiming the pending slot so a late reply isn't leaked.
func (p *Peer) CallContext(ctx context.Context, method string, params, out any) error {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = b
	}

	p.mu.Lock()
	id := p.nextID
	p.nextID++
	idRaw := json.RawMessage(strconv.Itoa(id))
	resCh := make(chan message, 1)
	p.pending[id] = resCh
	p.mu.Unlock()

	forget := func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}

	if err := p.send(message{ID: &idRaw, Method: method, Params: rawParams}); err != nil {
		forget()
		return err
	}

	select {
	case resp := <-resCh:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-ctx.Done():
		forget()
		return ctx.Err()
	case <-p.closed:
		return fmt.Errorf("api: connection closed")
	}
}

// Notify sends a one-way notification (no response expected).
func (p *Peer) Notify(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	return p.send(message{Method: method, Params: raw})
}

func (p *Peer) send(m message) error {
	m.JSONRPC = jsonrpcVersion
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.writeTimeout > 0 {
		if wd, ok := p.rwc.(writeDeadliner); ok {
			_ = wd.SetWriteDeadline(time.Now().Add(p.writeTimeout))
		}
	}
	if err := p.writeFrame(b); err != nil {
		// A failed or timed-out write leaves the frame half-emitted, so the stream
		// is unusable: drop the peer rather than desync every later frame.
		_ = p.Close()
		return err
	}
	return nil
}

func (p *Peer) writeFrame(b []byte) error {
	if _, err := p.bw.Write(b); err != nil {
		return err
	}
	if err := p.bw.WriteByte('\n'); err != nil {
		return err
	}
	return p.bw.Flush()
}

func (p *Peer) readLoop() {
	defer close(p.closed)
	defer p.cancel()
	scanner := bufio.NewScanner(p.rwc)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			_ = p.send(message{Error: &RPCError{Code: CodeParseError, Message: "parse error"}})
			continue
		}
		switch {
		case m.isRequest():
			go p.serveRequest(m)
		case m.isNotification():
			if p.onNotify != nil {
				p.onNotify(Notification{Method: m.Method, Params: m.Params})
			}
		default: // response: route to the waiting caller by id
			var id int
			if m.ID != nil {
				_ = json.Unmarshal(*m.ID, &id)
			}
			p.mu.Lock()
			ch := p.pending[id]
			delete(p.pending, id)
			p.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

func (p *Peer) serveRequest(m message) {
	resp := message{ID: m.ID}
	if p.dispatch == nil {
		resp.Error = &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + m.Method}
		_ = p.send(resp)
		return
	}
	result, err := p.dispatch(WithNotifier(p.ctx, p), m.Method, m.Params)
	if err != nil {
		if rpcErr, ok := err.(*RPCError); ok {
			resp.Error = rpcErr
		} else {
			resp.Error = &RPCError{Code: CodeInternalError, Message: err.Error()}
		}
		_ = p.send(resp)
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		resp.Error = &RPCError{Code: CodeInternalError, Message: err.Error()}
		_ = p.send(resp)
		return
	}
	resp.Result = raw
	_ = p.send(resp)
}
