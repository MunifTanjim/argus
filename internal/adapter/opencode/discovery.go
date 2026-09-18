package opencode

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

type serverClient struct {
	server session.TmuxServer
	client *tmux.Client
}

type discoverer struct {
	reg     *registry.Registry
	servers []serverClient

	dial func() (*client, bool)

	pumpOnce sync.Once
	ctx      context.Context

	mu       sync.Mutex
	pendPerm map[string]string // sessionID -> permissionID (set by SSE, read by Respond)
}

func newDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *discoverer {
	d := &discoverer{reg: reg, ctx: context.Background(), pendPerm: map[string]string{}}
	for server, c := range clients {
		d.servers = append(d.servers, serverClient{server: server, client: c})
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
		d.reg.ReconcileSessions(Agent, nil)
		return nil
	}
	sessions, err := c.listSessions(ctx)
	if err != nil {
		return err
	}

	paneByPath := d.panesByPath(ctx)

	found := make([]registry.DiscoveredSession, 0, len(sessions))
	for _, s := range sessions {
		dir := s.Location.Directory
		ds := registry.DiscoveredSession{
			AgentSessionID: s.ID,
			Cwd:            dir,
			Repo:           repoName(dir),
			TranscriptPath: s.ID,
			Name:           s.Title,
			Frontend:       session.FrontendExternal,
		}
		if pi, ok := paneByPath[dir]; ok {
			ds.HasPane = true
			ds.Server = pi.server
			ds.PaneID = pi.paneID
			ds.SessionName = pi.sessionName
			ds.WindowIndex = pi.windowIndex
			ds.CurrentPath = pi.currentPath
			ds.Frontend = session.FrontendTmux
		}
		found = append(found, ds)
	}
	d.reg.ReconcileSessions(Agent, found)

	d.pumpOnce.Do(func() { go d.runEventPump(d.ctx) })
	return nil
}

type paneInfo struct {
	server      session.TmuxServer
	paneID      string
	sessionName string
	windowIndex int
	currentPath string
}

func (d *discoverer) panesByPath(ctx context.Context) map[string]paneInfo {
	out := map[string]paneInfo{}
	for _, sc := range d.servers {
		panes, err := sc.client.ListPanes(ctx)
		if err != nil {
			continue
		}
		for _, p := range panes {
			if p.CurrentCommand != "opencode" || p.CurrentPath == "" {
				continue
			}
			out[p.CurrentPath] = paneInfo{
				server:      sc.server,
				paneID:      p.PaneID,
				sessionName: p.SessionName,
				windowIndex: p.WindowIndex,
				currentPath: p.CurrentPath,
			}
		}
	}
	return out
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
