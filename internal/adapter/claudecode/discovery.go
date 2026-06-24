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

// Tool is the adapter's tool identifier, stored on every session it owns.
const Tool = "claude-code"

// runPS returns a snapshot of all processes as "<pid> <tty> <stat> <args...>"
// lines. It is a package var so tests can stub it. Claude Code disguises its
// process name as a version string, so tmux's pane_current_command is unreliable;
// inspecting the real foreground process on each tty is authoritative.
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

// parseForegroundClaude maps each tty whose foreground process group (stat
// contains '+') includes a process whose argv0 basename is "claude" to that
// claude process's pid. The pid lets discovery read ~/.claude/sessions/<pid>.json
// regardless of how claude was launched (the pane's pane_pid is the shell when
// claude was started from a shell, so it can't be trusted).
func parseForegroundClaude(psOut string) map[string]int {
	out := map[string]int{}
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, tty, stat, argv0 := fields[0], fields[1], fields[2], fields[3]
		if !strings.Contains(stat, "+") { // not foreground
			continue
		}
		if filepath.Base(argv0) == "claude" {
			if n, err := strconv.Atoi(pid); err == nil {
				out[tty] = n
			}
		}
	}
	return out
}

// foregroundClaude takes one ps snapshot and returns the tty→claude-pid map.
func foregroundClaude() (map[string]int, error) {
	out, err := runPS()
	if err != nil {
		return nil, err
	}
	return parseForegroundClaude(out), nil
}

// isClaudePane reports whether a pane is running Claude Code: either a direct
// launch (command "claude") or a pane whose tty has claude in the foreground.
func isClaudePane(p tmux.Pane, claudeTTYs map[string]int) bool {
	if p.CurrentCommand == "claude" {
		return true
	}
	_, ok := claudeTTYs[normalizeTTY(p.TTY)]
	return ok
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
	match   func(tmux.Pane) bool

	sumMu    sync.Mutex
	sumCache map[string]summaryEntry // transcript path → last computed summary and status hint
}

// summaryEntry caches a transcript's computed summary and status hint, keyed by
// its mod time so a scan only re-parses when the transcript actually changed.
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

// cachedTranscript returns the summary and transcript-derived live status hint
// for a transcript, recomputing only when the file's mod time changed since the
// last scan. Returns (nil, "") when the path is empty or unreadable.
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

// enrich fills a DiscoveredPane's Claude-side fields from the pane's claude
// process: ~/.claude/sessions/<pid>.json gives the session id, cwd, and name;
// from those we derive the transcript path and a (mtime-cached) summary. claudePID
// is the foreground claude pid (from the ps scan); panePID is a fallback for
// tmux-launched panes. No-ops when no process file is found.
func (d *Discoverer) enrich(dp *registry.DiscoveredPane, claudePID, panePID int) {
	dir := claudeSessionsDir()
	ps, ok := readProcSession(dir, claudePID)
	if !ok {
		ps, ok = readProcSession(dir, panePID)
	}
	if !ok {
		return
	}
	dp.ClaudeSessionID = ps.SessionID
	dp.Name = ps.Name
	dp.Cwd = ps.Cwd
	if ps.Cwd != "" {
		if projDir, err := parser.ProjectDirForPath(ps.Cwd); err == nil {
			dp.TranscriptPath = filepath.Join(projDir, ps.SessionID+".jsonl")
			dp.Summary, dp.StatusHint = d.cachedTranscript(dp.TranscriptPath)
		}
	}
}

// ScanOnce scans every configured server once and reconciles the registry. An
// error from one server does not stop the others; the last error is returned.
// Discovery is on-demand: callers invoke this at startup and on triggers
// (client refresh, hook events, spawn/kill) rather than on a timer.
func (d *Discoverer) ScanOnce(ctx context.Context) error {
	// One ps snapshot per scan identifies claude panes by their real foreground
	// process and yields the claude pid per tty (unless a test matcher is set).
	var claudeTTYs map[string]int
	if d.match == nil {
		claudeTTYs, _ = foregroundClaude()
	}

	var lastErr error
	for _, sc := range d.servers {
		panes, err := sc.client.ListPanes(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		var found []registry.DiscoveredPane
		for _, p := range panes {
			isClaude := isClaudePane(p, claudeTTYs)
			if d.match != nil {
				isClaude = d.match(p)
			}
			if !isClaude {
				continue
			}
			dp := registry.DiscoveredPane{
				Tool:        Tool,
				Server:      sc.server,
				PaneID:      p.PaneID,
				SessionName: p.SessionName,
				WindowIndex: p.WindowIndex,
				CurrentPath: p.CurrentPath,
				Repo:        repoName(p.CurrentPath),
			}
			// Enrich immediately from ~/.claude/sessions/<pid>.json so the card
			// shows real info before any hook fires.
			d.enrich(&dp, claudeTTYs[normalizeTTY(p.TTY)], p.PanePID)
			if dp.Cwd != "" && dp.Repo == "" {
				dp.Repo = repoName(dp.Cwd)
			}
			found = append(found, dp)
		}
		d.reg.ReconcileDiscovered(Tool, sc.server, found)
	}
	return lastErr
}
