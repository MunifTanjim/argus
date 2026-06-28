// Package node wires argus's components together: the registry, the Claude
// Code discoverer over both tmux servers, and the JSON-RPC API server. It is
// the in-process core behind the argusd command.
package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// Node holds the wired-up core.
type Node struct {
	reg     *registry.Registry
	disc    *claudecode.Discoverer
	server  *api.Server
	clients map[session.TmuxServer]*tmux.Client

	id    string // stable node id announced to the gateway (composite-id prefix)
	label string // human-friendly node name (e.g. hostname)

	log *slog.Logger // operational logging; discards by default (see SetLogger)

	desktopNotify bool      // render incoming push.desktop notifications on this machine
	notifier      push.Sink // renders desktop notifications (OSNotifier in production)

	revealFn  func(ctx context.Context, c *tmux.Client, paneID string) error         // seam for tests; defaults to (*tmux.Client).Reveal
	focusedFn func(ctx context.Context, c *tmux.Client, paneID string) (bool, error) // seam for tests; defaults to (*tmux.Client).IsFocused

	pendingMu sync.Mutex
	pending   map[string]*pendingDecision // session id -> parked PermissionRequest

	subsMu sync.Mutex
	conns  map[api.Notifier]*connSubs // per-connection transcript subscriptions
}

// SetLogger routes the node's operational logging (scan/notify/cleanup failures)
// to l. Off by default so an embedded node never writes to a TUI's stderr; the
// standalone `start` command enables it.
func (d *Node) SetLogger(l *slog.Logger) {
	if l != nil {
		d.log = l
		d.server.SetLogger(l) // also turn on per-request logging
	}
}

// scan rescans discovery once, logging (not swallowing) any failure.
func (d *Node) scan(ctx context.Context) {
	if err := d.disc.ScanOnce(ctx); err != nil {
		d.log.Warn("discovery scan failed", "err", err)
	}
}

// SetDesktopNotify enables (or disables) rendering of push.desktop notifications
// on this node's machine, wiring the click command so a clicked notification
// focuses the session, and (re)building the notifier so render failures log
// through the node's logger. Call before Run — not safe once the API server is
// serving (it mutates fields read by handler goroutines).
func (d *Node) SetDesktopNotify(enabled bool, click func(string) []string) {
	d.desktopNotify = enabled
	d.notifier = push.NewOSNotifier(d.log, click)
}

// SetIdentity overrides the node's id and label (defaults derive from the
// hostname). The id is the routing key the gateway namespaces sessions under, so it
// must be stable across reconnects and unique within a fleet.
func (d *Node) SetIdentity(id, label string) {
	if id != "" {
		d.id = id
	}
	if label != "" {
		d.label = label
	}
}

// ID and Label report the node's identity (see SetIdentity).
func (d *Node) ID() string    { return d.id }
func (d *Node) Label() string { return d.label }

// Registry exposes the node's live session store so a co-located gateway can
// aggregate it as an in-process source.
func (d *Node) Registry() *registry.Registry { return d.reg }

// DispatchFunc exposes the node's control handlers so a co-located gateway can
// route control calls into the local engine without a network hop.
func (d *Node) DispatchFunc() api.DispatchFunc { return d.server.DispatchFunc() }

// clientFor returns the tmux client for a session's server.
func (d *Node) clientFor(s session.Session) (*tmux.Client, error) {
	c, ok := d.clients[s.Tmux.Server]
	if !ok {
		return nil, fmt.Errorf("no tmux client for server %q", s.Tmux.Server)
	}
	return c, nil
}

// resolveLocal maps a bare or composite "<nodeID>:<localID>" session id to a
// session local to this node, stripping this node's own prefix first (a composite
// id this node owns is locally addressable once unprefixed). A foreign or unknown
// id yields resolve's error. Shared by the focus-click and focus-suppression paths.
func (d *Node) resolveLocal(id string) (session.Session, *tmux.Client, error) {
	if nodeID, local, ok := session.SplitCompositeID(id); ok && nodeID == d.id {
		id = local
	}
	return d.resolve(id)
}

