package opencode

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const sessionIdleTTL = time.Hour

// Keep small: the service is a local single-process server.
const scanConcurrency = 4

// startTimeout bounds `opencode service start`.
const startTimeout = 20 * time.Second

type presenceEntry struct {
	lastActivity       time.Time
	status             session.Status
	awaitingPermission bool
	hydrated           bool
}

type discoverer struct {
	reg *registry.Registry

	dial func() (*client, bool)

	starts singleflight.Group // shares one `opencode service start` among concurrent callers
	wake   chan struct{}      // signals the event pump to dial now instead of after its retry delay

	pumpOnce    sync.Once
	pumpStarted atomic.Bool // set when the event pump goroutine has been launched
	seeded      atomic.Bool // idle-seed done (set only after a successful seed, so a transient error retries)
	ctx         context.Context

	argusTmux   *tmux.Client // argus tmux server, for adopting spawned terminal panes
	reconcileMu sync.Mutex   // serializes pane reconcile passes (concurrent scans must not interleave)

	mu       sync.Mutex
	presence map[string]*presenceEntry // opencode session id -> entry
	panes    map[string]string         // opencode session id -> adopted pane id (argus server)
	pendPerm map[string]string         // sessionID -> permissionID (SSE → Respond)
	pendForm map[string]*pendingForm   // sessionID -> pending question form (SSE → Respond)
}

type pendingForm struct {
	formID string
	fields []pendingField
}

// pendingField carries what Respond needs to translate an answer keyed by the
// emitted QuestionSpec.Question back into the form field key and option value.
type pendingField struct {
	key, question string
	multiselect   bool
	valueByLabel  map[string]string
}

// clients supplies the argus tmux server, used only to adopt spawned terminal
// panes; the live session list itself stays presence-driven, not pane-driven.
func newDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *discoverer {
	d := &discoverer{
		reg:       reg,
		argusTmux: clients[session.TmuxServerArgus],
		ctx:       context.Background(),
		presence:  map[string]*presenceEntry{},
		panes:     map[string]string{},
		pendPerm:  map[string]string{},
		pendForm:  map[string]*pendingForm{},
		wake:      make(chan struct{}, 1),
	}
	d.dial = func() (*client, bool) {
		info, ok := readServiceInfo()
		if !ok {
			return nil, false
		}
		return newClient(info), true
	}
	return d
}

func (d *discoverer) ScanOnce(ctx context.Context) error {
	// Start the live event pump on the first scan, before the dial check. The pump
	// retries its own connection, so it must not depend on the service being
	// reachable at this instant; otherwise a scan while the service is briefly down
	// skips it and live updates never begin. Without the pump, prompts appear only
	// when a later scan backfills them.
	d.pumpOnce.Do(func() {
		d.pumpStarted.Store(true)
		go d.runEventPump(d.ctx)
	})
	c, ok := d.dial()
	if !ok {
		return nil
	}
	active, err := c.listActive(ctx)
	if err == nil {
		// Adopt argus-spawned terminal panes first, so a pane-backed session surfaces
		// already controllable rather than appearing paneless until the next scan.
		if bound, ok := d.scanPanes(ctx); ok {
			d.reconcilePanes(ctx, bound, active)
		}
		// Sync pending prompts against the server before marking active sessions
		// Working, so a backfilled prompt is in place when the loop below skips it.
		d.reconcilePending(ctx, c, active)
		// Each hydrate is one GET; fan out so a burst of new sessions
		// doesn't serialize the scan.
		var ids []string
		for id := range active {
			// A session that waits for a form or permission reply stays in the active
			// list. Its interaction and AwaitingInput status come over SSE, so a bare
			// Working upsert here would clear the pending prompt (mergeInteraction drops
			// a rich interaction for a nil one). reconcilePending has already set the
			// pend entry for such a session, so skip it here.
			if d.hasPendingPrompt(id) {
				continue
			}
			ids = append(ids, id)
		}
		sort.Strings(ids)
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(scanConcurrency)
		for _, id := range ids {
			g.Go(func() error {
				select {
				case <-gctx.Done():
					return gctx.Err()
				default:
				}
				d.upsert(id, session.StatusWorking, nil)
				return nil
			})
		}
		_ = g.Wait()
		// One paginated list (vs N getSession) so it runs every scan; seeding
		// stays once so dismissed sessions aren't resurrected, and refreshKnown
		// only touches tracked ids.
		if sessions, lerr := c.listSessions(ctx); lerr == nil {
			if !d.seeded.Load() {
				d.seedIdle(sessions)
				d.seeded.Store(true)
			}
			d.refreshKnown(sessions)
		}
		d.sweepIdle()
	}
	return err
}

