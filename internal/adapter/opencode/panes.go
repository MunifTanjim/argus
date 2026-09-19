package opencode

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
)

// runPS snapshots processes with their tty and full args, so a spawned
// `opencode --session <id>` pane can be located by its command line.
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

// parseOpencodeProcs maps each `opencode ... --session <id>` process to its tty.
// It recognizes the id after "--session"/"-s" or in "--session=<id>", and only
// for a command line that names opencode, so an unrelated "--session" is ignored.
func parseOpencodeProcs(psOut string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		tty := fields[1]
		if tty == "??" || tty == "?" {
			tty = ""
		}
		args := fields[3:]
		isOpencode := false
		sessionID := ""
		for i, a := range args {
			if strings.Contains(filepath.Base(a), "opencode") {
				isOpencode = true
			}
			switch {
			case (a == "--session" || a == "-s") && i+1 < len(args):
				sessionID = args[i+1]
			case strings.HasPrefix(a, "--session="):
				sessionID = strings.TrimPrefix(a, "--session=")
			}
		}
		if isOpencode && sessionID != "" && tty != "" {
			out[sessionID] = tty
		}
	}
	return out
}

// scanPanes returns the argus-server panes running `opencode --session <id>`,
// keyed by opencode session id. ok is false when the scan could not run (no
// tmux client or a ps/tmux error), so callers do not treat it as "no panes" and
// detach live sessions.
func (d *discoverer) scanPanes(ctx context.Context) (bound map[string]string, ok bool) {
	if d.argusTmux == nil {
		return nil, false
	}
	psOut, err := runPS()
	if err != nil {
		return nil, false
	}
	procs := parseOpencodeProcs(psOut)
	panes, err := d.argusTmux.ListPanes(ctx)
	if err != nil {
		return nil, false
	}
	paneByTTY := map[string]string{}
	for _, p := range panes {
		paneByTTY[normalizeTTY(p.TTY)] = p.PaneID
	}
	bound = map[string]string{}
	for id, tty := range procs {
		if paneID, ok := paneByTTY[normalizeTTY(tty)]; ok {
			bound[id] = paneID
		}
	}
	return bound, true
}

// reconcilePanes adopts each bound pane onto its session (creating a paneless
// entry first for a pane whose session is not yet tracked) and detaches any
// previously-adopted pane that has disappeared, reverting that session to
// paneless. active marks sessions currently running, so a freshly-tracked
// pane-backed session gets the right status.
func (d *discoverer) reconcilePanes(ctx context.Context, bound map[string]string, active map[string]bool) {
	for id, paneID := range bound {
		d.mu.Lock()
		prev := d.panes[id]
		d.panes[id] = paneID
		_, known := d.presence[id]
		d.mu.Unlock()

		if known {
			if prev != paneID {
				d.applyPane(id, paneID)
			}
			continue
		}
		st := session.StatusAwaitingInput
		in := &session.Interaction{Kind: session.InteractionIdle}
		if active[id] {
			st, in = session.StatusWorking, nil
		}
		d.upsert(id, st, in)
	}

	d.mu.Lock()
	var gone []string
	for id := range d.panes {
		if _, ok := bound[id]; !ok {
			gone = append(gone, id)
		}
	}
	for _, id := range gone {
		delete(d.panes, id)
	}
	d.mu.Unlock()
	for _, id := range gone {
		d.reg.ClearPane(id)
	}
}

// applyPane adopts paneID onto an already-tracked session without touching its
// status or interaction (an empty Status is a no-op in ApplyHook).
func (d *discoverer) applyPane(id, paneID string) {
	d.reg.ApplyHook(registry.HookUpdate{
		Agent:          Agent,
		AgentSessionID: id,
		Server:         session.TmuxServerArgus,
		PaneID:         paneID,
		CanPrompt:      true,
	})
}
