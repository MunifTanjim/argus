package client

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/trustpin"
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
	genesisHash []byte         // pinned trust-log genesis hash; nil = trust-log sync off
	static      e2e.KeyPair    // stable client identity; threaded into every E2EClient
	chainPath   string         // locked-mode chain persist path; "" = no persistence
	plaintext   bool           // when true, connectOnce builds a plaintext (no-Noise) E2EClient
	gate        *trustpin.Gate // shared across reconnects; a dropped connection must not clear quarantine
	ctx         context.Context
	cancel      context.CancelFunc

	events chan api.Notification
	states chan bool
	paths  chan NodePath
	kick   chan struct{}
	via    map[string]string // last forwarded via per node; supervise goroutine only

	mu  sync.Mutex
	cur *E2EClient // nil while disconnected
	p2p P2POptions // applied to every E2EClient
}

// NewReconnectingE2EClient dials + handshakes once (so startup failure surfaces),
// then maintains the connection until Close.
func NewReconnectingE2EClient(ctx context.Context, dial api.Dialer) (*ReconnectingE2EClient, error) {
	static, err := e2e.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return newReconnecting(ctx, dial, nil, static, "", false)
}

// NewReconnectingE2EClientLocked pins a trust-log genesis hash AND a stable persisted
// client identity — the locked-mode client: it presents the same authenticated
// static across reconnects (the key a signer authorizes) and syncs the trust log.
// chainPath, if non-empty, seeds the trust store from the last persisted chain on
// reconnect (so a reconnect cannot be used to serve a stale pre-revocation chain).
func NewReconnectingE2EClientLocked(ctx context.Context, dial api.Dialer, genesisHash []byte, static e2e.KeyPair, chainPath string) (*ReconnectingE2EClient, error) {
	return newReconnecting(ctx, dial, genesisHash, static, chainPath, false)
}

// NewReconnectingPlainClient dials + connects once (so startup failure surfaces),
// then maintains a plaintext (no Noise encryption) connection until Close.
func NewReconnectingPlainClient(ctx context.Context, dial api.Dialer) (*ReconnectingE2EClient, error) {
	return newReconnecting(ctx, dial, nil, e2e.KeyPair{}, "", true)
}

func newReconnecting(ctx context.Context, dial api.Dialer, genesisHash []byte, static e2e.KeyPair, chainPath string, plaintext bool) (*ReconnectingE2EClient, error) {
	cctx, cancel := context.WithCancel(ctx)
	c := &ReconnectingE2EClient{
		dial:        dial,
		genesisHash: genesisHash,
		static:      static,
		chainPath:   chainPath,
		plaintext:   plaintext,
		gate:        &trustpin.Gate{},
		ctx:         cctx,
		cancel:      cancel,
		events:      make(chan api.Notification, 256),
		states:      make(chan bool, 8),
		paths:       make(chan NodePath, 256),
		via:         map[string]string{},
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
func (c *ReconnectingE2EClient) connectOnce() (*E2EClient, error) {
	conn, err := c.dial(c.ctx)
	if err != nil {
		return nil, err
	}
	var cur *E2EClient
	if c.plaintext {
		cur, err = NewE2EClientPlain(conn)
	} else {
		cur, err = NewE2EClientWithGate(conn, c.static, c.genesisHash, c.chainPath, c.gate)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	c.mu.Lock()
	p2p := c.p2p
	c.mu.Unlock()
	cur.SetP2POptions(p2p)
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
		c.resetPaths()
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

func (c *ReconnectingE2EClient) forward(cur *E2EClient) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-cur.Done():
			c.drainPaths(cur)
			return
		case ev := <-cur.Events():
			select {
			case c.events <- ev:
			default: // drop for a slow consumer
			}
		case p := <-cur.Paths():
			c.sendPath(p)
		}
	}
}

// drainPaths forwards the paths cur reported before it dropped, so none is
// lost to select picking Done first.
func (c *ReconnectingE2EClient) drainPaths(cur *E2EClient) {
	for {
		select {
		case p := <-cur.Paths():
			c.sendPath(p)
		default:
			return
		}
	}
}

// sendPath forwards p and tracks it; an empty Via (link lost) drops the node,
// so resetPaths reports only nodes that had a link.
func (c *ReconnectingE2EClient) sendPath(p NodePath) {
	if p.Via == "" {
		delete(c.via, p.NodeID)
	} else {
		c.via[p.NodeID] = p.Via
	}
	select {
	case c.paths <- p:
	default:
	}
}

// resetPaths reports every tracked node without a link once the dropped
// client stops forwarding; the next client reports each node as it opens.
func (c *ReconnectingE2EClient) resetPaths() {
	ids := make([]string, 0, len(c.via))
	for id := range c.via {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c.sendPath(NodePath{NodeID: id})
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

// TrustTip returns the live client's trust-log tip (nil while disconnected/off).
func (c *ReconnectingE2EClient) TrustTip() []byte {
	if cur := c.current(); cur != nil {
		return cur.TrustTip()
	}
	return nil
}

// Quarantined reports whether this device is unpinned on a network that has a
// trust log. Sticky across reconnects.
func (c *ReconnectingE2EClient) Quarantined() bool { return c.gate.Tripped() }

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

// Paths reports node link changes, stable across reconnects.
func (c *ReconnectingE2EClient) Paths() <-chan NodePath { return c.paths }

// NodeVias returns the link each open node uses (empty while disconnected).
func (c *ReconnectingE2EClient) NodeVias() map[string]string {
	if cur := c.current(); cur != nil {
		return cur.NodeVias()
	}
	return map[string]string{}
}

// SetP2POptions sets the direct-link options for the live client and every
// reconnect.
func (c *ReconnectingE2EClient) SetP2POptions(o P2POptions) {
	c.mu.Lock()
	c.p2p = o
	cur := c.cur
	c.mu.Unlock()
	if cur != nil {
		cur.SetP2POptions(o)
	}
}

// Reconnect forces an immediate reconnect attempt (resets backoff). No-op while connected.
func (c *ReconnectingE2EClient) Reconnect() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// AuthTipKnownForTest exposes the inner client's authTip entry; test-only.
func (c *ReconnectingE2EClient) AuthTipKnownForTest(identityPub []byte) (tip []byte, known bool) {
	cur := c.current()
	if cur == nil {
		return nil, false
	}
	return cur.authTipEntry(identityPub)
}

func (c *ReconnectingE2EClient) Close() error {
	c.cancel()
	if cur := c.current(); cur != nil {
		return cur.Close()
	}
	return nil
}
