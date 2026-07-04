// Package codex is the argus adapter for OpenAI Codex CLI.
package codex

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

const Agent = "codex"

type paneInfo struct {
	server      session.TmuxServer
	paneID      string
	sessionName string
	windowIndex int
	currentPath string
}

type serverClient struct {
	server session.TmuxServer
	client *tmux.Client
}

type Discoverer struct {
	reg     *registry.Registry
	servers []serverClient

	mu          sync.Mutex
	modelNames  map[string]string // slug → display name, from models_cache.json
	modelsMtime time.Time         // re-parse only when the cache file changes
}

func NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *Discoverer {
	d := &Discoverer{reg: reg}
	for server, client := range clients {
		d.servers = append(d.servers, serverClient{server: server, client: client})
	}
	return d
}

// Pane IDs are only unique per server.
func paneKey(server session.TmuxServer, paneID string) string {
	return string(server) + ":" + paneID
}

func (d *Discoverer) ScanOnce(ctx context.Context) error {
	snaps, err := listSnapshots()
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.threadID)
	}
	meta, _ := loadThreadMeta(ids) // enrichment only; nil/empty on failure

	paneByKey := map[string]paneInfo{}
	paneByTTY := map[string]paneInfo{}
	var lastErr error
	for _, sc := range d.servers {
		panes, err := sc.client.ListPanes(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		for _, p := range panes {
			pi := paneInfo{
				server:      sc.server,
				paneID:      p.PaneID,
				sessionName: p.SessionName,
				windowIndex: p.WindowIndex,
				currentPath: p.CurrentPath,
			}
			paneByKey[paneKey(sc.server, p.PaneID)] = pi
			paneByTTY[normalizeTTY(p.TTY)] = pi
		}
	}

	pidByThread := map[string]int{}
	procs, psOK := pidTTYs()
	if psOK {
		for _, s := range snaps {
			if pid, ok := lastProcessPID(s.threadID); ok {
				pidByThread[s.threadID] = pid
			}
		}
	}
	live, fallback := classifyLiveness(snaps, pidByThread, procs, paneByTTY)

	d.reg.ReconcileSessions(Agent, buildDiscovered(live, meta, paneByKey, fallback, d.cachedModelNames()))
	return lastErr
}

func (d *Discoverer) cachedModelNames() map[string]string {
	path, err := modelsCachePath()
	if err != nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	mt := info.ModTime()

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.modelNames != nil && d.modelsMtime.Equal(mt) {
		return d.modelNames
	}
	d.modelNames = loadModelNames()
	d.modelsMtime = mt
	return d.modelNames
}

func classifyLiveness(snaps []snapshot, pidByThread map[string]int, procs map[int]string, paneByTTY map[string]paneInfo) (live []snapshot, fallback map[string]paneInfo) {
	fallback = map[string]paneInfo{}
	for _, s := range snaps {
		if pid, known := pidByThread[s.threadID]; known {
			tty, alive := procs[pid]
			if !alive {
				continue // process gone: crash-stale snapshot
			}
			if tty != "" {
				if pi, ok := paneByTTY[normalizeTTY(tty)]; ok {
					fallback[s.threadID] = pi
				}
			}
		}
		live = append(live, s)
	}
	return live, fallback
}

func normalizeTTY(tty string) string { return strings.TrimPrefix(tty, "/dev/") }

func pidTTYs() (map[int]string, bool) {
	cmd := shell.NewCommand("ps", "-ax", "-o", "pid=", "-o", "tty=")
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	out := map[int]string{}
	for _, line := range strings.Split(string(cmd.StdOut()), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if tty := fields[1]; tty != "??" && tty != "?" {
			out[pid] = tty
		} else {
			out[pid] = ""
		}
	}
	return out, true
}

func buildDiscovered(snaps []snapshot, meta map[string]threadMeta, paneByKey, fallbackPane map[string]paneInfo, modelNames map[string]string) []registry.DiscoveredSession {
	var out []registry.DiscoveredSession
	for _, s := range snaps {
		m := meta[s.threadID]

		transcript := m.rolloutPath
		if transcript == "" {
			transcript = findRolloutPath(s.threadID)
		}

		d := registry.DiscoveredSession{
			AgentSessionID: s.threadID,
			TranscriptPath: transcript,
			Cwd:            m.cwd,
			Repo:           repoName(m.cwd),
			Summary:        summaryFor(m, modelNames),
		}

		if pi, ok := snapshotPane(s, paneByKey); ok {
			bindPane(&d, pi)
		} else if pi, ok := fallbackPane[s.threadID]; ok {
			bindPane(&d, pi)
		} else {
			d.Frontend = session.FrontendExternal
		}

		out = append(out, d)
	}
	return out
}

func snapshotPane(s snapshot, paneByKey map[string]paneInfo) (paneInfo, bool) {
	if s.paneID == "" {
		return paneInfo{}, false
	}
	pi, ok := paneByKey[paneKey(serverFromSocket(s.socketPath), s.paneID)]
	return pi, ok
}

func bindPane(d *registry.DiscoveredSession, pi paneInfo) {
	d.HasPane = true
	d.Server = pi.server
	d.PaneID = pi.paneID
	d.SessionName = pi.sessionName
	d.WindowIndex = pi.windowIndex
	d.CurrentPath = pi.currentPath
	d.Frontend = session.FrontendTmux
	if pi.currentPath != "" {
		d.Cwd = pi.currentPath
		d.Repo = repoName(pi.currentPath)
	}
}

// Returns nil when all metadata fields are empty.
func summaryFor(m threadMeta, modelNames map[string]string) *session.Summary {
	model := m.model
	if dn := modelNames[m.model]; dn != "" {
		model = dn
	}
	if model == "" && m.title == "" && m.tokens == 0 {
		return nil
	}
	return &session.Summary{ModelName: model, ModelColor: modelColorFor(model), Tokens: m.tokens, Task: m.title}
}

func findRolloutPath(threadID string) string {
	dir, err := codexHome()
	if err != nil {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(dir, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// Falls back to dir's basename when no .git ancestor is found.
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
