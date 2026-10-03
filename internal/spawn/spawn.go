// Package spawn holds helpers shared between the sessions.spawn RPC (node) and
// the `argus spawn` CLI command, so both derive identical tmux session names.
package spawn

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/tmux"
)

// TerminalSession is the tmux session that holds a node's persistent terminals.
const TerminalSession = "terminal"

// SessionName returns a unique tmux session name for cwd on client's server. The
// base is cwd's folder name; collisions with existing sessions or
// TerminalSession get a -2, -3, … suffix. A failure to list panes is tolerated:
// any real collision surfaces later as a tmux new-session error.
func SessionName(ctx context.Context, client *tmux.Client, cwd string) string {
	return freeName(ctx, client, defaultSessionName(cwd))
}

// AvoidReserved returns name, or a suffixed free name when name is
// TerminalSession.
func AvoidReserved(ctx context.Context, client *tmux.Client, name string) string {
	if name != TerminalSession {
		return name
	}
	return freeName(ctx, client, name)
}

func freeName(ctx context.Context, client *tmux.Client, base string) string {
	taken := map[string]bool{TerminalSession: true}
	if panes, err := client.ListPanes(ctx); err == nil {
		for _, p := range panes {
			taken[p.SessionName] = true
		}
	}
	return uniqueName(base, taken)
}

// defaultSessionName derives a tmux session name from cwd's base (e.g. the repo
// folder), falling back to "claude" for empty/root paths.
func defaultSessionName(cwd string) string {
	base := filepath.Base(strings.TrimSpace(cwd))
	switch base {
	case "", ".", string(filepath.Separator):
		return "claude"
	}
	return base
}

// uniqueName returns base, or base-2, base-3, … to avoid collisions with taken.
func uniqueName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !taken[cand] {
			return cand
		}
	}
}
