// Package node is the in-process core behind argusd: registry, Claude Code
// discoverer over both tmux servers, and the JSON-RPC API server.
package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/forge"
	"github.com/MunifTanjim/argus/internal/host"
	"github.com/MunifTanjim/argus/internal/mcphttp"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/projectreg"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/selfupgrade"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
	"github.com/MunifTanjim/argus/internal/wsscript"
)

// Node holds the wired-up core.
type Node struct {
	reg         *registry.Registry
	mcpListener net.Listener
	mcpToken    string
	// adapterList holds agent adapters in priority order; [0] is the default.
	adapterList []adapter.Adapter
	adapters    map[string]adapter.Adapter
	procTable   func(context.Context) (procwalk.Table, error)
	discs       []adapter.Discoverer
	server      *api.Server
	clients     map[session.TmuxServer]*tmux.Client

	id      string // stable node id announced to the gateway (composite-id prefix)
	label   string // human-friendly node name (e.g. hostname)
	version string // binary version, reported to clients via identify/server.info

	identity       e2e.KeyPair // node's Noise static keypair (E2E channel responder)
	identityPubB64 string      // base64 public half, announced to the gateway
	e2ee           bool

	signer       trustlog.SignerKey // node's Ed25519 signer keypair (locked-mode trust log); zero when unset
	signerPubB64 string             // base64 public half, announced to the gateway roster

	// Locked-mode trust state: nil/zero in TOFU mode; enforcement engages only once
	// a trust store is loaded via EnableTrustLog.
	trust             atomic.Pointer[trustlog.SyncStore] // locked-mode trust store; nil when off
	trustGate         trustpin.Gate                      // fail-closed state when unpinned on a locked network
	localDisabledFlag atomic.Bool                        // per-node locked-mode escape hatch (persisted marker)
	trustPath         string                             // on-disk chain path for persistence
	trustPersistMu    sync.Mutex                         // serializes atomic temp-file+rename persist
	pinSource         string                             // "config", "file", or "none"
	pinGenesis        []byte                             // copy of the resolved genesis hash
	// pinMu serializes the pin-state decision and guards every post-start read and
	// write of pinGenesis/pinSource/retainedEntries. Quarantined() and
	// rejectsChannels() must stay lock-free.
	pinMu              sync.Mutex
	retainedEntries    *trustlog.EntryStore // every retained entry, indexed by hash; the sole source of the sync offer; guarded by pinMu
	lastUnplacedLogged int                  // last unplaced count that triggered a warning; 0 means none; guarded by pinMu
	lastDisjointLogged bool                 // true when the last sync returned disjoint; suppresses repeat warnings; guarded by pinMu

	activeResponder atomic.Pointer[relayResponder]
	activeUplink    atomic.Pointer[api.Peer] // current gateway uplink peer for event-triggered pulls
	peers           atomic.Pointer[peerPool] // channels to peer nodes on the current uplink; nil when down
	roster          *peerRoster              // gateway roster mirror, never nil

	// trustPullTrigger rate-limits pulls a gateway trustlog.changed hint can
	// provoke, so one untrusted notification amplifies into at most one pull/window.
	trustPullTrigger triggerLimiter
	testTriggerPeer  atomic.Pointer[trustCaller] // test override for triggerPeer

	mirrorPrefix string // wraps the argus-mirror-<termID> marker for naming mirror sessions
	mirrorSuffix string

	caps api.NodeCapabilities // what this node supports (e.g. spawn = tmux present)

	wakelock *host.Wakelock

	log *slog.Logger // operational logging; discards by default (see SetLogger)

	desktopNotify bool      // render desktop notifications on this machine
	notifier      push.Sink // renders desktop notifications (OSNotifier in production)

	adopting sync.Map // session id -> struct{}; one workspace adoption at a time

	scripts         *wsscript.Runner
	projreg         *projectreg.Registry                                              // node-local project/workspace registry; nil = disabled
	worktreeDirTmpl string                                                            // Go-template path for new worktrees
	issueTmpl       string                                                            // Go-template branch name for issue workspaces
	forgeFor        func(ctx context.Context, repoDir string) (forge.Provider, error) // seam for tests; defaults to forge.For
	pushStore       *push.Store                                                       // per-node Web Push subscription store; nil = push disabled
	pushDeliverer   atomic.Pointer[push.Deliverer]                                    // egress for encrypted mobile pushes over the uplink RPC

	revealFn        func(ctx context.Context, c *tmux.Client, paneID string) error         // seam for tests; defaults to (*tmux.Client).Reveal
	focusedFn       func(ctx context.Context, c *tmux.Client, paneID string) (bool, error) // seam for tests; defaults to (*tmux.Client).IsFocused
	restoreMirrorFn func(c *tmux.Client, m *mirrorState)                                   // seam for tests; defaults to (*Node).restoreMirror

	instanceID string                                                                                 // tells mods this node process from a restarted one
	spawnFn    func(ctx context.Context, p api.SpawnParams, presetID string) (api.SpawnResult, error) // seam for tests; defaults to (*Node).spawn

	pagents           *pagent.Store // nil = pagent.* disabled
	pagentCfg         config.AgentsConfig
	pagentPrimaryFile string           // node state file holding the primary agent id
	pagentNow         func() time.Time // clock of idle close and session start grace; tests replace it
	pagentLocks       sync.Map         // agent id -> *sync.Mutex; serializes session starts per agent
	pagentPending     sync.Map         // spawned session id -> time.Time; not registered yet

	// extractorFor picks the memory extractor for an agent's settings; nil when
	// none is available. Tests replace it.
	extractorFor func(set pagent.Settings) memory.Extractor
	captureMu    sync.Mutex
	captureErrAt map[string]time.Time // thread id -> last memory.error and capture retry gate
	tidyMu       sync.Mutex
	tidyAt       map[string]time.Time // agent id -> when tidy last ran
	tidyTry      map[string]time.Time // agent id -> last tidy attempt
	tidyIdle     map[string]string    // agent id -> memory commit at the last tidy that changed nothing

	// routineStartFn starts a claimed routine run; tests replace it.
	routineStartFn  func(ctx context.Context, a pagent.Agent, r routine.Routine, run pagent.RoutineRun) error
	routineMu       sync.Mutex
	routineCache    map[string]map[string]routineFile // agent id -> slug -> parsed file
	routineErrSent  map[string]string                 // agent id/slug -> key of the last routine.error
	routineSkip     map[string]string                 // agent id/slug -> reason of the current skip streak
	routineStale    map[string]time.Time              // agent id/slug -> due time of the last stale notice
	routineRuns     sync.Map                          // run id -> *runCtl; runs this process executes
	routineCtx      context.Context                   // the run context of runs started by RPC and webhooks; set by Run
	routineWG       sync.WaitGroup                    // run goroutines
	routinePayloads sync.Map                          // run id -> webhook body of a run not started yet
	notify          notifyState                       // held and delayed agent pushes

	clientMu        sync.Mutex
	lastClientAt    time.Time // when a client was last attached
	lastClientSaved time.Time
	lastClientFile  string // node state file holding lastClientAt

	// resumeFn relaunches a past agent session; defaults to (*Node).resumeAdopt.
	// errResumeStarting comes with the launching session's id.
	resumeFn func(ctx context.Context, agent, agentSessionID, cwd, existingID string) (string, error)
	// killFn ends a session; defaults to (*Node).killSession.
	killFn func(ctx context.Context, s session.Session) error

	orch            *orch.Store // nil = orch.* and message.* disabled
	orchPrev        map[string]session.Status
	orchWorked      map[string]bool      // session id -> reported working since the last pass
	orchLive        map[string]bool      // actor keys live at the last pass
	orchGone        map[string]time.Time // actor key -> when it was first seen gone
	orchGoneGrace   time.Duration
	orchSpawning    *spawningKeys
	orchSpawnWindow time.Duration
	orchSettled     chan struct{} // closed once startup discovery has settled
	orchSettleOne   *sync.Once    // closes orchSettled
	orchSeeded      bool
	orchRecheck     atomic.Bool          // a peer queued a message: check its recipient is live
	orchHeld        map[string]time.Time // actor key -> when its delivery hold ends
	// deliverFn types a delivery into a session; tests replace it.
	deliverFn func(ctx context.Context, s session.Session, text string) error
	// paneProbe reports a pane session's last client activity and its screen;
	// tests replace it.
	paneProbe     func(ctx context.Context, s session.Session) (time.Time, string, error)
	orchSigs      map[string]string            // session id -> last pending interaction sig
	orchParents   map[string]session.ParentRef // session id -> parent last published
	orchKickCh    chan struct{}
	orchExpiredAt time.Time
	orchRequests  requestLocks
	ownerSinks    atomic.Pointer[[]push.Sink] // Immediate then Delayed sinks, set by StartPush

	outboxEvery time.Duration                                                           // retry period of the outbox flush
	outboxSend  func(ctx context.Context, node, method string, p json.RawMessage) error // seam for tests; defaults to the peer pool

	pendingMu sync.Mutex
	pending   map[string]*pendingDecision // request id -> parked PermissionRequest

	modPermsMu sync.Mutex
	modPerms   map[string]*modPermission // by request id

	subsMu      sync.Mutex
	conns       map[api.Notifier]*connSubs // per-connection transcript subscriptions
	transcripts *transcriptCache

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

	binMu    sync.Mutex
	binCache map[string]bool // agent binary name -> found on PATH (memoized)
}