func (d *discoverer) upsert(id string, st session.Status, in *session.Interaction) {
	if id == "" {
		return
	}
	d.mu.Lock()
	e, existed := d.presence[id]
	if !existed {
		e = &presenceEntry{}
		d.presence[id] = e
	}
	e.lastActivity = time.Now()
	e.status = st
	e.awaitingPermission = in != nil && in.Kind == session.InteractionPermission
	needHydrate := !e.hydrated
	paneID := d.panes[id]
	d.mu.Unlock()

	u := registry.HookUpdate{
		Agent:          Agent,
		AgentSessionID: id,
		Status:         st,
		Frontend:       session.FrontendExternal,
		TranscriptPath: id,
		Input:          session.InputAPI,
	}
	if paneID != "" {
		u.Server = session.TmuxServerArgus
		u.PaneID = paneID
	}
	if in != nil {
		u.Interaction = in
		u.ReplaceInteraction = true
	}
	if needHydrate {
		if c, ok := d.dial(); ok {
			if s, err := c.getSession(d.ctx, id); err == nil {
				u.Name = s.Title
				u.Cwd = s.Location.Directory
				u.Repo = gittree.RepoName(s.Location.Directory)
				u.Summary = summaryFor(s)
				d.mu.Lock()
				if e, ok := d.presence[id]; ok {
					e.hydrated = true
				}
				d.mu.Unlock()
			}
		}
	}
	d.reg.ApplyHook(u)
}

// refreshKnown never creates sessions (dismissed ids stay gone) and only
// applies on change, to avoid spamming subscribers with EventUpdated.
func (d *discoverer) refreshKnown(sessions []ocSession) {
	for _, s := range sessions {
		if s.ID == "" {
			continue
		}
		d.mu.Lock()
		e, tracked := d.presence[s.ID]
		d.mu.Unlock()
		if !tracked {
			continue
		}
		name := s.Title
		cwd := s.Location.Directory
		repo := gittree.RepoName(cwd)
		sum := summaryFor(s)

		cur, ok := d.reg.Get(registry.AgentSessionKey(Agent, s.ID))
		if !ok {
			continue
		}
		changed := false
		if name != "" && cur.Name != name {
			changed = true
		}
		if cwd != "" && cur.Cwd != cwd {
			changed = true
		}
		if repo != "" && cur.Repo != repo {
			changed = true
		}
		if sum != nil && !summariesEqual(cur.Summary, sum) {
			changed = true
		}
		if !changed {
			continue
		}
		u := registry.HookUpdate{Agent: Agent, AgentSessionID: s.ID}
		if name != "" {
			u.Name = name
		}
		if cwd != "" {
			u.Cwd = cwd
		}
		if repo != "" {
			u.Repo = repo
		}
		if sum != nil {
			u.Summary = sum
		}
		d.reg.ApplyHook(u)
		// A rename implies recent activity; don't let the sweep drop it.
		if updated := time.UnixMilli(s.Time.Updated); !updated.IsZero() {
			d.mu.Lock()
			if e != nil && updated.After(e.lastActivity) {
				e.lastActivity = updated
			}
			d.mu.Unlock()
		}
	}
}

