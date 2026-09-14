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

func TestSurfaceIdle(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{{Question: "Pick"}}},
	})
	id := r.Snapshot()[0].ID

	sub, _ := r.Subscribe()

	// A mismatched expectKind (state changed under the poll) must not swap.
	if r.SurfaceIdle(id, session.InteractionPermission) {
		t.Fatal("SurfaceIdle must skip when the observed kind no longer matches")
	}
	if !r.SurfaceIdle(id, session.InteractionQuestion) {
		t.Fatal("SurfaceIdle should replace the stale question")
	}
	got, _ := r.Get(id)
	if got.Status != session.StatusIdle || got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("want idle composer, got status=%q interaction=%+v", got.Status, got.Interaction)
	}
	// It emitted an update event (not a push-triggering awaiting-input).
	select {
	case ev := <-sub:
		if ev.Type != EventUpdated || ev.Session.Status != session.StatusIdle {
			t.Fatalf("want idle EventUpdated, got %+v", ev)
		}
	default:
		t.Fatal("SurfaceIdle should publish an event")
	}
	// Idempotent: already the idle composer → no change, no event.
	if r.SurfaceIdle(id, session.InteractionQuestion) {
		t.Fatal("SurfaceIdle should be a no-op once idle")
	}
	// Missing session → no change.
	if r.SurfaceIdle("nope", session.InteractionQuestion) {
		t.Fatal("SurfaceIdle on a missing session should be false")
	}
}

// A native interrupt clears the prompt to working (interaction nil). SurfaceIdle
// with an empty expectKind surfaces idle from that state, but a fresh prompt that
// arrived under the poll (non-empty kind) must not be clobbered.
func TestSurfaceIdleFromClearedInteraction(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusWorking,
	})
	id := r.Snapshot()[0].ID

	// A fresh prompt arrived (interaction now permission): empty expect must skip.
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionPermission},
	})
	if r.SurfaceIdle(id, "") {
		t.Fatal("empty expect must not clobber a fresh prompt")
	}

	// Cleared back to working (interaction nil): empty expect surfaces idle.
	r.ClearInteraction(id)
	if !r.SurfaceIdle(id, "") {
		t.Fatal("empty expect should surface idle from a cleared (working) turn")
	}
	got, _ := r.Get(id)
	if got.Status != session.StatusIdle || got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("want idle composer, got status=%q interaction=%+v", got.Status, got.Interaction)
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

func TestSeedInsertsAndIndexes(t *testing.T) {
	r := New()
	r.Seed([]session.Session{
		{ID: "s1", Agent: "claude", AgentSessionID: "cs1"},
		{ID: "s2", Agent: "codex", Tmux: session.TmuxLocation{Server: session.TmuxServerDefault, PaneID: "%3"}},
	})

	if got := r.Snapshot(); len(got) != 2 {
		t.Fatalf("Snapshot len = %d, want 2", len(got))
	}
	if _, ok := r.Get("s1"); !ok {
		t.Fatal("s1 not found")
	}
	if id, ok := r.index.findByAgentSession("cs1"); !ok || id != "s1" {
		t.Fatalf("findByAgentSession(cs1) = %q,%v; want s1,true", id, ok)
	}
	if id, ok := r.index.findByPane(PaneKey(session.TmuxServerDefault, "%3")); !ok || id != "s2" {
		t.Fatalf("findByPane = %q,%v; want s2,true", id, ok)
	}
}