// SetLogger routes operational logging to l. Off by default so a node never
// writes to stderr unasked; the `start` command enables it.
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
	if d.projreg != nil {
		for _, s := range d.reg.Snapshot() {
			d.adoptSessionWorkspace(ctx, s)
		}
	}
}

// Worktree mapping is stable, so a session with a workspace id is not resolved
// again.
func (d *Node) adoptSessionWorkspace(ctx context.Context, s session.Session) {
	if d.projreg == nil || s.WorkspaceID != "" || s.Status == session.StatusDead {
		return
	}
	// A scan's add event and the scan's own pass both reach new sessions.
	if _, busy := d.adopting.LoadOrStore(s.ID, struct{}{}); busy {
		return
	}
	defer d.adopting.Delete(s.ID)
	if cur, ok := d.reg.Get(s.ID); ok && cur.WorkspaceID != "" {
		return
	}
	dir := sessionDir(s)
	if dir == "" {
		return
	}
	wsID, isNew, err := d.projreg.Adopt(ctx, dir)
	if err != nil {
		d.log.Warn("adopt workspace", "session", s.ID, "err", err)
		return
	}
	if wsID != "" {
		d.reg.SetWorkspaceID(s.ID, wsID)
	}
	if isNew {
		d.notifyProjectsChanged()
	}
}