// resolve looks up a session and its tmux client and pane.
func (d *Node) resolve(id string) (session.Session, *tmux.Client, error) {
	s, ok := d.reg.Get(id)
	if !ok {
		return session.Session{}, nil, fmt.Errorf("unknown session: %s", id)
	}
	if !s.Controllable() {
		return s, nil, fmt.Errorf("%s: %w", id, api.ErrNoTerminalControl)
	}
	c, err := d.clientFor(s)
	return s, c, err
}

// New builds a Node watching the user's default tmux server and argus's
// private "-L argus" socket.
func New() *Node {
	return newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerDefault: tmux.New(""),
		session.TmuxServerArgus:   tmux.New("argus"),
	})
}

// newNode wires a Node over the given tmux clients. Exposed for tests that
// inject isolated tmux servers.
func newNode(clients map[session.TmuxServer]*tmux.Client) *Node {
	reg := registry.New()
	disc := claudecode.NewDiscoverer(reg, clients)

	host, _ := os.Hostname()
	if host == "" {
		host = "argusd"
	}
	d := &Node{
		reg: reg, disc: disc, clients: clients, id: host, label: host,
		log:     slog.New(slog.DiscardHandler),
		pending: map[string]*pendingDecision{},
		conns:   map[api.Notifier]*connSubs{},
	}
	d.notifier = push.NewOSNotifier(nil, nil)
	d.revealFn = func(ctx context.Context, c *tmux.Client, paneID string) error {
		return c.Reveal(ctx, paneID)
	}
	d.focusedFn = func(ctx context.Context, c *tmux.Client, paneID string) (bool, error) {
		return c.IsFocused(ctx, paneID)
	}

	srv := api.NewServer()
	d.registerHandlers(srv)
	// Stream registry changes to each connected client.
	srv.OnConnect(func(n api.Notifier) func() {
		d.registerConn(n)
		events, cancel := reg.Subscribe()
		// Send the current snapshot first so a fresh client is in sync. A client may
		// hang up before/while we stream it — e.g. a liveness probe that connects and
		// immediately closes (localNodeRunning). That's expected: stop on the first
		// failed notify rather than spamming one per session against a dead connection.
		// The live-event loop below treats a dropped client the same way (silent return).
		for _, s := range reg.Snapshot() {
			if err := n.Notify(api.MethodSessionEvent, registry.Event{Type: registry.EventAdded, Session: s}); err != nil {
				break
			}
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
			d.dropConn(n)
		}
	})

	d.server = srv
	return d
}

// Run scans once at startup and serves the API on the unix socket until ctx is
// cancelled. Further discovery is on demand. The socket (and a stale leftover)
// are managed automatically.
func (d *Node) Run(ctx context.Context, socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	// A leftover socket is only safe to remove if no node is actually listening
	// on it. Probe first: if a live node answers, refuse rather than unlink it
	// out from under the running node (which would orphan it and let teardown of
	// either node delete the other's socket). Only a stale/dead socket is removed.
	if _, err := os.Stat(socketPath); err == nil {
		if conn, derr := net.Dial("unix", socketPath); derr == nil {
			conn.Close()
			return fmt.Errorf("a node is already running at %s", socketPath)
		} else if !nodeAbsent(derr) {
			return fmt.Errorf("probing socket %s: %w", socketPath, derr)
		}
		if err := os.Remove(socketPath); err != nil {
			d.log.Warn("removing stale socket failed", "path", socketPath, "err", err)
		}
	}

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer func() {
		l.Close()
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			d.log.Warn("removing socket failed", "path", socketPath, "err", err)
		}
	}()

	// Scan once at startup; subsequent scans are on demand (client refresh,
	// hook events, spawn/kill).
	go d.scan(ctx)

	// Close the listener when the context is done so Serve returns.
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	err = d.server.Serve(l)
	if ctx.Err() != nil {
		return nil // shutdown requested
	}
	return err
}

// nodeAbsent reports whether a dial error means no node is listening: a missing
// socket file (ENOENT) or a stale one with no listener (ECONNREFUSED). Any other
// error is a real problem and should not be treated as "safe to remove".
func nodeAbsent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}
