// Package codex is the argus adapter for OpenAI Codex CLI. It discovers Codex
// sessions running in tmux, ingests Codex hook events, and drives input and
// lifecycle. Parsing Codex's rollout-*.jsonl transcript is not yet implemented;
// transcript and history reads return empty, so the live pane screen is the
// read path for this first cut.
package codex

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// Tool is the adapter's tool identifier, stored on every session it owns.
const Tool = "codex"

// runPS snapshots all processes as "<pid> <tty> <stat> <args...>" lines. A
// package var so tests can stub it.
//
// NOTE (validate against a real Codex): the matcher below assumes the Codex CLI's
// argv0 basename is "codex". Confirm with `ps -ax -o pid=,args=` on a live
// session and adjust codexProc match if the binary reports a different name.
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

// codexProc reports whether a ps argv0 is the Codex CLI.
func codexProc(argv0 string) bool {
	return filepath.Base(argv0) == "codex"
}

// parseCodexProcs maps each codex pid to its tty. A ttyless process (ps shows
// "??") maps to "". Keys are the liveness set; values drive pid→tty→pane
// correlation.
func parseCodexProcs(psOut string) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, tty, argv0 := fields[0], fields[1], fields[3]
		if !codexProc(argv0) {
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

// codexProcs takes one ps snapshot and returns the codex pid→tty map.
func codexProcs() (map[int]string, error) {
	out, err := runPS()
	if err != nil {
		return nil, err
	}
	return parseCodexProcs(out), nil
}

// paneInfo is a tmux pane located for tty correlation.
type paneInfo struct {
	server      session.TmuxServer
	paneID      string
	sessionName string
	windowIndex int
	currentPath string
}

// serverClient pairs a logical tmux server with its CLI client.
type serverClient struct {
	server session.TmuxServer
	client *tmux.Client
}

// Discoverer scans one or more tmux servers for Codex panes and reconciles them
// into the registry. Unlike Claude Code, Codex writes no per-PID session file,
// so discovery reports only structural pane info; session id, cwd, and
// transcript path arrive later via hook events.
type Discoverer struct {
	reg     *registry.Registry
	servers []serverClient
}

// NewDiscoverer builds a Discoverer. clients maps each logical server to its
// tmux client (e.g. default → tmux.New(""), argus → tmux.New("argus")).
func NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *Discoverer {
	d := &Discoverer{reg: reg}
	for server, client := range clients {
		d.servers = append(d.servers, serverClient{server: server, client: client})
	}
	return d
}

// buildDiscovered correlates live codex procs to tmux panes. Only pane-bound
// procs are reported (v1 controls tmux sessions only); a ttyless codex proc is
// surfaced later by its hook events, keyed on session id.
func buildDiscovered(procs map[int]string, paneByTTY map[string]paneInfo) []registry.DiscoveredSession {
	var out []registry.DiscoveredSession
	for _, tty := range procs {
		if tty == "" {
			continue
		}
		pi, ok := paneByTTY[normalizeTTY(tty)]
		if !ok {
			continue
		}
		out = append(out, registry.DiscoveredSession{
			HasPane:     true,
			Server:      pi.server,
			PaneID:      pi.paneID,
			SessionName: pi.sessionName,
			WindowIndex: pi.windowIndex,
			CurrentPath: pi.currentPath,
			Cwd:         pi.currentPath,
			Repo:        repoName(pi.currentPath),
			Frontend:    session.FrontendTmux,
		})
	}
	return out
}

// ScanOnce scans every configured server once and reconciles the registry. One
// server's error doesn't stop the others; the last error is returned.
func (d *Discoverer) ScanOnce(ctx context.Context) error {
	// ps is the liveness oracle: a ps failure skips the whole reconcile so we
	// never prune on a failed probe.
	procs, err := codexProcs()
	if err != nil {
		return err
	}

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

	d.reg.ReconcileSessions(Tool, buildDiscovered(procs, paneByTTY))
	return lastErr
}

// repoName returns a display name for dir: the basename of the nearest ancestor
// holding a ".git" entry, else the basename of dir itself. Returns "" only when
// dir is empty.
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

// SetRunPSForTest swaps the ps command and returns a restore func.
func SetRunPSForTest(fn func() (string, error)) (restore func()) {
	prev := runPS
	runPS = fn
	return func() { runPS = prev }
}
