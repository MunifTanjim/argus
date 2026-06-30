package api

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

const (
	reconnectBaseBackoff = 500 * time.Millisecond
	reconnectMaxBackoff  = 15 * time.Second
)

// Dialer establishes a new connection. Called for the initial connect and every
// reconnect, so it must be safe to invoke repeatedly.
type Dialer func(ctx context.Context) (net.Conn, error)

// ReconnectingClient re-dials with capped exponential backoff when its connection
// drops, keeping a stable Events() stream across reconnects and reporting state
// transitions on States(). Reconnect() forces an immediate retry. Safe for concurrent use.
type ReconnectingClient struct {
	dial   Dialer
	ctx    context.Context
	cancel context.CancelFunc

	events chan Notification
	states chan bool
	kick   chan struct{}

	mu   sync.Mutex
	peer *Peer // nil while disconnected
}

// NewReconnectingClient dials once (so startup failure surfaces immediately) and
// then maintains the connection until Close.
func NewReconnectingClient(ctx context.Context, dial Dialer) (*ReconnectingClient, error) {
	cctx, cancel := context.WithCancel(ctx)
	c := &ReconnectingClient{
		dial:   dial,
		ctx:    cctx,
		cancel: cancel,
		events: make(chan Notification, 64),
		states: make(chan bool, 8),
		kick:   make(chan struct{}, 1),
	}
	conn, err := dial(cctx)
	if err != nil {
		cancel()
		return nil, err
	}
	c.setPeer(conn)
	go c.supervise()
	return c, nil
}

// setPeer installs a new peer that relays notifications onto the stable events channel.
func (c *ReconnectingClient) setPeer(conn net.Conn) {
	p := NewPeer(conn, PeerOptions{
		OnNotify: func(n Notification) {
			select {
			case c.events <- n:
			default: // drop if the consumer is slow
			}
		},
	})
	c.mu.Lock()
	c.peer = p
	c.mu.Unlock()
}

func (c *ReconnectingClient) currentPeer() *Peer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peer
}

// supervise reconnects with backoff each time the live peer drops, emitting state
// transitions, until ctx ends.
func (c *ReconnectingClient) supervise() {
	for {
		p := c.currentPeer()
		select {
		case <-c.ctx.Done():
			return
		case <-p.Done():
		}
		if c.ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.peer = nil
		c.mu.Unlock()
		c.emitState(false)

		if !c.reconnect() {
			return // ctx cancelled while retrying
		}
		c.emitState(true)
	}
}

// reconnect re-dials with capped exponential backoff; a Reconnect() kick short-
// circuits the wait and resets backoff. Returns false if ctx ended first.
func (c *ReconnectingClient) reconnect() bool {
	backoff := reconnectBaseBackoff
	for {
		conn, err := c.dial(c.ctx)
		if err == nil {
			c.setPeer(conn)
			return true
		}
		select {
		case <-c.ctx.Done():
			return false
		case <-c.kick:
			backoff = reconnectBaseBackoff
		case <-time.After(backoff):
			if backoff *= 2; backoff > reconnectMaxBackoff {
				backoff = reconnectMaxBackoff
			}
		}
	}
}

func (c *ReconnectingClient) emitState(connected bool) {
	select {
	case c.states <- connected:
	case <-c.ctx.Done():
	}
}

// Events returns the stable notification stream, not closed on reconnect; it
// carries notifications from whichever connection is currently live.
func (c *ReconnectingClient) Events() <-chan Notification { return c.events }

// States reports connection-state transitions: false when the connection drops, true
// when it is re-established.
func (c *ReconnectingClient) States() <-chan bool { return c.states }

// Reconnect forces an immediate reconnect attempt (and resets backoff). No-op while connected.
func (c *ReconnectingClient) Reconnect() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Call routes to the live peer, erroring promptly when disconnected rather than blocking.
func (c *ReconnectingClient) Call(method string, params, out any) error {
	p := c.currentPeer()
	if p == nil {
		return errors.New("api: disconnected")
	}
	return p.Call(method, params, out)
}

// Close stops reconnecting and terminates the current connection.
func (c *ReconnectingClient) Close() error {
	c.cancel()
	if p := c.currentPeer(); p != nil {
		return p.Close()
	}
	return nil
}