// watchSessionWorkspacesLoop gives a session added outside a scan (a beacon
// hello, a hook) its workspace now rather than at the next scan.
func (d *Node) watchSessionWorkspacesLoop(ctx context.Context, events <-chan registry.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == registry.EventAdded && ev.Session.WorkspaceID == "" {
				go d.adoptSessionWorkspace(ctx, ev.Session)
			}
		}
	}
}

func (d *Node) adapterFor(agent string) adapter.Adapter {
	if a, ok := d.adapters[agent]; ok {
		return a
	}
	return d.adapterList[0]
}

// SetDesktopNotify toggles desktop notification rendering on this machine;
// click wires a clicked notification to focus the session. Call before Run —
// not safe once serving (mutates fields read by handler goroutines).
func (d *Node) SetDesktopNotify(enabled bool, click func(string) []string) {
	d.desktopNotify = enabled
	d.notifier = push.NewOSNotifier(d.log, click)
}

func (d *Node) DesktopNotifyEnabled() bool { return d.desktopNotify }

// SetPushStore enables node-side push registration (register/unregister/test).
// Call before StartPush. nil disables push.
func (d *Node) SetPushStore(store *push.Store) { d.pushStore = store }

// SetProjectRegistry with nil disables the registry: project.list returns
// empty and sessions carry no workspace_id.
func (d *Node) SetProjectRegistry(r *projectreg.Registry) { d.projreg = r }

