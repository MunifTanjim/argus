package client

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

const (
	reconnectBaseBackoff = 500 * time.Millisecond
	reconnectMaxBackoff  = 15 * time.Second
)

// ReconnectingE2EClient maintains an E2E connection to a gateway, re-dialing with
// backoff and re-running discovery + handshakes on each reconnect. It satisfies
// tui.Client with a stable Events()/States() stream across reconnects.
type ReconnectingE2EClient struct {
	dial        api.Dialer
	genesisHead []byte // pinned trust-log genesis; nil = trust-log sync off
	ctx         context.Context
	cancel      context.CancelFunc

	events chan api.Notification
	states chan bool
	kick   chan struct{}

	mu  sync.Mutex
	cur *E2EClient // nil while disconnected
}

// NewReconnectingE2EClient dials + handshakes once (so startup failure surfaces),
// then maintains the connection until Close.
func NewReconnectingE2EClient(ctx context.Context, dial api.Dialer) (*ReconnectingE2EClient, error) {
	return newReconnecting(ctx, dial, nil)
}

// NewReconnectingE2EClientWithGenesis is NewReconnectingE2EClient plus a pinned
// trust-log genesis head, so every (re)connection syncs the network's trust log.
func NewReconnectingE2EClientWithGenesis(ctx context.Context, dial api.Dialer, genesisHead []byte) (*ReconnectingE2EClient, error) {
	return newReconnecting(ctx, dial, genesisHead)
}

func newReconnecting(ctx context.Context, dial api.Dialer, genesisHead []byte) (*ReconnectingE2EClient, error) {
	cctx, cancel := context.WithCancel(ctx)
	c := &ReconnectingE2EClient{
		dial:        dial,
		genesisHead: genesisHead,
		ctx:         cctx,
		cancel:      cancel,
		events:      make(chan api.Notification, 256),
		states:      make(chan bool, 8),
		kick:        make(chan struct{}, 1),
	}
	cur, err := c.connectOnce()
	if err != nil {
		cancel()
		return nil, err
	}
	c.mu.Lock()
	c.cur = cur
	c.mu.Unlock()
	go c.supervise()
	return c, nil
}

// connectOnce dials, builds a fresh E2EClient, and completes discovery + handshakes.
//
// SECURITY(slice-5): the client builds a fresh, empty trust-log store each
// reconnect and re-pulls from scratch — it keeps no persisted HEAD, so a
// malicious gateway can serve a stale pre-revocation chain to a reconnecting
// client. Harmless while the client enforces nothing; client persistence MUST
// land together with client-side enforcement in Slice 5, or revocation is
// defeatable by forcing a reconnect.
func (c *ReconnectingE2EClient) connectOnce() (*E2EClient, error) {
	conn, err := c.dial(c.ctx)
	if err != nil {
		return nil, err
	}
	cur, err := NewE2EClientWithGenesis(conn, c.genesisHead)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := cur.Connect(); err != nil {
		cur.Close()
		return nil, err
	}
	return cur, nil
}

func (c *ReconnectingE2EClient) current() *E2EClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

// supervise forwards events from the live E2EClient and reconnects when it drops.
func (c *ReconnectingE2EClient) supervise() {
	for {
		cur := c.current()
		if cur == nil {
			return
		}
		c.forward(cur) // blocks until cur drops or ctx ends
		if c.ctx.Err() != nil {
			return
		}
		cur.Close()
		c.mu.Lock()
		c.cur = nil
		c.mu.Unlock()
		c.emitState(false)

		next := c.reconnect()
		if next == nil {
			return // ctx cancelled while retrying
		}
		c.mu.Lock()
		c.cur = next
		c.mu.Unlock()
		c.emitState(true)
	}
}

// forward pumps the current client's notifications onto the stable events channel
// until that client drops or the context ends.
func (c *ReconnectingE2EClient) forward(cur *E2EClient) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-cur.Done():
			return
		case ev := <-cur.Events():
			select {
			case c.events <- ev:
			default: // drop for a slow consumer
			}
		}
	}
}

// reconnect re-dials + re-handshakes with capped backoff; a Reconnect() kick short-
// circuits the wait. Returns nil if ctx ended first.
func (c *ReconnectingE2EClient) reconnect() *E2EClient {
	backoff := reconnectBaseBackoff
	for {
		if cur, err := c.connectOnce(); err == nil {
			return cur
		}
		select {
		case <-c.ctx.Done():
			return nil
		case <-c.kick:
			backoff = reconnectBaseBackoff
		case <-time.After(backoff):
			if backoff *= 2; backoff > reconnectMaxBackoff {
				backoff = reconnectMaxBackoff
			}
		}
	}
}

func (c *ReconnectingE2EClient) emitState(connected bool) {
	select {
	case c.states <- connected:
	case <-c.ctx.Done():
	}
}

// DeviceAuthorized reports whether pub is authorized by the live client's synced
// trust log (false while disconnected or when trust-log sync is off).
func (c *ReconnectingE2EClient) DeviceAuthorized(pub []byte) bool {
	cur := c.current()
	return cur != nil && cur.DeviceAuthorized(pub)
}

// TrustHead returns the live client's trust-log HEAD (nil while disconnected/off).
func (c *ReconnectingE2EClient) TrustHead() []byte {
	if cur := c.current(); cur != nil {
		return cur.TrustHead()
	}
	return nil
}

// Call routes to the live E2E client, erroring promptly when disconnected.
func (c *ReconnectingE2EClient) Call(method string, params, out any) error {
	cur := c.current()
	if cur == nil {
		return errors.New("client: disconnected")
	}
	return cur.Call(method, params, out)
}

// Events is the stable notification stream across reconnects.
func (c *ReconnectingE2EClient) Events() <-chan api.Notification { return c.events }

// States reports connection transitions (false on drop, true on reconnect).
func (c *ReconnectingE2EClient) States() <-chan bool { return c.states }

// Reconnect forces an immediate reconnect attempt (resets backoff). No-op while connected.
func (c *ReconnectingE2EClient) Reconnect() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Close stops reconnecting and terminates the current connection.
func (c *ReconnectingE2EClient) Close() error {
	c.cancel()
	if cur := c.current(); cur != nil {
		return cur.Close()
	}
	return nil
}
