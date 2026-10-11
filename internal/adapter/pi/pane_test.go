package pi

import (
	"context"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func TestMatchPaneByTTY(t *testing.T) {
	panes := map[session.TmuxServer][]tmux.Pane{
		session.TmuxServerDefault: {{PaneID: "%1", TTY: "/dev/ttys001"}},
		session.TmuxServerArgus:   {{PaneID: "%7", TTY: "/dev/ttys003"}, {PaneID: "%8", TTY: "/dev/ttys004", Dead: true}},
	}
	for _, tc := range []struct {
		tty        string
		wantServer session.TmuxServer
		wantPane   string
	}{
		{"ttys003", session.TmuxServerArgus, "%7"},
		{"/dev/ttys001", session.TmuxServerDefault, "%1"},
		{"ttys009", "", ""},
		{"ttys004", "", ""},
		{"", "", ""},
		{"??", "", ""},
	} {
		server, pane := matchPane(tc.tty, panes)
		if server != tc.wantServer || pane != tc.wantPane {
			t.Errorf("matchPane(%q) = (%q, %q), want (%q, %q)", tc.tty, server, pane, tc.wantServer, tc.wantPane)
		}
	}
}

func TestPaneLookupSkipsWithoutPidOrClients(t *testing.T) {
	ctx := context.Background()
	if server, pane := paneLookup(nil)(ctx, 42); server != "" || pane != "" {
		t.Fatalf("nil clients = (%q, %q)", server, pane)
	}
	clients := map[session.TmuxServer]*tmux.Client{session.TmuxServerArgus: tmux.New("argus-never-started")}
	if server, pane := paneLookup(clients)(ctx, 0); server != "" || pane != "" {
		t.Fatalf("pid 0 = (%q, %q)", server, pane)
	}
}