// seedIdle adds idle sessions whose last update falls within the idle TTL as
// AwaitingInput, so a restart surfaces sessions active in the last hour. It skips
// running/archived sessions and any id already tracked, so it never resurrects a
// dismissed session on a later scan (the seeded flag runs it once, at startup).
func (d *discoverer) seedIdle(sessions []ocSession) {
	cutoff := time.Now().Add(-sessionIdleTTL)
	for _, s := range sessions {
		if s.ID == "" || s.Time.Archived != 0 {
			continue
		}
		updated := time.UnixMilli(s.Time.Updated)
		if updated.Before(cutoff) {
			continue
		}
		d.mu.Lock()
		if _, exists := d.presence[s.ID]; exists {
			d.mu.Unlock()
			continue
		}
		d.presence[s.ID] = &presenceEntry{
			lastActivity: updated,
			status:       session.StatusAwaitingInput,
			hydrated:     true,
		}
		d.mu.Unlock()

		d.reg.ApplyHook(registry.HookUpdate{
			Agent:              Agent,
			AgentSessionID:     s.ID,
			Status:             session.StatusAwaitingInput,
			Frontend:           session.FrontendExternal,
			TranscriptPath:     s.ID,
			Input:              session.InputAPI,
			Name:               s.Title,
			Cwd:                s.Location.Directory,
			Repo:               gittree.RepoName(s.Location.Directory),
			Summary:            summaryFor(s),
			Interaction:        &session.Interaction{Kind: session.InteractionIdle},
			ReplaceInteraction: true,
		})
	}
}

func (d *discoverer) sendPrompt(ctx context.Context, sessionID, text string) error {
	c, ok := d.dial()
	if !ok {
		return fmt.Errorf("opencode: service unavailable")
	}
	return c.sendPrompt(ctx, sessionID, text)
}

// startService runs `opencode service start`, which returns once the background
// server is up and is a no-op when it already runs.
var startService = func(ctx context.Context) error {
	cmd := shell.NewCommandContext(ctx, "opencode", "service", "start")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("opencode: start service: %w: %s", err, cmd.StdErr().String())
	}
	return nil
}

