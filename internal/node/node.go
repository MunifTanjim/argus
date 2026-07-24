// Package node is the in-process core behind argusd: registry, Claude Code
// discoverer over both tmux servers, and the JSON-RPC API server.
package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// Node holds the wired-up core.
type Node struct {
	reg *registry.Registry
	// adapterList holds agent adapters in priority order; [0] is the default.
	adapterList []adapter.Adapter
	adapters    map[string]adapter.Adapter
	discs       []adapter.Discoverer
	server      *api.Server
	clients     map[session.TmuxServer]*tmux.Client

	id      string // stable node id announced to the gateway (composite-id prefix)
	label   string // human-friendly node name (e.g. hostname)
	version string // binary version, reported to clients via identify/server.info

	identity       e2e.KeyPair // node's Noise static keypair (E2E channel responder)
	identityPubB64 string      // base64 public half, announced to the gateway

	signer       trustlog.SignerKey // node's Ed25519 signer keypair (locked-mode trust log)
	signerPubB64 string             // base64 public half, announced to the gateway roster

	beacon            trustlog.SignerKey       // node's Ed25519 beacon keypair (anti-equivocation; every node)
	beaconPubB64      string                   // base64 public half, announced to the gateway roster
	beaconCounter     atomic.Uint64            // monotonic emission counter; bumped by makeBeacon
	beaconCounterPath string                   // path for counter persistence; "" = disabled
	activeUplink      atomic.Pointer[api.Peer] // current gateway uplink peer for beacon.offer

	// Peer beacon courier ingest state (guarded by peerBeaconMu).
	// peerBeaconPubs is the set of roster-known peer beacon public keys
	// (excludes self). Populated by syncRoster on each uplink tick.
	// peerBeacons stores the latest counter-guarded accepted beacon per key.
	// peerBeaconMiss tracks consecutive unreconciled ticks (N=2 guard).
	// equivocation is set permanently once persistent peer beacon divergence is detected.
	peerBeaconMu   sync.Mutex
	peerBeaconPubs map[string]bool             // string(rawPub) → true
	peerBeacons    map[string]api.Beacon       // string(rawPub) → latest accepted beacon
	peerBeaconCtr  map[string]uint64           // string(rawPub) → last accepted counter
	peerBeaconMiss map[string]*beaconMissState // string(rawPub) → miss streak
	equivocation   atomic.Bool                 // set permanently on persistent peer beacon divergence

	mirrorPrefix string // wraps the argus-mirror-<termID> marker for naming mirror sessions
	mirrorSuffix string

	caps api.NodeCapabilities // what this node supports (e.g. spawn = tmux present)

	log *slog.Logger // operational logging; discards by default (see SetLogger)

	desktopNotify bool      // render desktop notifications for this node's own sessions locally (via push.Watch/DesktopSink)
	notifier      push.Sink // renders desktop notifications (OSNotifier in production)

	pushStore     *push.Store                    // per-node Web Push subscription store; nil = push disabled
	pushDeliverer atomic.Pointer[push.Deliverer] // egress for encrypted mobile pushes (uplink RPC or in-process)

	revealFn        func(ctx context.Context, c *tmux.Client, paneID string) error         // seam for tests; defaults to (*tmux.Client).Reveal
	focusedFn       func(ctx context.Context, c *tmux.Client, paneID string) (bool, error) // seam for tests; defaults to (*tmux.Client).IsFocused
	restoreMirrorFn func(c *tmux.Client, m *mirrorState)                                   // seam for tests; defaults to (*Node).restoreMirror

	pendingMu sync.Mutex
	pending   map[string]*pendingDecision // session id -> parked PermissionRequest

	subsMu sync.Mutex
	conns  map[api.Notifier]*connSubs // per-connection transcript subscriptions

	termsMu sync.Mutex
	terms   map[api.Notifier]*connTerms // per-connection live terminals

	sessionTermsMu sync.Mutex
	sessionTerms   map[string]*term // session id -> live terminal (single viewer per session)

	// openMu serializes terminal.open so evict→setup→register is atomic; without it
	// two concurrent opens of one session both register, defeating single-viewer.
	openMu sync.Mutex

	// Discovery registers a launched pane asynchronously, so a rapid second resume
	// can't see the first pane yet; the guard returns the launching session rather
	// than spawning a duplicate.
	resumeMu sync.Mutex
	resuming map[string]string // agent+session key -> launched session id

	trust          atomic.Pointer[trustlog.SyncStore] // locked-mode trust store; nil when off
	trustPath      string                             // on-disk chain path for persistence
	trustPersistMu sync.Mutex                         // serializes atomic temp-file+rename persist

	localDisabledFlag atomic.Bool // per-node locked-mode escape hatch (persisted marker)

	activeResponder atomic.Pointer[relayResponder] // the current uplink responder, if any
}

