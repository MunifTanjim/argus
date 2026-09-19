package opencode

import (
	"context"
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
}

type discoverer struct {
	reg *registry.Registry

	dial func() (*client, bool)

	pumpOnce sync.Once
	ctx      context.Context

	mu       sync.Mutex
	presence map[string]*presenceEntry // opencode session id -> entry
	pendPerm map[string]string         // sessionID -> permissionID (SSE → Respond)
}

// clients is accepted for interface compatibility; the presence model uses no tmux panes.
func newDiscoverer(reg *registry.Registry, _ map[session.TmuxServer]*tmux.Client) *discoverer {
	d := &discoverer{
		reg:      reg,
		ctx:      context.Background(),
		presence: map[string]*presenceEntry{},
		pendPerm: map[string]string{},
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
	d.mu.Unlock()

	u := registry.HookUpdate{
		Agent:          Agent,
		AgentSessionID: id,
		Status:         st,
		Frontend:       session.FrontendExternal,
		TranscriptPath: id,
	}
	if in != nil {
		u.Interaction = in
		u.ReplaceInteraction = true
	}
	if !existed {
		if c, ok := d.dial(); ok {
			if s, err := c.getSession(d.ctx, id); err == nil {
				u.Cwd = s.Location.Directory
				u.Repo = repoName(s.Location.Directory)
			}
		}
	}
	d.reg.ApplyHook(u)
}

func (d *discoverer) remove(id string) {
	d.mu.Lock()
	delete(d.presence, id)
	delete(d.pendPerm, id)
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
