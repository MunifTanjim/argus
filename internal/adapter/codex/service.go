package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

const sessionIdleTTL = time.Hour

// dismissGrace is how long a dismissed thread's status is ignored, so the
// statuses of its interrupted turn winding down do not track it again.
const dismissGrace = 30 * time.Second

type threadEntry struct {
	cwd, path, name, model string
	repo                   string
	tokens                 int
	lastActivity           time.Time
	activeTurn             string
	status                 string // root's last status type: active, idle, systemError
}

type serverClient struct {
	server session.TmuxServer
	client *tmux.Client
}

type paneRef struct {
	server session.TmuxServer
	paneID string
}

// discoverer mirrors the Codex app-server daemon's loaded threads into the
// registry. Liveness is the daemon's: a session exists while its thread is loaded.
type discoverer struct {
	reg      *registry.Registry
	servers  []serverClient
	sockPath func() (string, error)
	ctx      context.Context
	retry    time.Duration

	pumpOnce sync.Once

	// reconcileMu serializes pane scans so an older result cannot overwrite a newer one.
	reconcileMu sync.Mutex

	connMu sync.Mutex
	cur    *rpcConn

	mu       sync.Mutex
	threads  map[string]*threadEntry      // root thread id -> entry
	parentOf map[string]string            // subagent thread id -> parent thread id
	subs     map[string]bool              // thread ids subscribed on the current connection
	ignored  map[string]bool              // ephemeral or rollout-less threads
	pending  map[string][]*pendingRequest // root thread id -> open requests, oldest first
	panes    map[string]paneRef           // root thread id -> adopted viewer pane
	reqSeq   uint64                       // last request token handed out

	fileChanges map[string]string    // fileChangeKey -> patch of a started fileChange item
	dismissed   map[string]time.Time // dismissed thread id -> end of its grace period

	modelMu     sync.Mutex
	modelNames  map[string]string
	modelsMtime time.Time

	listPanes func(ctx context.Context, sc serverClient) ([]tmux.Pane, error)
}

func newDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *discoverer {
	d := &discoverer{
		reg:      reg,
		sockPath: daemonSocketPath,
		ctx:      context.Background(),
		retry:    3 * time.Second,
		threads:  map[string]*threadEntry{},
		parentOf: map[string]string{},
		subs:     map[string]bool{},
		ignored:  map[string]bool{},
		pending:  map[string][]*pendingRequest{},
		panes:    map[string]paneRef{},

		fileChanges: map[string]string{},
		dismissed:   map[string]time.Time{},
		listPanes: func(ctx context.Context, sc serverClient) ([]tmux.Pane, error) {
			return sc.client.ListPanes(ctx)
		},
	}
	for server, client := range clients {
		d.servers = append(d.servers, serverClient{server: server, client: client})
	}
	return d
}

func daemonSocketPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "app-server-control", "app-server-control.sock"), nil
}

func (d *discoverer) ScanOnce(ctx context.Context) error {
	d.pumpOnce.Do(func() { go d.runPump(d.ctx) })
	d.mu.Lock()
	idle := len(d.threads) == 0 && len(d.panes) == 0
	d.mu.Unlock()
	if !idle {
		// Panes only bind to tracked threads, so skip ps and tmux when none are.
		d.reconcileMu.Lock()
		if bound, ok := d.scanPanes(ctx); ok {
			d.reconcilePanes(bound)
		}
		d.reconcileMu.Unlock()
	}
	d.sweepIdle(ctx)
	return nil
}

func (d *discoverer) conn() *rpcConn {
	d.connMu.Lock()
	defer d.connMu.Unlock()
	return d.cur
}

func (d *discoverer) setConn(c *rpcConn) {
	d.connMu.Lock()
	d.cur = c
	d.connMu.Unlock()
}

func (d *discoverer) runPump(ctx context.Context) {
	log := slog.Default().With("scope", "codex")
	last := ""
	for ctx.Err() == nil {
		err := d.runConn(ctx)
		// Log each distinct failure once, so a missing daemon is visible without
		// a line every retry. A dropped connection always logs: it had connected.
		if err != nil && ctx.Err() == nil {
			if msg := err.Error(); msg != last {
				log.Info("codex app-server daemon connection failed; retrying", "err", err)
				last = msg
			}
			if errors.Is(err, errConnClosed) {
				last = ""
			}
		}
		if sleep(ctx, d.retry) {
			return
		}
	}
}

