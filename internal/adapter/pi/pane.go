package pi

import (
	"context"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// matchPane finds the pane whose tty is tty, the way Claude discovery binds a
// process to its pane.
func matchPane(tty string, panes map[session.TmuxServer][]tmux.Pane) (session.TmuxServer, string) {
	tty = normalizeTTY(tty)
	if tty == "" {
		return "", ""
	}
	for server, list := range panes {
		for _, p := range list {
			if !p.Dead && strings.TrimPrefix(p.TTY, "/dev/") == tty {
				return server, p.PaneID
			}
		}
	}
	return "", ""
}

// normalizeTTY returns tty without /dev/, or "" for a process without one.
func normalizeTTY(tty string) string {
	tty = strings.TrimPrefix(strings.TrimSpace(tty), "/dev/")
	if tty == "?" || tty == "??" {
		return ""
	}
	return tty
}

// paneLookup locates the watched tmux pane a pi process runs in.
func paneLookup(clients map[session.TmuxServer]*tmux.Client) func(context.Context, int) (session.TmuxServer, string) {
	return func(ctx context.Context, pid int) (session.TmuxServer, string) {
		if pid <= 0 || len(clients) == 0 {
			return "", ""
		}
		cmd := shell.NewCommandContext(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(pid))
		if cmd.Run() != nil {
			return "", ""
		}
		tty := normalizeTTY(cmd.StdOut().String())
		if tty == "" {
			return "", ""
		}
		panes := map[session.TmuxServer][]tmux.Pane{}
		for server, c := range clients {
			if list, err := c.ListPanes(ctx); err == nil {
				panes[server] = list
			}
		}
		return matchPane(tty, panes)
	}
}