// SetLogger routes operational logging to l. Off by default so an embedded node
// never writes to a TUI's stderr; the standalone `start` command enables it.
func (d *Node) SetLogger(l *slog.Logger) {
	if l != nil {
		d.log = l
		d.server.SetLogger(l) // also turn on per-request logging
	}
}

// scan rescans discovery, logging any failure.
func (d *Node) scan(ctx context.Context) {
	for _, disc := range d.discs {
		if err := disc.ScanOnce(ctx); err != nil {
			d.log.Warn("discovery scan failed", "err", err)
		}
	}
}

func (d *Node) adapterFor(agent string) adapter.Adapter {
	if a, ok := d.adapters[agent]; ok {
		return a
	}
	return d.adapterList[0]
}

// SetDesktopNotify toggles whether this node renders desktop notifications for its
// own sessions locally (via push.Watch/DesktopSink); click wires a clicked notification
// to focus the session. Call before Run — not safe once serving (mutates fields read by handler goroutines).
func (d *Node) SetDesktopNotify(enabled bool, click func(string) []string) {
	d.desktopNotify = enabled
	d.notifier = push.NewOSNotifier(d.log, click)
}

func (d *Node) DesktopNotifyEnabled() bool { return d.desktopNotify }

// SetPushStore enables node-side push registration (register/unregister/test).
// Call before Run.
func (d *Node) SetPushStore(store *push.Store) { d.pushStore = store }

// SetPushDeliverer wires how encrypted mobile pushes reach the gateway for egress.
// Safe to call concurrently (e.g. from runUplink on reconnect while StartPush reads it).
func (d *Node) SetPushDeliverer(dv push.Deliverer) { d.pushDeliverer.Store(&dv) }

// SetIdentity overrides the node's id and label (default: hostname). The id is
// the gateway's routing key, so it must be stable across reconnects and unique
// within a fleet.
func (d *Node) SetIdentity(id, label string) {
	if id != "" {
		d.id = id
	}
	if label != "" {
		d.label = label
	}
}

// SetVersion records the node's binary version, reported to clients via
// identify/server.info. Call before Run.
func (d *Node) SetVersion(v string) { d.version = v }

// SetIdentityKey sets the node's Noise static keypair, whose public half is
// announced to the gateway (identity_pubkey) for E2E channel setup. Call before Run.
func (d *Node) SetIdentityKey(kp e2e.KeyPair) {
	d.identity = kp
	d.identityPubB64 = base64.StdEncoding.EncodeToString(kp.Public)
}

// SetSignerKey sets the node's Ed25519 signer keypair, whose public half is
// announced to the gateway roster (signer_pubkey) so a later `lock init` can
// designate it as a trusted signer. Call before Run. The private half stays local.
func (d *Node) SetSignerKey(kp trustlog.SignerKey) {
	d.signer = kp
	d.signerPubB64 = base64.StdEncoding.EncodeToString(kp.Public)
}

// SignerPubKey returns the base64 Ed25519 signer public half, or "" if unset.
func (d *Node) SignerPubKey() string { return d.signerPubB64 }