func (d *discoverer) awaitClient(ctx context.Context) (*client, error) {
	if c, ok := d.dial(); ok {
		return c, nil
	}
	// Concurrent callers share one start. It runs on its own bounded context so a
	// caller that gives up does not fail the others.
	ch := d.starts.DoChan("start", func() (any, error) {
		if _, ok := d.dial(); ok {
			return nil, nil
		}
		sctx, cancel := context.WithTimeout(context.Background(), startTimeout)
		defer cancel()
		if err := startService(sctx); err != nil {
			return nil, err
		}
		select {
		case d.wake <- struct{}{}:
		default:
		}
		return nil, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := d.dial(); ok {
			return c, nil
		}
		if sleep(ctx, 50*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("opencode: service unavailable")
}

func (d *discoverer) spawnSession(ctx context.Context, cwd string, opts adapter.SpawnOptions) (string, error) {
	var model ocModelRef
	if opts.Model != "" {
		provider, id, ok := strings.Cut(opts.Model, "/")
		if !ok || provider == "" || id == "" {
			return "", fmt.Errorf("opencode: invalid model %q", opts.Model)
		}
		model = ocModelRef{ID: id, ProviderID: provider}
	}
	c, err := d.awaitClient(ctx)
	if err != nil {
		return "", err
	}
	created, err := c.createSession(ctx, cwd, model)
	if err != nil {
		return "", err
	}
	id := created.ID
	if model.ID != "" && (created.Model.ID != model.ID || created.Model.ProviderID != model.ProviderID) {
		if err := c.switchModel(ctx, id, model); err != nil {
			_ = c.deleteSession(ctx, id) // best effort: don't leave a session on the wrong model
			return "", err
		}
	}
	// Register before prompting: the session already exists on the server, and an
	// idle (unprompted) session is not in /api/session/active, so a later scan would
	// not find it. Registering first keeps it tracked even if the prompt fails.
	d.upsert(id, session.StatusWorking, nil)
	if opts.Prompt != "" {
		if err := c.sendPrompt(ctx, id, opts.Prompt); err != nil {
			return "", err
		}
	}
	return id, nil
}

// reconcilePending makes argus's pending-prompt state match the server's. The SSE
// event stream has no backfill, so a form.created or a reply can be missed across a
// pump (re)connect — most commonly when argus starts while a question already sits
// open. For each candidate session it fetches the live forms and permissions and:
//   - renders a prompt the server has but argus missed (backfill), and
//   - drops a prompt argus tracks that the server no longer reports (returns to idle).
//
// Candidates are the server's active sessions (may hold a missed prompt) plus every
// session argus tracks as pending (may have been answered while disconnected). A
// fetch error leaves that session untouched: only a successful list drives a change.
func (d *discoverer) reconcilePending(ctx context.Context, c *client, active map[string]bool) {
	ids := map[string]bool{}
	for id := range active {
		ids[id] = true
	}
	d.mu.Lock()
	for id := range d.pendForm {
		ids[id] = true
	}
	for id := range d.pendPerm {
		ids[id] = true
	}
	d.mu.Unlock()

	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(scanConcurrency)
	for _, id := range list {
		g.Go(func() error {
			select {
			case <-gctx.Done():
				return gctx.Err()
			default:
			}
			d.reconcileSession(gctx, c, id)
			return nil
		})
	}
	_ = g.Wait()
}

func (d *discoverer) reconcileSession(ctx context.Context, c *client, id string) {
	forms, err := c.listForms(ctx, id)
	if err != nil {
		return
	}
	if len(forms) > 0 {
		form := &forms[0]
		d.mu.Lock()
		pf := d.pendForm[id]
		d.mu.Unlock()
		if pf == nil || pf.formID != form.ID {
			d.applyForm(form)
		}
		return
	}
	d.mu.Lock()
	_, hadForm := d.pendForm[id]
	d.mu.Unlock()
	if hadForm {
		d.mu.Lock()
		delete(d.pendForm, id)
		d.mu.Unlock()
		d.upsert(id, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	}

	perms, err := c.listPermissions(ctx, id)
	if err != nil {
		return
	}
	if len(perms) > 0 {
		p := perms[0]
		d.mu.Lock()
		reqID := d.pendPerm[id]
		d.mu.Unlock()
		if reqID != p.ID {
			src := ""
			if p.Source != nil {
				src = p.Source.Type
			}
			d.applyPermission(id, p.ID, p.Action, src)
		}
		return
	}
	d.mu.Lock()
	_, hadPerm := d.pendPerm[id]
	d.mu.Unlock()
	if hadPerm {
		d.mu.Lock()
		delete(d.pendPerm, id)
		d.mu.Unlock()
		d.upsert(id, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	}
}

// hasPendingPrompt reports whether the session waits on a user reply to a form or
// permission prompt.
func (d *discoverer) hasPendingPrompt(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, form := d.pendForm[id]
	_, perm := d.pendPerm[id]
	return form || perm
}

func (d *discoverer) dismiss(id string) { d.remove(id) }

func (d *discoverer) remove(id string) {
	d.mu.Lock()
	delete(d.presence, id)
	delete(d.panes, id)
	delete(d.pendPerm, id)
	delete(d.pendForm, id)
	d.mu.Unlock()
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
}

func (d *discoverer) sweepIdle() {
	cutoff := time.Now().Add(-sessionIdleTTL)
	var stale []string
	d.mu.Lock()
	for id, e := range d.presence {
		// A session with an unanswered permission (awaitingPermission) or question
		// (pendForm) prompt waits on the user, not the agent, and must not age out.
		// Since the ScanOnce guard stops refreshing its lastActivity, without this it
		// would be swept after the idle TTL, and remove() would drop its pendForm.
		if e.awaitingPermission {
			continue
		}
		if _, formPending := d.pendForm[id]; formPending {
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
		d.remove(id)
	}
}
