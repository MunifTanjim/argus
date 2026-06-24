package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

const (
	defaultTmuxEnv = "/tmp/tmux-1000/default,1234,0"
	argusTmuxEnv   = "/tmp/tmux-1000/argus,1234,0"
)

// planJump gates the `O` jump: it must refuse anything that isn't a same-machine
// default-server session reachable from a default-server tmux client, and return
// the pane id otherwise.
func TestPlanJump(t *testing.T) {
	defaultLocal := session.Session{ // same machine (no gateway), default server
		Tmux: session.TmuxLocation{Server: session.TmuxServerDefault, PaneID: "%3"},
	}
	cases := []struct {
		name       string
		s          session.Session
		hostname   string
		tmuxEnv    string
		wantPane   string
		wantReason string // substring; empty = expect a successful plan
	}{
		{
			name:       "not inside tmux",
			s:          defaultLocal,
			tmuxEnv:    "",
			wantReason: "inside tmux",
		},
		{
			name:       "tui on non-default server",
			s:          defaultLocal,
			tmuxEnv:    argusTmuxEnv,
			wantReason: "default tmux server",
		},
		{
			name: "session on argus private socket",
			s: session.Session{
				Tmux: session.TmuxLocation{Server: session.TmuxServerArgus, PaneID: "%9"},
			},
			tmuxEnv:    defaultTmuxEnv,
			wantReason: "private socket",
		},
		{
			name: "session on another machine",
			s: session.Session{
				NodeID:    "box-2",
				NodeLabel: "box-2",
				Tmux:      session.TmuxLocation{Server: session.TmuxServerDefault, PaneID: "%4"},
			},
			hostname:   "box-1",
			tmuxEnv:    defaultTmuxEnv,
			wantReason: "box-2",
		},
		{
			name: "no pane id",
			s: session.Session{
				Tmux: session.TmuxLocation{Server: session.TmuxServerDefault},
			},
			tmuxEnv:    defaultTmuxEnv,
			wantReason: "no tmux pane",
		},
		{
			name:     "local default-server session jumps",
			s:        defaultLocal,
			tmuxEnv:  defaultTmuxEnv,
			wantPane: "%3",
		},
		{
			name: "gateway session on this host jumps",
			s: session.Session{
				NodeID:    "me",
				NodeLabel: "me",
				Tmux:      session.TmuxLocation{Server: session.TmuxServerDefault, PaneID: "%7"},
			},
			hostname: "me",
			tmuxEnv:  defaultTmuxEnv,
			wantPane: "%7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paneID, reason := planJump(tc.s, tc.hostname, tc.tmuxEnv)
			if tc.wantReason == "" {
				if reason != "" {
					t.Fatalf("expected a jump, got refusal %q", reason)
				}
				if paneID != tc.wantPane {
					t.Fatalf("paneID = %q, want %q", paneID, tc.wantPane)
				}
				return
			}
			if paneID != "" {
				t.Fatalf("expected refusal, got pane %q", paneID)
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Fatalf("reason %q should contain %q", reason, tc.wantReason)
			}
		})
	}
}

// Pressing O outside tmux surfaces the reason as a flash rather than acting.
func TestListJumpFlashesWhenRefused(t *testing.T) {
	t.Setenv("TMUX", "") // force the "not inside tmux" branch deterministically
	m := testModel()
	m.sessions = map[string]session.Session{
		"s1": {ID: "s1", Tmux: session.TmuxLocation{Server: session.TmuxServerDefault, PaneID: "%1"}},
	}
	m.order = []string{"s1"}

	res, cmd := m.handleKey(tea.KeyPressMsg{Code: 'O'})
	got := res.(model)
	if cmd != nil {
		t.Fatalf("a refused jump should issue no command")
	}
	if !strings.Contains(got.flash, "inside tmux") {
		t.Fatalf("expected an explanatory flash, got %q", got.flash)
	}
}
