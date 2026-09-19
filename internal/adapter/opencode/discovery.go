package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

const sessionIdleTTL = time.Hour

type presenceEntry struct {
	lastActivity       time.Time
	status             session.Status
	awaitingPermission bool
	hydrated           bool
}

type discoverer struct {
	reg *registry.Registry

	dial func() (*client, bool)

	pumpOnce sync.Once
	seedOnce sync.Once
	ctx      context.Context

	mu       sync.Mutex
	presence map[string]*presenceEntry // opencode session id -> entry
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

// clients is accepted for interface compatibility; the presence model uses no tmux panes.
func newDiscoverer(reg *registry.Registry, _ map[session.TmuxServer]*tmux.Client) *discoverer {
	d := &discoverer{
		reg:      reg,
		ctx:      context.Background(),
		presence: map[string]*presenceEntry{},
		pendPerm: map[string]string{},
		pendForm: map[string]*pendingForm{},
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
	c, ok := d.dial()
	if !ok {
		return nil
	}
	active, err := c.listActive(ctx)
	if err == nil {
		for id := range active {
			d.upsert(id, session.StatusWorking, nil)
		}
		d.seedOnce.Do(func() {
			if sessions, lerr := c.listSessions(ctx); lerr == nil {
				d.seedIdle(sessions)
			}
		})
	}
	d.pumpOnce.Do(func() {
		go d.runEventPump(d.ctx)
		go d.ageOutLoop(d.ctx)
	})
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
	d.mu.Unlock()

	u := registry.HookUpdate{
		Agent:          Agent,
		AgentSessionID: id,
		Status:         st,
		Frontend:       session.FrontendExternal,
		TranscriptPath: id,
		CanPrompt:      true,
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
				u.Repo = repoName(s.Location.Directory)
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

// seedIdle adds idle sessions whose last update falls within the idle TTL as
// AwaitingInput, so a restart surfaces sessions active in the last hour. It skips
// running/archived sessions and any id already tracked, so it never resurrects a
// dismissed session on a later scan (guarded by seedOnce to run at startup only).
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
			CanPrompt:          true,
			Name:               s.Title,
			Cwd:                s.Location.Directory,
			Repo:               repoName(s.Location.Directory),
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

func (d *discoverer) dismiss(id string) { d.remove(id) }

func (d *discoverer) remove(id string) {
	d.mu.Lock()
	delete(d.presence, id)
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
		if e.awaitingPermission {
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

func (d *discoverer) ageOutLoop(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.sweepIdle()
		}
	}
}

// repoName returns the git repo basename for dir, falling back to dir's basename.
// Mirrors the same helper in internal/adapter/codex.
func repoName(dir string) string {
	for d := dir; d != ""; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return filepath.Base(d)
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}
