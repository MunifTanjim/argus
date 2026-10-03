package codex

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
)

var runPS = func() (string, error) {
	cmd := shell.NewCommand("ps", "-ax", "-o", "pid=", "-o", "tty=", "-o", "stat=", "-o", "args=")
	err := cmd.Run()
	return cmd.StdOut().String(), err
}

// parseCodexResumeProcs maps each `codex resume <thread-id>` process on a tty to
// that tty. A plain `codex` launch names no thread, so it cannot be bound.
func parseCodexResumeProcs(psOut string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		tty := fields[1]
		if tty == "?" || tty == "??" {
			continue
		}
		args := fields[3:]
		if filepath.Base(args[0]) != "codex" || args[1] != "resume" {
			continue
		}
		for _, a := range args[2:] {
			if !strings.HasPrefix(a, "-") {
				out[a] = tty
				break
			}
		}
	}
	return out
}

// scanPanes finds panes on every known tmux server that run `codex resume <id>`.
// ok is false when the scan could not run (ps failure or any server's ListPanes error),
// so callers keep existing panes.
func (d *discoverer) scanPanes(ctx context.Context) (map[string]paneRef, bool) {
	if len(d.servers) == 0 {
		return nil, false
	}
	psOut, err := runPS()
	if err != nil {
		return nil, false
	}
	procs := parseCodexResumeProcs(psOut)
	if len(procs) == 0 {
		return map[string]paneRef{}, true
	}
	byTTY := map[string]paneRef{}
	for _, sc := range d.servers {
		panes, err := d.listPanes(ctx, sc)
		if err != nil {
			return nil, false
		}
		for _, p := range panes {
			byTTY[normalizeTTY(p.TTY)] = paneRef{server: sc.server, paneID: p.PaneID}
		}
	}
	bound := map[string]paneRef{}
	for id, tty := range procs {
		if ref, ok := byTTY[normalizeTTY(tty)]; ok {
			bound[id] = ref
		}
	}
	return bound, true
}

// reconcilePanes adopts bound panes onto tracked sessions and detaches panes that
// disappeared. Untracked threads are skipped: opening a viewer loads the thread,
// and the daemon then reports it. All registry calls happen while holding d.mu
// so a concurrent forget cannot interleave; the registry never calls back into
// the discoverer.
func (d *discoverer) reconcilePanes(bound map[string]paneRef) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for id, ref := range bound {
		if _, tracked := d.threads[id]; !tracked {
			continue
		}
		if d.panes[id] == ref {
			continue
		}
		if old := d.panes[id]; old != (paneRef{}) {
			d.reg.ClearPane(id)
		}
		d.panes[id] = ref
		d.reg.ApplyHook(registry.HookUpdate{
			Agent: Agent, AgentSessionID: id, Server: ref.server, PaneID: ref.paneID, Input: session.InputAPI,
		})
	}

	for id := range d.panes {
		if _, ok := bound[id]; !ok {
			delete(d.panes, id)
			if e := d.threads[id]; e != nil {
				e.lastActivity = time.Now()
			}
			d.reg.ClearPane(id)
		}
	}
}