// SetWorktreeDirTemplate sets the Go-template path for new worktrees (vars
// .Repo.Name, .Branch.Name, .Branch.Slug), relative to a project's main working
// tree. It rejects a template that does not parse or names an unknown var.
func (d *Node) SetWorktreeDirTemplate(t string) error {
	if _, err := renderWorktreePath(t, "repo", "branch", "/"); err != nil {
		return err
	}
	d.worktreeDirTmpl = t
	return nil
}

// SetIssueBranchTemplate sets the Go-template branch name for workspaces
// created from an issue (vars .Issue.Number, .Issue.Title, .Issue.Slug). It
// rejects a template that does not parse or names an unknown var.
func (d *Node) SetIssueBranchTemplate(t string) error {
	if _, err := renderIssueBranch(t, forge.Issue{Number: 1, Title: "title"}); err != nil {
		return err
	}
	d.issueTmpl = t
	return nil
}

// SetPushDeliverer wires how encrypted mobile pushes reach the gateway for egress.
// Safe to call concurrently (e.g. from runUplink on reconnect while StartPush reads it).
func (d *Node) SetPushDeliverer(dv push.Deliverer) { d.pushDeliverer.Store(&dv) }

// SetIdentity overrides the node's id and label (default: hostname). The id is
// the gateway's routing key, so it must be stable across reconnects and unique
// within a fleet.
func (d *Node) SetIdentity(id, label string) {
	if id != "" {
		d.id = id
		if d.orch != nil {
			d.orch.SetNodeID(id)
		}
	}
	if label != "" {
		d.label = label
	}
}

// SetVersion records the node's binary version, reported to clients via
// identify/server.info. Call before Run.
func (d *Node) SetVersion(v string) { d.version = v }

// SetSelfUpgrade serves the server.* upgrade methods on the socket and on
// relay channels. Call before Run.
func (d *Node) SetSelfUpgrade(s *selfupgrade.Service) { s.Register(d.server) }

// SetStop serves server.stop on the socket only; remote channels cannot reach
// it. Call before Run.
func (d *Node) SetStop(stop func()) { api.HandleStop(d.server, stop) }

// SetIdentityKey's public half is announced to the gateway (identity_pubkey). Call before Run.
func (d *Node) SetIdentityKey(kp e2e.KeyPair) {
	d.identity = kp
	d.identityPubB64 = base64.StdEncoding.EncodeToString(kp.Public)
}

// SetE2EE toggles the blind-relay uplink; default false uses the plaintext uplink. Call before ConnectGateway.
func (d *Node) SetE2EE(enabled bool) { d.e2ee = enabled }

// SetSignerKey sets the node's Ed25519 signer keypair. Call before Run.
func (d *Node) SetSignerKey(kp trustlog.SignerKey) {
	d.signer = kp
	d.signerPubB64 = base64.StdEncoding.EncodeToString(kp.Public)
}

// SignerPubKey returns the base64 Ed25519 signer public half, or "" if unset.
func (d *Node) SignerPubKey() string { return d.signerPubB64 }

// SignerPublic returns a copy of the raw Ed25519 signer public half, or nil if unset.
func (d *Node) SignerPublic() []byte {
	if len(d.signer.Public) == 0 {
		return nil
	}
	return append([]byte(nil), d.signer.Public...)
}

// Quarantined reports whether this node is quarantined on a locked network it
// cannot pin. In TOFU mode (nil trust store) this is always false.
func (d *Node) Quarantined() bool { return d.trustGate.Tripped() }

func (d *Node) localDisabled() bool { return d.localDisabledFlag.Load() }

// reevaluateTrustChannels drops live client channels no longer authorized after a
// trust-store advance. No-op when no uplink responder is active.
func (d *Node) reevaluateTrustChannels() {
	if r := d.activeResponder.Load(); r != nil {
		r.reevaluate()
	}
}

// remoteDispatch rejects lock.*, orch.* and server.stop (local-only) and dispatches the rest.
func (d *Node) remoteDispatch() api.DispatchFunc {
	full := d.server.DispatchFunc()
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if strings.HasPrefix(method, "lock.") || strings.HasPrefix(method, "orch.") || method == api.MethodServerStop {
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
		return full(ctx, method, params)
	}
}

