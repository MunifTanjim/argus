package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func pane(id, name string) DiscoveredPane {
	return DiscoveredPane{
		Tool:        "claude-code",
		Server:      session.TmuxServerDefault,
		PaneID:      id,
		SessionName: name,
		CurrentPath: "/tmp",
	}
}

func TestReconcileAddsUpdatesRemoves(t *testing.T) {
	r := New()

	// Add two.
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{
		pane("%0", "a"), pane("%1", "b"),
	})
	if got := len(r.Snapshot()); got != 2 {
		t.Fatalf("want 2 sessions, got %d", got)
	}

	// Update %0's window name; drop %1; add %2.
	upd := pane("%0", "a2")
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{
		upd, pane("%2", "c"),
	})
	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 sessions after reconcile, got %d", len(snap))
	}
	byID := map[string]session.Session{}
	for _, s := range snap {
		byID[s.Tmux.PaneID] = s
	}
	if _, gone := byID["%1"]; gone {
		t.Errorf("%%1 should have been removed")
	}
	if byID["%0"].Tmux.SessionName != "a2" {
		t.Errorf("%%0 session name: want a2, got %q", byID["%0"].Tmux.SessionName)
	}
	if byID["%0"].Status != session.StatusDiscovered {
		t.Errorf("%%0 status: want discovered, got %q", byID["%0"].Status)
	}
}

func TestReconcileDiscoveredSeedsStatusHint(t *testing.T) {
	const server = session.TmuxServerDefault

	// New idle session: seeded idle + synthesized idle interaction.
	r := New()
	r.ReconcileDiscovered("claude", server, []DiscoveredPane{
		{Tool: "claude", Server: server, PaneID: "%1", StatusHint: session.StatusIdle},
	})
	s, ok := r.Get(paneKey(server, "%1"))
	if !ok || s.Status != session.StatusIdle {
		t.Fatalf("new idle: ok=%v status=%q want idle", ok, s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("new idle: want idle interaction, got %+v", s.Interaction)
	}

	// New working session: seeded working, no interaction.
	r.ReconcileDiscovered("claude", server, []DiscoveredPane{
		{Tool: "claude", Server: server, PaneID: "%2", StatusHint: session.StatusWorking},
	})
	s, _ = r.Get(paneKey(server, "%2"))
	if s.Status != session.StatusWorking || s.Interaction != nil {
		t.Fatalf("new working: status=%q interaction=%+v", s.Status, s.Interaction)
	}

	// No hint: stays discovered.
	r.ReconcileDiscovered("claude", server, []DiscoveredPane{
		{Tool: "claude", Server: server, PaneID: "%3"},
	})
	s, _ = r.Get(paneKey(server, "%3"))
	if s.Status != session.StatusDiscovered {
		t.Fatalf("no hint: status=%q want discovered", s.Status)
	}
}

func TestReconcileDiscoveredNeverOverridesHookStatus(t *testing.T) {
	const server = session.TmuxServerDefault
	r := New()
	// Create as discovered, then a hook promotes it to working.
	r.ReconcileDiscovered("claude", server, []DiscoveredPane{
		{Tool: "claude", Server: server, PaneID: "%1"},
	})
	r.ApplyHook(HookUpdate{
		Tool: "claude", Server: server, PaneID: "%1", Status: session.StatusWorking,
	})

	// A later discovery carrying an idle hint must NOT downgrade the hook status.
	r.ReconcileDiscovered("claude", server, []DiscoveredPane{
		{Tool: "claude", Server: server, PaneID: "%1", StatusHint: session.StatusIdle},
	})
	s, _ := r.Get(paneKey(server, "%1"))
	if s.Status != session.StatusWorking || s.Interaction != nil {
		t.Fatalf("hook status overridden: status=%q interaction=%+v", s.Status, s.Interaction)
	}
}

func TestReconcileServersAreIndependent(t *testing.T) {
	r := New()
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{pane("%0", "a")})

	argusPane := DiscoveredPane{Tool: "claude-code", Server: session.TmuxServerArgus, PaneID: "%0", SessionName: "x"}
	r.ReconcileDiscovered("claude-code", session.TmuxServerArgus, []DiscoveredPane{argusPane})

	if got := len(r.Snapshot()); got != 2 {
		t.Fatalf("same pane id on two servers must be two sessions, got %d", got)
	}

	// Reconciling one server empty must not touch the other.
	r.ReconcileDiscovered("claude-code", session.TmuxServerArgus, nil)
	snap := r.Snapshot()
	if len(snap) != 1 || snap[0].Tmux.Server != session.TmuxServerDefault {
		t.Fatalf("default server session should survive argus reconcile: %+v", snap)
	}
}

func TestSubscribeReceivesEvents(t *testing.T) {
	r := New()
	ch, cancel := r.Subscribe()
	defer cancel()

	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{pane("%0", "a")})

	ev := <-ch
	if ev.Type != EventAdded || ev.Session.Tmux.PaneID != "%0" {
		t.Fatalf("want added %%0, got %+v", ev)
	}

	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, nil)
	ev = <-ch
	if ev.Type != EventRemoved || ev.Session.Tmux.PaneID != "%0" {
		t.Fatalf("want removed %%0, got %+v", ev)
	}
}

func TestSnapshotStampsStatusLabel(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{
		Tool:   "claude-code",
		Server: session.TmuxServerDefault,
		PaneID: "%1",
		Status: session.StatusWorking,
	})
	snap := r.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected at least one session")
	}
	for _, s := range snap {
		if s.StatusLabel != "working" {
			t.Fatalf("status_label = %q, want working", s.StatusLabel)
		}
	}
}