func sleep(ctx context.Context, dur time.Duration) (canceled bool) {
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

func (d *discoverer) runConn(ctx context.Context) error {
	path, err := d.sockPath()
	if err != nil {
		return err
	}
	c, err := dialRPC(ctx, path)
	if err != nil {
		return err
	}
	defer c.close()
	if err := c.initialize(ctx); err != nil {
		return err
	}
	d.setConn(c)
	// The daemon unloads every thread when it exits, so a dropped connection ends
	// every session; reconnect re-tracks whatever the daemon holds then.
	defer d.dropAll()
	defer d.setConn(nil)
	if err := d.syncLoaded(ctx, c); err != nil {
		return err
	}
	for {
		m, ok := c.next(ctx)
		if !ok {
			return errConnClosed
		}
		d.handle(ctx, c, m)
	}
}

func (d *discoverer) syncLoaded(ctx context.Context, c *rpcConn) error {
	var ids []string
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := c.call(ctx, "thread/loaded/list", params, &res); err != nil {
			return err
		}
		ids = append(ids, res.Data...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
	}
	for _, id := range ids {
		d.track(ctx, c, id)
	}
	return nil
}

// track subscribes to a thread and registers it. thread/resume both subscribes and
// returns the thread, but fails until the thread's first turn writes a rollout;
// then thread/read supplies the metadata and onStatus retries the subscription.
func (d *discoverer) track(ctx context.Context, c *rpcConn, id string) {
	var res struct {
		Thread cxThread `json:"thread"`
	}
	subscribed := true
	if err := c.call(ctx, "thread/resume", map[string]any{"threadId": id, "excludeTurns": true}, &res); err != nil {
		var re *rpcError
		if !errors.As(err, &re) {
			return
		}
		subscribed = false
		if err := c.call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &res); err != nil {
			return
		}
	}
	if res.Thread.ID == "" {
		return
	}
	if subscribed {
		d.mu.Lock()
		d.subs[id] = true
		d.mu.Unlock()
	}
	d.adopt(res.Thread)
}

func (d *discoverer) adopt(t cxThread) {
	if !t.trackable() {
		d.mu.Lock()
		d.ignored[t.ID] = true
		d.mu.Unlock()
		return
	}
	if p := t.parent(); p != "" {
		d.mu.Lock()
		d.parentOf[t.ID] = p
		d.mu.Unlock()
		return
	}
	repo := gittree.RepoName(t.Cwd)
	d.mu.Lock()
	e := d.threads[t.ID]
	if e == nil {
		e = &threadEntry{}
		d.threads[t.ID] = e
	}
	e.cwd, e.repo, e.path, e.model = t.Cwd, repo, *t.Path, t.Model
	if name := t.title(); name != "" {
		e.name = name
	}
	e.lastActivity = time.Now()
	d.mu.Unlock()
	d.applyStatus(t.ID, t.Status)
}

// rootOfLocked follows parentOf to the root thread. Unknown ids are their own root.
func (d *discoverer) rootOfLocked(id string) string {
	for i := 0; i < 16; i++ {
		p, ok := d.parentOf[id]
		if !ok {
			break
		}
		id = p
	}
	return id
}