// peerDispatch serves channels another node opened (chanState.opener set): it
// is remoteDispatch plus orch.*. lock.* and server.stop stay local-only.
func (d *Node) peerDispatch() api.DispatchFunc {
	full := d.server.DispatchFunc()
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if strings.HasPrefix(method, "lock.") || method == api.MethodServerStop {
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
		return full(ctx, method, params)
	}
}

// uplinkDispatch answers only node.identify and ping. Ping must pass so the gateway's
// keepalive probe is not counted a failure that tears the uplink down.
func (d *Node) uplinkDispatch() api.DispatchFunc {
	full := d.remoteDispatch()
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case api.MethodNodeIdentify, api.MethodPing:
			return full(ctx, method, params)
		default:
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
	}
}

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

// Registry exposes the node's live session store.
func (d *Node) Registry() *registry.Registry { return d.reg }

// DispatchFunc exposes the local engine without a network hop. It returns
// remoteDispatch, so that lock.* methods registered on d.server stay reachable
// only from the local unix socket (CLI).
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

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "argusd"
	}
	// Probe tmux once so a node without it advertises no spawn support rather than
	// failing at use. Bounded so a wedged tmux binary can't hang startup.
	var caps api.NodeCapabilities
	if c, ok := clients[session.TmuxServerArgus]; ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		caps.SpawnSession = c.Available(ctx)
		caps.Terminal = caps.SpawnSession
		cancel()
	}

	wakelock := host.NewWakelock("")
	caps.HostWakelock = wakelock.Supported()
	caps.CBOR = true
	caps.CrossNodeOrch = crossNodeOrchVersion

	d := &Node{
		reg: reg, clients: clients, id: hostname, label: hostname,
		adapterList:  adapterList,
		procTable:    procwalk.Snapshot,
		adapters:     adapterByAgent,
		discs:        discs,
		caps:         caps,
		wakelock:     wakelock,
		log:          slog.New(slog.DiscardHandler),
		pending:      map[string]*pendingDecision{},
		modPerms:     map[string]*modPermission{},
		instanceID:   newID(),
		forgeFor:     forge.For,
		conns:        map[api.Notifier]*connSubs{},
		transcripts:  newTranscriptCache(transcriptCacheMaxIdle, transcriptCacheIdleTTL),
		terms:        map[api.Notifier]*connTerms{},
		sessionTerms: map[string]*term{},
		resuming:     map[string]string{},
	}
	d.roster = newPeerRoster(func() string { return d.id })
	d.notifier = push.NewOSNotifier(nil, nil)
	d.scripts = wsscript.NewRunner(d.notifyProjectsChanged)
	d.revealFn = func(ctx context.Context, c *tmux.Client, paneID string) error {
		return c.Reveal(ctx, paneID)
	}
	d.focusedFn = func(ctx context.Context, c *tmux.Client, paneID string) (bool, error) {
		return c.IsFocused(ctx, paneID)
	}
	d.restoreMirrorFn = d.restoreMirror
	d.spawnFn = d.spawn
	d.resumeFn = d.resumeAdopt
	d.killFn = d.killSession
	d.pagentNow = time.Now
	d.extractorFor = defaultExtractor

	srv := api.NewServer()
	d.registerHandlers(srv)
	srv.OnConnect(d.streamRegistry)

	d.server = srv
	return d
}