// SetBeaconKey sets the node's Ed25519 beacon keypair, whose public half is
// announced to the gateway roster (beacon_pubkey) for anti-equivocation. Call
// before Run. The private half stays local.
func (d *Node) SetBeaconKey(kp trustlog.SignerKey) {
	d.beacon = kp
	d.beaconPubB64 = base64.StdEncoding.EncodeToString(kp.Public)
}

// BeaconPub returns the base64 Ed25519 beacon public half, or "" if unset.
func (d *Node) BeaconPub() string { return d.beaconPubB64 }

// SetBeaconCounterPath enables beacon counter persistence. On call it reads the
// counter from the sibling file (path + ".counter") and seeds beaconCounter so
// the first emission after a restart is strictly greater than the last value
// peers accepted before the restart. makeBeacon writes the updated counter back
// on every emission. Call before Run, after SetBeaconKey.
func (d *Node) SetBeaconCounterPath(path string) {
	d.beaconCounterPath = path
	if n := LoadBeaconCounter(path); n > 0 {
		d.beaconCounter.Store(n)
	}
}

// Equivocation reports whether this node has detected a trust-log equivocation
// via the client courier: a peer's signed HEAD beacon whose tip could not be
// reconciled with this node's own chain after the N=2 persistence guard. Once
// set, this flag is never cleared for the lifetime of the node process.
func (d *Node) Equivocation() bool { return d.equivocation.Load() }

// SetMirrorAffixes sets the prefix and suffix that bracket the argus-mirror-<termID>
// marker in tmux mirror-session names. Call before Run.
func (d *Node) SetMirrorAffixes(prefix, suffix string) {
	d.mirrorPrefix = prefix
	d.mirrorSuffix = suffix
}

// mirrorMarker is the reaper's match anchor: every mirror session name contains it.
const mirrorMarker = "argus-mirror-"

// mirrorName composes the reaper-recognizable mirror session name for a term.
// The term id is sanitized because tmux session names may not contain ':' or '.'.
func (d *Node) mirrorName(termID string) string {
	return d.mirrorPrefix + mirrorMarker + sanitizeTmuxName(termID) + d.mirrorSuffix
}

// isMirror reports whether name is one of argus's own grouped mirror sessions.
func (d *Node) isMirror(name string) bool {
	return strings.HasPrefix(name, d.mirrorPrefix) &&
		strings.HasSuffix(name, d.mirrorSuffix) &&
		strings.Contains(name, mirrorMarker)
}

// sanitizeTmuxName replaces the characters tmux reserves in target specs so an
// arbitrary term id is safe to embed in a session name.
func sanitizeTmuxName(s string) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(s)
}

// ID and Label report the node's identity (see SetIdentity).
func (d *Node) ID() string      { return d.id }
func (d *Node) Label() string   { return d.label }
func (d *Node) Version() string { return d.version }

// Capabilities reports what this node supports (e.g. spawn = tmux available).
// Clients use it to gate features per node.
func (d *Node) Capabilities() api.NodeCapabilities { return d.caps }

// Registry exposes the node's live session store so a co-located gateway can
// aggregate it as an in-process source.
func (d *Node) Registry() *registry.Registry { return d.reg }

// remoteDispatch is the control surface exposed to REMOTE callers (the gateway
// uplink, E2E-relayed clients, a co-located gateway). It rejects lock.* methods:
// locked-mode control is local-admin only (the CLI dials the local unix socket),
// so a malicious gateway cannot disable enforcement or forge trust-log changes.
func (d *Node) remoteDispatch() api.DispatchFunc {
	full := d.server.DispatchFunc()
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if strings.HasPrefix(method, "lock.") {
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
		return full(ctx, method, params)
	}
}

// DispatchFunc exposes a restricted control surface for co-located gateways: routes
// non-lock.* calls into the local engine without a network hop. lock.* is local-
// admin only (CLI dials the unix socket); a co-located gateway serves remote clients,
// so it must not expose locked-mode control.
func (d *Node) DispatchFunc() api.DispatchFunc { return d.remoteDispatch() }