func (d *discoverer) handle(ctx context.Context, c *rpcConn, m inbound) {
	if m.ID != nil {
		d.handleRequest(ctx, c, m)
		return
	}
	switch m.Method {
	case "item/started", "item/completed":
		d.noteFileChange(m.Method, m.Params)
	case "thread/started":
		var p struct {
			Thread cxThread `json:"thread"`
		}
		if json.Unmarshal(m.Params, &p) == nil && p.Thread.ID != "" {
			d.adopt(p.Thread)
		}
	case "thread/status/changed":
		var p struct {
			ThreadID string       `json:"threadId"`
			Status   threadStatus `json:"status"`
		}
		if json.Unmarshal(m.Params, &p) == nil && p.ThreadID != "" {
			d.onStatus(ctx, c, p.ThreadID, p.Status)
		}
	case "thread/closed":
		d.forget(requestThreadID(m.Params))
	case "thread/name/updated":
		var p struct {
			ThreadID   string  `json:"threadId"`
			ThreadName *string `json:"threadName"`
		}
		if json.Unmarshal(m.Params, &p) == nil && p.ThreadName != nil && *p.ThreadName != "" {
			name := *p.ThreadName
			if d.updateEntry(p.ThreadID, func(e *threadEntry) bool {
				changed := e.name != name
				e.name = name
				return changed
			}) {
				d.applyHook(p.ThreadID, "", nil)
			}
		}
	case "turn/started", "turn/completed":
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		d.updateEntry(p.ThreadID, func(e *threadEntry) bool {
			if m.Method == "turn/started" {
				e.activeTurn = p.Turn.ID
			} else if e.activeTurn == p.Turn.ID {
				e.activeTurn = ""
			}
			return false
		})
	case "thread/tokenUsage/updated":
		var p struct {
			ThreadID   string `json:"threadId"`
			TokenUsage struct {
				Total struct {
					TotalTokens int `json:"totalTokens"`
				} `json:"total"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		tokens := p.TokenUsage.Total.TotalTokens
		if d.updateEntry(p.ThreadID, func(e *threadEntry) bool {
			changed := e.tokens != tokens
			e.tokens = tokens
			return changed
		}) {
			d.applyHook(p.ThreadID, "", nil)
		}
	case "serverRequest/resolved":
		d.onResolved(m.Params)
	}
}

func (d *discoverer) onStatus(ctx context.Context, c *rpcConn, id string, st threadStatus) {
	d.mu.Lock()
	if until, ok := d.dismissed[id]; ok {
		if st.Type != "notLoaded" && time.Now().Before(until) {
			d.mu.Unlock()
			return
		}
		delete(d.dismissed, id)
	}
	ignored := d.ignored[id]
	_, known := d.threads[id]
	_, isChild := d.parentOf[id]
	subscribed := d.subs[id]
	d.mu.Unlock()
	if ignored {
		return
	}
	if !st.waiting() {
		d.dropPendingFrom(id)
	}
	if st.Type == "notLoaded" {
		d.forget(id)
		return
	}
	// Unknown threads are tracked only once they turn active: new threads arrive
	// via thread/started and loaded ones via syncLoaded, so an idle status for an
	// unknown id is a dismissed thread winding down.
	if st.Type == "active" && (!subscribed || (!known && !isChild)) {
		d.track(ctx, c, id) // fetches the thread and applies its current status
		return
	}
	if !known && !isChild {
		return
	}
	if isChild {
		// Release a finished subagent; parentOf stays so a later active re-tracks it.
		if subscribed && (st.Type == "idle" || st.Type == "systemError") {
			_ = c.call(ctx, "thread/unsubscribe", map[string]any{"threadId": id}, nil)
			d.mu.Lock()
			delete(d.subs, id)
			d.mu.Unlock()
		}
		return
	}
	d.applyStatus(id, st)
}

// applyStatus sets a root session's status. A waiting thread is left alone: its
// server request (replayed on subscribe) carries the prompt.
func (d *discoverer) applyStatus(id string, st threadStatus) {
	if st.Type == "notLoaded" {
		d.forget(id)
		return
	}
	d.mu.Lock()
	if e := d.threads[id]; e != nil {
		e.status = st.Type
	}
	d.mu.Unlock()
	switch {
	case st.waiting():
		d.touch(id)
	case st.Type == "active":
		d.refreshCard(id)
	default: // idle, systemError
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
		d.upsert(id, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	}
}

// refreshCard shows the oldest open request on a root session, or Working when
// none remain.
func (d *discoverer) refreshCard(root string) {
	d.mu.Lock()
	head := d.headPending(root)
	active := false
	if e := d.threads[root]; e != nil {
		active = e.status == "active"
	}
	d.mu.Unlock()
	if head != nil {
		d.upsert(root, session.StatusAwaitingInput, head.interaction)
		return
	}
	if active {
		d.upsert(root, session.StatusWorking, nil)
		return
	}
	d.upsert(root, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
}

// resumable reports whether `codex resume <id>` can load a tracked thread. A thread
// with no turn yet has no rollout file, and resume exits with "no rollout found".
// Untracked ids (history) are left to the resume command itself.
func (d *discoverer) resumable(id string) bool {
	d.mu.Lock()
	path := ""
	if e := d.threads[id]; e != nil {
		path = e.path
	}
	d.mu.Unlock()
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}

// updateEntry runs f on a tracked root's entry under d.mu and returns f's result,
// or false when id is not tracked.
func (d *discoverer) updateEntry(id string, f func(e *threadEntry) bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := d.threads[id]; e != nil {
		return f(e)
	}
	return false
}

func (d *discoverer) touch(id string) {
	d.updateEntry(id, func(e *threadEntry) bool {
		e.lastActivity = time.Now()
		return false
	})
}

// upsert sets status and interaction on a tracked root session.
func (d *discoverer) upsert(id string, st session.Status, ix *session.Interaction) {
	d.touch(id)
	d.applyHook(id, st, ix)
}

// applyHook pushes the entry's fields to the registry. An empty status leaves
// status and interaction unchanged.
// Callers must not hold d.mu.
func (d *discoverer) applyHook(id string, st session.Status, ix *session.Interaction) {
	names := d.cachedModelNames()
	// d.mu is held across the registry call so a concurrent forget cannot send
	// Dead between the lookup and ApplyHook, which would re-create a card nobody
	// tracks. Lock order is d.mu -> registry (as in reconcilePanes); the registry
	// never calls back into the discoverer.
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.threads[id]
	if e == nil {
		return
	}
	u := registry.HookUpdate{
		Agent:              Agent,
		AgentSessionID:     id,
		Name:               e.name,
		Cwd:                e.cwd,
		Repo:               e.repo,
		TranscriptPath:     e.path,
		Frontend:           session.FrontendExternal,
		Input:              session.InputAPI,
		Status:             st,
		Summary:            summaryFor(e.model, e.name, e.tokens, names),
		Interaction:        ix,
		ReplaceInteraction: st != "",
	}
	if pane, ok := d.panes[id]; ok {
		u.Server, u.PaneID = pane.server, pane.paneID
	}
	d.reg.ApplyHook(u)
}

// forget drops a thread the daemon no longer holds. known reports whether it
// was a tracked root, whose card was removed.
func (d *discoverer) forget(id string) (known bool) {
	if id == "" {
		return false
	}
	d.mu.Lock()
	_, known = d.threads[id]
	var root string
	dropped := false
	if _, isChild := d.parentOf[id]; isChild {
		root = d.rootOfLocked(id)
		dropped = d.dropPendingLocked(root, func(pr *pendingRequest) bool { return pr.threadID == id })
	}
	delete(d.threads, id)
	delete(d.parentOf, id)
	delete(d.subs, id)
	delete(d.ignored, id)
	delete(d.pending, id)
	delete(d.panes, id)
	for k := range d.fileChanges {
		if strings.HasPrefix(k, id+"\x00") {
			delete(d.fileChanges, k)
		}
	}
	d.mu.Unlock()
	if known {
		d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
	}
	if dropped {
		d.refreshCard(root)
	}
	return known
}

func (d *discoverer) dropAll() {
	d.mu.Lock()
	ids := make([]string, 0, len(d.threads))
	for id := range d.threads {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.forget(id)
	}
	d.mu.Lock()
	d.parentOf = map[string]string{}
	d.subs = map[string]bool{}
	d.ignored = map[string]bool{}
	d.fileChanges = map[string]string{}
	d.mu.Unlock()
}

func (d *discoverer) cachedModelNames() map[string]string {
	path, err := modelsCachePath()
	if err != nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	mt := info.ModTime()
	d.modelMu.Lock()
	defer d.modelMu.Unlock()
	if d.modelNames != nil && d.modelsMtime.Equal(mt) {
		return d.modelNames
	}
	d.modelNames = loadModelNames()
	d.modelsMtime = mt
	return d.modelNames
}

// startDaemon runs `codex app-server daemon start`, which is a no-op when the
// daemon is already running.
var startDaemon = func(ctx context.Context) error {
	cmd := shell.NewCommandContext(ctx, "codex", "app-server", "daemon", "start")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codex: start daemon: %w: %s", err, cmd.StdErr().String())
	}
	return nil
}

// awaitConn returns the live connection, starting the daemon if none exists and
// waiting for the pump to connect.
func (d *discoverer) awaitConn(ctx context.Context) (*rpcConn, error) {
	if c := d.conn(); c != nil {
		return c, nil
	}
	if err := startDaemon(ctx); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c := d.conn(); c != nil {
			return c, nil
		}
		if sleep(ctx, 50*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("codex: daemon unavailable")
}

func startTurn(ctx context.Context, c *rpcConn, id, text string) error {
	return c.call(ctx, "turn/start", map[string]any{"threadId": id, "input": []map[string]string{{"type": "text", "text": text}}}, nil)
}

func (d *discoverer) sendPrompt(ctx context.Context, id, text string) error {
	c := d.conn()
	if c == nil {
		return fmt.Errorf("codex: daemon unavailable")
	}
	return startTurn(ctx, c, id, text)
}

// spawnSession starts a thread in cwd with the user's config.toml approval and
// sandbox defaults. The starting client is subscribed to the new thread.
func (d *discoverer) spawnSession(ctx context.Context, cwd, prompt string) (string, error) {
	c, err := d.awaitConn(ctx)
	if err != nil {
		return "", err
	}
	var res struct {
		Thread cxThread `json:"thread"`
	}
	if err := c.call(ctx, "thread/start", map[string]any{"cwd": cwd}, &res); err != nil {
		return "", err
	}
	id := res.Thread.ID
	if id == "" {
		return "", fmt.Errorf("codex: thread/start returned no thread id")
	}
	d.mu.Lock()
	d.subs[id] = true
	d.mu.Unlock()
	d.adopt(res.Thread)
	if prompt == "" {
		return id, nil
	}
	d.upsert(id, session.StatusWorking, nil)
	if err := startTurn(ctx, c, id, prompt); err != nil {
		// The thread stays idle and sends no status, so restore its card here.
		d.refreshCard(id)
		return "", err
	}
	return id, nil
}

// dismiss stops the active turn, unsubscribes and removes the card. The daemon
// unloads the thread about a minute after its last subscriber leaves.
func (d *discoverer) dismiss(ctx context.Context, id string) {
	d.mu.Lock()
	turn, active := "", false
	if e := d.threads[id]; e != nil {
		turn, active = e.activeTurn, e.status == "active"
	}
	// Drop the root's subagents too, unsubscribing those still subscribed.
	var children, subscribed []string
	for child := range d.parentOf {
		if d.rootOfLocked(child) == id {
			children = append(children, child)
		}
	}
	until := time.Now().Add(dismissGrace)
	d.dismissed[id] = until
	for _, child := range children {
		if d.subs[child] {
			subscribed = append(subscribed, child)
		}
		delete(d.parentOf, child)
		delete(d.subs, child)
		d.dismissed[child] = until
	}
	d.mu.Unlock()
	if c := d.conn(); c != nil {
		if turn == "" && active {
			// The turn started before this connection saw turn/started.
			if t, running, err := latestTurn(ctx, c, id); err == nil && running {
				turn = t
			}
		}
		if turn != "" {
			_ = c.call(ctx, "turn/interrupt", map[string]any{"threadId": id, "turnId": turn}, nil)
		}
		_ = c.call(ctx, "thread/unsubscribe", map[string]any{"threadId": id}, nil)
		for _, child := range subscribed {
			_ = c.call(ctx, "thread/unsubscribe", map[string]any{"threadId": child}, nil)
		}
	}
	if !d.forget(id) {
		// The user's kill always removes the card, even one the discoverer lost.
		d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
	}
}

// sweepIdle unsubscribes from and removes sessions idle past sessionIdleTTL,
// except those waiting on the user or shown in an adopted pane.
func (d *discoverer) sweepIdle(ctx context.Context) {
	cutoff := time.Now().Add(-sessionIdleTTL)
	var stale []string
	d.mu.Lock()
	for id, e := range d.threads {
		// status covers a turn that started before this connection saw turn/started.
		if len(d.pending[id]) > 0 || e.activeTurn != "" || e.status == "active" {
			continue
		}
		if _, hasPane := d.panes[id]; hasPane {
			continue
		}
		if e.lastActivity.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	d.mu.Unlock()
	for _, id := range stale {
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		d.dismiss(dctx, id)
		cancel()
	}
}
