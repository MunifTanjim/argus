package registry

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestSubscribeReceivesEvents(t *testing.T) {
	r := New()
	ch, cancel := r.Subscribe()
	defer cancel()

	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})

	ev := <-ch
	if ev.Type != EventAdded || ev.Session.Tmux.PaneID != "%0" {
		t.Fatalf("want added %%0, got %+v", ev)
	}

	r.ReconcileSessions("claude", nil)
	ev = <-ch
	if ev.Type != EventRemoved || ev.Session.Tmux.PaneID != "%0" {
		t.Fatalf("want removed %%0, got %+v", ev)
	}
}

func TestEndedGraceSuppressesRescanReCreation(t *testing.T) {
	r := New()
	r.endedGrace = 10 * time.Second // keep grace active throughout the test

	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusWorking,
	})
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("setup: want 1 session, got %d", n)
	}

	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusDead,
	})
	if n := len(r.Snapshot()); n != 0 {
		t.Fatalf("after dead hook: want 0 sessions, got %d", n)
	}

	pKey := PaneKey(session.TmuxServerDefault, "%0")
	if !r.recentlyEnded(pKey) {
		t.Error("pane key should be recentlyEnded after exit hook")
	}
	if !r.recentlyEnded("s1") {
		t.Error("agent session id should be recentlyEnded after exit hook")
	}

	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("s1", "%0", "a", session.TmuxServerDefault),
	})
	if n := len(r.Snapshot()); n != 0 {
		t.Fatalf("rescan during grace must not re-create, got %d sessions", n)
	}

	// clearEnded (via a non-dead hook) re-admits the session.
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusIdle,
	})
	if r.recentlyEnded(pKey) {
		t.Error("pane key should be cleared after a non-dead hook")
	}
	if r.recentlyEnded("s1") {
		t.Error("agent session id should be cleared after a non-dead hook")
	}

	// Remove the hook-created session so reconcile can exercise re-creation.
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusDead,
	})
	// Manually clear so it looks like the grace expired.
	r.mu.Lock()
	delete(r.ended, pKey)
	delete(r.ended, "s1")
	r.mu.Unlock()

	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("s1", "%0", "a", session.TmuxServerDefault),
	})
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("after grace cleared, rescan must re-create; got %d sessions", n)
	}
}

func TestRecentlyEndedExpiresAfterGrace(t *testing.T) {
	r := New()
	r.endedGrace = 1 * time.Millisecond

	r.mu.Lock()
	r.ended["key1"] = time.Now().Add(-10 * time.Millisecond) // already expired
	r.mu.Unlock()

	r.mu.Lock()
	still := r.recentlyEnded("key1")
	_, present := r.ended["key1"]
	r.mu.Unlock()

	if still {
		t.Error("recentlyEnded should return false after grace expires")
	}
	if present {
		t.Error("expired entry should be self-cleaned from the map")
	}
}

func TestReconcileSessionsSweepsExpiredEndedEntries(t *testing.T) {
	r := New()
	r.endedGrace = 1 * time.Millisecond

	r.mu.Lock()
	r.ended["ghost-pane"] = time.Now().Add(-time.Second) // expired, never rediscovered
	r.mu.Unlock()

	r.ReconcileSessions("claude", nil) // no discovered panes: ghost is not queried

	r.mu.Lock()
	_, present := r.ended["ghost-pane"]
	r.mu.Unlock()
	if present {
		t.Error("reconcile should sweep an expired ended entry that is never rediscovered")
	}
}

func TestSnapshotStampsStatusLabel(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{
		Agent:  "claude",
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