// streamRegistry pushes the current session snapshot to n, then every subsequent
// registry change, until the returned stop func runs. Both transports use it: a
// direct socket connection (via OnConnect) and an E2E channel terminated from the
// gateway uplink.
func (d *Node) streamRegistry(n api.Notifier) func() {
	d.registerConn(n)
	events, cancel := d.reg.Subscribe()
	// A client may hang up mid-stream (e.g. a liveness probe); stop on the first
	// failed notify rather than spamming one per session against a dead connection.
	for _, s := range d.reg.Snapshot() {
		if err := n.Notify(api.MethodSessionEvent, registry.Event{Type: registry.EventAdded, Session: d.withCaps(s)}); err != nil {
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
				ev.Session = d.withCaps(ev.Session)
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
}

// UseMCP hands the node's MCP settings to adapters that need them outside
// their agent's config files. Call before Run, so spawns see them.
func (d *Node) UseMCP(opts adapter.InstallOptions) {
	for _, a := range d.adapterList {
		if u, ok := a.(adapter.MCPUser); ok {
			u.UseMCP(opts)
		}
	}
}

// ListenMCP binds the local MCP listener on 127.0.0.1:port; Run serves it.
// Call before Run.
func (d *Node) ListenMCP(port int, token string) error {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	d.mcpListener, d.mcpToken = l, token
	return nil
}

// Run scans once at startup and serves the API on the unix socket until ctx is
// cancelled. The socket (and a stale leftover) are managed automatically.
func (d *Node) Run(ctx context.Context, socketPath string) error {
	modPath := ModSocketPath(socketPath)
	if err := checkSocketPath(modPath); err != nil {
		return err
	}
	l, err := api.ListenUnix(socketPath, d.log)
	if err != nil {
		return err
	}
	defer d.scripts.Close()
	defer d.dropAllTerms()
	d.log.Info("serving local API", "socket", socketPath)
	d.wakelock.SetLogger(d.log)
	d.wakelock.Restore()
	defer func() {
		l.Close()
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			d.log.Warn("removing socket failed", "path", socketPath, "err", err)
		}
	}()

	// The probe above proved no live node serves socketPath, so a leftover
	// mod socket named after it is stale.
	_ = os.Remove(modPath)
	ml, err := api.ListenOwnerOnly(modPath, d.log)
	if err != nil {
		return err
	}
	modSrv := &http.Server{Handler: d.modHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = modSrv.Serve(ml) }()
	d.log.Info("serving mod socket", "socket", modPath)
	defer func() {
		_ = modSrv.Close()
		if err := os.Remove(modPath); err != nil && !os.IsNotExist(err) {
			d.log.Warn("removing mod socket failed", "path", modPath, "err", err)
		}
	}()

	if d.mcpListener != nil {
		defer d.serveMCP()()
	}

	// Sweep orphaned mirror sessions left by unclean disconnects before any
	// scan so stale sessions don't surface as live ones.
	d.reapMirrors(context.Background())

	workspaceEvents, stopWorkspaceEvents := d.reg.Subscribe()
	defer stopWorkspaceEvents()
	go d.watchSessionWorkspacesLoop(ctx, workspaceEvents)

	// Subsequent scans are on demand (client refresh, hook events, spawn/kill).
	go func() {
		d.scan(ctx)
		if d.orch != nil {
			d.orchSettle(ctx)
		}
	}()

	// Detach any live terminal whose session ends, so a viewer never lands on the
	// bare shell the agent leaves behind.
	go d.watchSessionExits(ctx)

	if d.orch != nil {
		go d.orchLoop(ctx)
		go d.outboxLoop(ctx)
	}

	if d.pagents != nil {
		pagentEvents, stopPAgentEvents := d.reg.Subscribe()
		defer stopPAgentEvents()
		go d.pagentWatch(ctx, pagentEvents)
		go d.pagentIdleLoop(ctx)
		go d.pagentCaptureLoop(ctx)
		go d.pagentTidyLoop(ctx)
		d.routineMu.Lock()
		d.routineCtx = ctx
		d.routineMu.Unlock()
		go d.pagentRoutineLoop(ctx)
		stopWebhooks, err := d.serveWebhooks(ctx)
		if err != nil {
			d.log.Warn("pagent: routine webhooks disabled", "err", err)
		} else {
			defer stopWebhooks()
		}
	}

	return d.server.ServeContext(ctx, l)
}

// mcpIdleTimeout closes keep-alive connections that agent runtimes leave open.
const mcpIdleTimeout = 2 * time.Minute

// serveMCP serves the bound MCP listener. The returned func stops the server.
func (d *Node) serveMCP() func() {
	srv := &http.Server{
		Handler:           mcphttp.NewHandler(d.mcpToken, d.version, mcpBackend{d}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       mcpIdleTimeout,
	}
	go func() { _ = srv.Serve(d.mcpListener) }()
	d.log.Info("serving mcp", "addr", d.mcpListener.Addr().String())
	return func() { _ = srv.Close() }
}
