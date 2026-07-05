package antigravity

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

// Discoverer scans for live `agy` processes and reconciles them into the registry.
type Discoverer struct {
	reg     *registry.Registry
	servers []serverClient
}

func NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *Discoverer {
	d := &Discoverer{reg: reg}
	for server, client := range clients {
		d.servers = append(d.servers, serverClient{server: server, client: client})
	}
	return d
}

type agyProc struct {
	tty            string
	conversationID string
	cwd            string
	transcriptPath string
	summary        *session.Summary
}

// ScanOnce runs one discovery pass. A failed ps aborts (never prune on a bad
// probe); tmux failures degrade to paneless binding.
func (d *Discoverer) ScanOnce(ctx context.Context) error {
	procs, ok := listAgyProcs()
	if !ok {
		return nil
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

	d.reg.ReconcileSessions(Agent, buildDiscovered(procs, paneByTTY))
	return lastErr
}

// buildDiscovered maps live processes to reconcile input. Pure: no IO.
func buildDiscovered(procs []agyProc, paneByTTY map[string]paneInfo) []registry.DiscoveredSession {
	out := make([]registry.DiscoveredSession, 0, len(procs))
	for _, pr := range procs {
		ds := registry.DiscoveredSession{
			AgentSessionID: pr.conversationID,
			Cwd:            pr.cwd,
			Repo:           repoName(pr.cwd),
			Frontend:       session.FrontendExternal,
			TranscriptPath: pr.transcriptPath,
			Summary:        pr.summary,
		}
		if pi, ok := paneByTTY[normalizeTTY(pr.tty)]; ok {
			ds.HasPane = true
			ds.Server = pi.server
			ds.PaneID = pi.paneID
			ds.SessionName = pi.sessionName
			ds.WindowIndex = pi.windowIndex
			ds.CurrentPath = pi.currentPath
			ds.Frontend = session.FrontendTmux
			if pi.currentPath != "" {
				ds.Cwd = pi.currentPath
				ds.Repo = repoName(pi.currentPath)
			}
		}
		out = append(out, ds)
	}
	return out
}

// normalizeTTY strips a leading "/dev/" so tmux's "/dev/ttys004" matches ps's "ttys004".
func normalizeTTY(tty string) string { return strings.TrimPrefix(tty, "/dev/") }

// listAgyProcs finds live agy processes and resolves conversation ids via argv/lsof.
// ok is false only when the ps probe itself fails.
func listAgyProcs() ([]agyProc, bool) {
	cmd := shell.NewCommand("ps", "-ax", "-o", "pid=", "-o", "tty=", "-o", "command=")
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	var out []agyProc
	for _, line := range strings.Split(string(cmd.StdOut()), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		tty := fields[1]
		command := strings.Join(fields[2:], " ")
		if !isAgyCommand(command) {
			continue
		}
		if tty == "??" || tty == "?" {
			tty = ""
		}
		pr := agyProc{tty: tty, conversationID: conversationIDFromArgv(command)}
		if pr.conversationID == "" {
			pr.conversationID = conversationIDFromLsof(pid)
		}
		pr.cwd = cwdFromLsof(pid)
		pr.transcriptPath = transcriptPathFor(pr.conversationID)
		pr.summary = buildSummary(pr.conversationID, pr.transcriptPath, "")
		out = append(out, pr)
	}
	return out, true
}

func isAgyCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return filepath.Base(fields[0]) == "agy"
}

func conversationIDFromArgv(argv string) string {
	for _, f := range strings.Fields(argv) {
		if v, ok := strings.CutPrefix(f, "--conversation="); ok {
			return v
		}
	}
	return ""
}

// conversationIDFromLsof resolves a process's conversation id from its open brain/<id> directory handle.
func conversationIDFromLsof(pid int) string {
	dir, err := brainDir()
	if err != nil {
		return ""
	}
	cmd := shell.NewCommand("lsof", "-a", "-p", strconv.Itoa(pid), "-Fn")
	if err := cmd.Run(); err != nil {
		return ""
	}
	prefix := dir + string(os.PathSeparator)
	for _, line := range strings.Split(string(cmd.StdOut()), "\n") {
		path, ok := strings.CutPrefix(line, "n")
		if !ok {
			continue
		}
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		if id, _, _ := strings.Cut(rest, string(os.PathSeparator)); id != "" {
			return id
		}
	}
	return ""
}

func cwdFromLsof(pid int) string {
	cmd := shell.NewCommand("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn")
	if err := cmd.Run(); err != nil {
		return ""
	}
	for _, line := range strings.Split(string(cmd.StdOut()), "\n") {
		if path, ok := strings.CutPrefix(line, "n"); ok {
			return path
		}
	}
	return ""
}

// repoName returns the basename of the nearest ancestor holding a ".git" entry,
// else the basename of dir.
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
