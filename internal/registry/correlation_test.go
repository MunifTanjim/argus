package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

// A hook can arrive before discovery sees the pane (session learned via hook, keyed
// by pane). A later discovery of that same pane must enrich the one session, not
// create a duplicate.
func TestHookThenDiscoverySamePaneStaysSingle(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{
		Tool:   "claude-code",
		Server: session.TmuxServerDefault,
		PaneID: "%0",
		Status: session.StatusWorking,
	})
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{pane("%0", "a")})

	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("hook+discovery for the same pane should yield 1 session, got %d", n)
	}
}
