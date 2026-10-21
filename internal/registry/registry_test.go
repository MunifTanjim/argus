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

// A decision nobody answered clears a still-pending prompt to working, but
// leaves the idle composer a later event surfaced untouched.
func TestClearPendingDecision(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionPermission},
	})
	id := r.Snapshot()[0].ID

	r.ClearPendingDecision(id)
	got, _ := r.Get(id)
	if got.Status != session.StatusWorking || got.Interaction != nil {
		t.Fatalf("want working with no interaction, got status=%q interaction=%+v", got.Status, got.Interaction)
	}

	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionIdle},
	})
	sub, unsub := r.Subscribe()
	defer unsub()
	r.ClearPendingDecision(id)
	got, _ = r.Get(id)
	if got.Status != session.StatusAwaitingInput || got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("want the idle composer kept, got status=%q interaction=%+v", got.Status, got.Interaction)
	}
	select {
	case ev := <-sub:
		t.Fatalf("ClearPendingDecision on an idle interaction published %+v", ev)
	default:
	}
}

func TestApplyHookFollowsThePreviousAgentSession(t *testing.T) {
	r := New()
	first, _ := r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "a", Name: "old", TranscriptPath: "/a.jsonl", Input: session.InputAPI, Status: session.StatusWorking})
	if first.ID != "pi:a" {
		t.Fatalf("first id = %q, want pi:a", first.ID)
	}
	ch, cancel := r.Subscribe()
	defer cancel()

	got, alive := r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "b", PrevAgentSessionID: "a", Input: session.InputAPI, Status: session.StatusWorking})
	if !alive || got.ID != "pi:a" || got.AgentSessionID != "b" {
		t.Fatalf("got %+v alive=%v, want id pi:a agent session b", got, alive)
	}
	if got.Name != "" || got.TranscriptPath != "" {
		t.Fatalf("name %q transcript %q carried over from the previous session", got.Name, got.TranscriptPath)
	}
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("want 1 session, got %d", n)
	}
	select {
	case ev := <-ch:
		if ev.Type != EventUpdated || ev.Session.ID != "pi:a" {
			t.Fatalf("event = %+v, want updated pi:a", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	if s, ok := r.FindByAgentSession("b"); !ok || s.ID != "pi:a" {
		t.Fatalf("FindByAgentSession(b) = %+v,%v", s, ok)
	}
	if _, ok := r.FindByAgentSession("a"); ok {
		t.Fatal("FindByAgentSession(a) still finds the record")
	}
}

func TestApplyHookSwitchOnAPaneDropsTheOldName(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "a", Server: session.TmuxServerArgus, PaneID: "%7", Name: "old", TranscriptPath: "/a.jsonl", Input: session.InputAPI, Status: session.StatusWorking})
	got, _ := r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "b", PrevAgentSessionID: "a", Server: session.TmuxServerArgus, PaneID: "%7", Input: session.InputAPI, Status: session.StatusWorking})
	if got.ID != "pi:a" || got.AgentSessionID != "b" || got.Name != "" || got.TranscriptPath != "" {
		t.Fatalf("got id %q session %q name %q transcript %q", got.ID, got.AgentSessionID, got.Name, got.TranscriptPath)
	}
}

func TestApplyHookCreateAvoidsASwitchedRecordsID(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "a", Input: session.InputAPI, Status: session.StatusWorking})
	r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "b", PrevAgentSessionID: "a", Input: session.InputAPI, Status: session.StatusWorking})
	ch, cancel := r.Subscribe()
	defer cancel()

	if dead, alive := r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "a", Status: session.StatusDead}); alive || dead.ID == "pi:a" {
		t.Fatalf("dead for unheld a = %+v alive=%v, want a removal under another id", dead, alive)
	}
	select {
	case ev := <-ch:
		if ev.Session.ID == "pi:a" {
			t.Fatalf("event for the switched record: %+v", ev)
		}
	case <-time.After(time.Second):
	}
	if s, ok := r.Get("pi:a"); !ok || s.AgentSessionID != "b" {
		t.Fatalf("switched record = %+v,%v", s, ok)
	}

	got, _ := r.ApplyHook(HookUpdate{Agent: "pi", AgentSessionID: "a", Input: session.InputAPI, Status: session.StatusWorking})
	if got.ID == "pi:a" || got.AgentSessionID != "a" {
		t.Fatalf("new record = id %q session %q", got.ID, got.AgentSessionID)
	}
	if n := len(r.Snapshot()); n != 2 {
		t.Fatalf("want 2 sessions, got %d", n)
	}
	a, okA := r.FindByAgentSession("a")
	b, okB := r.FindByAgentSession("b")
	if !okA || !okB || a.ID == b.ID || a.AgentSessionID != "a" || b.AgentSessionID != "b" || b.ID != "pi:a" {
		t.Fatalf("FindByAgentSession a = %+v,%v b = %+v,%v", a, okA, b, okB)
	}
}
