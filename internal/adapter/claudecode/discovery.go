// Package claudecode is the argus adapter for Claude Code. It discovers Claude
// sessions in tmux, ingests hook events, reads transcripts, and drives
// input/lifecycle.
package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// Agent is the adapter's coding-agent identifier, stored on every session it owns.
const Agent = "claude"

// runPS snapshots all processes as "<pid> <tty> <stat> <args...>" lines. A
// package var so tests can stub it. Claude Code disguises its process name as a
// version string, so tmux's pane_current_command is unreliable; the real
// foreground process per tty is authoritative.
var runPS = func() (string, error) {
	cmd := shell.NewCommand("ps", "-ax", "-o", "pid=", "-o", "tty=", "-o", "stat=", "-o", "args=")
	err := cmd.Run()
	return cmd.StdOut().String(), err
}

// normalizeTTY strips a leading "/dev/" so tmux's "/dev/ttys002" matches ps's
// "ttys002".
func normalizeTTY(tty string) string {
	return strings.TrimPrefix(tty, "/dev/")
}

// parseClaudeProcs maps each claude pid (argv0 basename "claude") to its tty.
// A ttyless process (ps shows "??") maps to "". Keys are the liveness set;
// values drive pid→tty→pane correlation.
func parseClaudeProcs(psOut string) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, tty, argv0 := fields[0], fields[1], fields[3]
		if filepath.Base(argv0) != "claude" {
			continue
		}
		n, err := strconv.Atoi(pid)
		if err != nil {
			continue
		}
		if tty == "??" || tty == "?" {
			tty = ""
		}
		out[n] = tty
	}
	return out
}

// claudeProcs takes one ps snapshot and returns the claude pid→tty map.
func claudeProcs() (map[int]string, error) {
	out, err := runPS()
	if err != nil {
		return nil, err
	}
	return parseClaudeProcs(out), nil
}

// paneInfo is a tmux pane located for tty correlation.
type paneInfo struct {
	server      session.TmuxServer
	paneID      string
	sessionName string
	windowIndex int
	currentPath string
}

// buildDiscovered correlates live proc-sessions to tmux panes and produces the
// reconcile input. A pid absent from procs is not alive and is skipped. Sets
// structural + identity fields only — ScanOnce fills Summary/StatusHint.
func buildDiscovered(procs map[int]string, paneByTTY map[string]paneInfo, entries []procSession) []registry.DiscoveredSession {
	var out []registry.DiscoveredSession
	for _, ps := range entries {
		tty, alive := procs[ps.PID]
		if !alive {
			continue
		}
		d := registry.DiscoveredSession{
			AgentSessionID: ps.SessionID,
			Name:           ps.Name,
			Cwd:            ps.Cwd,
			Repo:           repoName(ps.Cwd),
		}
		if tty != "" {
			if pi, ok := paneByTTY[normalizeTTY(tty)]; ok {
				d.HasPane = true
				d.Server = pi.server
				d.PaneID = pi.paneID
				d.SessionName = pi.sessionName
				d.WindowIndex = pi.windowIndex
				d.CurrentPath = pi.currentPath
				if pi.currentPath != "" {
					d.Repo = repoName(pi.currentPath)
				}
			}
		}
		if d.HasPane {
			d.Frontend = session.FrontendTmux
		} else {
			d.Frontend = frontendFor(ps.Entrypoint, false)
		}
		if ps.Cwd != "" {
			if projDir, err := parser.ProjectDirForPath(ps.Cwd); err == nil {
				d.TranscriptPath = filepath.Join(projDir, ps.SessionID+".jsonl")
			}
		}
		out = append(out, d)
	}
	return out
}

// serverClient pairs a logical tmux server with its CLI client.
type serverClient struct {
	server session.TmuxServer
	client *tmux.Client
}

// Discoverer scans one or more tmux servers for Claude panes and reconciles
// them into the registry.
type Discoverer struct {
	reg     *registry.Registry
	servers []serverClient

	sumMu    sync.Mutex
	sumCache map[string]summaryEntry // transcript path → cached summary + status hint
}

// summaryEntry caches a transcript's summary and status hint; modTime gates
// re-parsing so a scan only re-reads a changed transcript.
type summaryEntry struct {
	modTime    time.Time
	summary    *session.Summary
	statusHint session.Status
}

// NewDiscoverer builds a Discoverer. clients maps each logical server to its
// tmux client (e.g. default → tmux.New(""), argus → tmux.New("argus")).
func NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *Discoverer {
	d := &Discoverer{reg: reg, sumCache: map[string]summaryEntry{}}
	for server, client := range clients {
		d.servers = append(d.servers, serverClient{server: server, client: client})
	}
	return d
}

// cachedTranscript returns a transcript's summary and live status hint,
// recomputing only when its mod time changed. Returns (nil, "") when the path
// is empty or unreadable.
func (d *Discoverer) cachedTranscript(path string) (*session.Summary, session.Status) {
	if path == "" {
		return nil, ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, ""
	}
	mt := info.ModTime()

	d.sumMu.Lock()
	defer d.sumMu.Unlock()
	if e, ok := d.sumCache[path]; ok && e.modTime.Equal(mt) {
		return e.summary, e.statusHint
	}
	sum := summarize(path)
	hint := classifyLiveStatus(path)
	d.sumCache[path] = summaryEntry{modTime: mt, summary: sum, statusHint: hint}
	return sum, hint
}

// ScanOnce scans every configured server once and reconciles the registry. One
// server's error doesn't stop the others; the last error is returned. Discovery
// is on-demand (startup + triggers), not timed.
func (d *Discoverer) ScanOnce(ctx context.Context) error {
	// ps is the liveness oracle: a ps failure skips the whole reconcile so we
	// never prune on a failed probe.
	procs, err := claudeProcs()
	if err != nil {
		return err
	}

	// tmux is consulted only to attach panes by tty correlation.
	paneByTTY := map[string]paneInfo{}
	var lastErr error
	for _, sc := range d.servers {
		panes, err := sc.client.ListPanes(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		for _, p := range panes {
			paneByTTY[normalizeTTY(p.TTY)] = paneInfo{
				server:      sc.server,
				paneID:      p.PaneID,
				sessionName: p.SessionName,
				windowIndex: p.WindowIndex,
				currentPath: p.CurrentPath,
			}
		}
	}

	found := buildDiscovered(procs, paneByTTY, listProcSessions(claudeSessionsDir()))
	for i := range found {
		found[i].Summary, found[i].StatusHint = d.cachedTranscript(found[i].TranscriptPath)
	}
	d.reg.ReconcileSessions(Agent, found)
	return lastErr
}

// Test seams (used by other packages' tests). Not for production use.

// NormalizeTTYForTest exposes normalizeTTY to external test packages.
func NormalizeTTYForTest(tty string) string { return normalizeTTY(tty) }

// SetSessionsDirForTest overrides the proc-session dir ("" restores default).
func SetSessionsDirForTest(dir string) { claudeSessionsDirOverride = dir }

// SetRunPSForTest swaps the ps command and returns a restore func.
func SetRunPSForTest(fn func() (string, error)) (restore func()) {
	prev := runPS
	runPS = fn
	return func() { runPS = prev }
}