// clientFor returns the tmux client for a session's server.
func (d *Node) clientFor(s session.Session) (*tmux.Client, error) {
	c, ok := d.clients[s.Tmux.Server]
	if !ok {
		return nil, fmt.Errorf("no tmux client for server %q", s.Tmux.Server)
	}
	return c, nil
}

// resolveLocal resolves a bare or composite "<nodeID>:<localID>" id to a local
// session, stripping this node's own prefix first. Foreign/unknown ids yield
// resolve's error. Shared by the focus-click and focus-suppression paths.
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

	adapterList := adapters.All()
	adapterByAgent := make(map[string]adapter.Adapter, len(adapterList))
	discs := make([]adapter.Discoverer, 0, len(adapterList))
	for _, a := range adapterList {
		adapterByAgent[a.Agent()] = a
		discs = append(discs, a.NewDiscoverer(reg, clients))
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "argusd"
	}
	// Probe tmux once so a node without it advertises no spawn support rather than
	// failing at use. Bounded so a wedged tmux binary can't hang startup.
	var caps api.NodeCapabilities
	if c, ok := clients[session.TmuxServerArgus]; ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		caps.SpawnSession = c.Available(ctx)
		cancel()
	}

	d := &Node{
		reg: reg, clients: clients, id: host, label: host,
		adapterList:    adapterList,
		adapters:       adapterByAgent,
		discs:          discs,
		caps:           caps,
		log:            slog.New(slog.DiscardHandler),
		pending:        map[string]*pendingDecision{},
		conns:          map[api.Notifier]*connSubs{},
		terms:          map[api.Notifier]*connTerms{},
		sessionTerms:   map[string]*term{},
		resuming:       map[string]string{},
		peerBeaconPubs: map[string]bool{},
		peerBeacons:    map[string]api.Beacon{},
		peerBeaconCtr:  map[string]uint64{},
		peerBeaconMiss: map[string]*beaconMissState{},
	}
	d.notifier = push.NewOSNotifier(nil, nil)
	d.revealFn = func(ctx context.Context, c *tmux.Client, paneID string) error {
		return c.Reveal(ctx, paneID)
	}
	d.focusedFn = func(ctx context.Context, c *tmux.Client, paneID string) (bool, error) {
		return c.IsFocused(ctx, paneID)
	}
	d.restoreMirrorFn = d.restoreMirror

	srv := api.NewServer()
	d.registerHandlers(srv)
	// Stream registry changes to each connected client.
	srv.OnConnect(func(n api.Notifier) func() {
		d.registerConn(n)
		events, cancel := reg.Subscribe()
		// Send the current snapshot first so a fresh client is in sync. A client may
		// hang up mid-stream (e.g. a liveness probe); stop on the first failed notify
		// rather than spamming one per session against a dead connection.
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
// cancelled. The socket (and a stale leftover) are managed automatically.
func (d *Node) Run(ctx context.Context, socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	// Probe a leftover socket before removing it: if a live node answers, refuse
	// rather than unlink it out from under the running node (which would orphan it).
	// Only a stale/dead socket is removed.
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
	d.log.Info("serving local API", "socket", socketPath)
	defer func() {
		l.Close()
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			d.log.Warn("removing socket failed", "path", socketPath, "err", err)
		}
	}()

	// Sweep orphaned mirror sessions left by unclean disconnects before any
	// scan so stale sessions don't surface as live ones.
	d.reapMirrors(context.Background())

	// Subsequent scans are on demand (client refresh, hook events, spawn/kill).
	go d.scan(ctx)

	// Detach any live terminal whose session ends, so a viewer never lands on the
	// bare shell the agent leaves behind.
	go d.watchSessionExits(ctx)

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

// reevaluateTrustChannels drops live client channels no longer authorized after a
// trust-store advance. No-op when no uplink responder is active.
func (d *Node) reevaluateTrustChannels() {
	if r := d.activeResponder.Load(); r != nil {
		r.reevaluate()
	}
}

// nodeAbsent reports whether a dial error means no node is listening (ENOENT or
// ECONNREFUSED). Any other error is real and must not be treated as safe-to-remove.
func nodeAbsent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}
