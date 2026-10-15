package registry

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestStatusSinceStampsOnlyOnChange(t *testing.T) {
	clock := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = old })

	r := New()
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusWorking})
	if s.StatusSince != "2026-10-15T09:00:00Z" {
		t.Fatalf("created working: StatusSince = %q", s.StatusSince)
	}

	clock = clock.Add(5 * time.Second)
	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusWorking})
	if s.StatusSince != "2026-10-15T09:00:00Z" {
		t.Fatalf("same status re-stamped: %q", s.StatusSince)
	}
	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Name: "renamed"})
	if s.StatusSince != "2026-10-15T09:00:00Z" {
		t.Fatalf("an update without a status re-stamped: %q", s.StatusSince)
	}

	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionPermission}})
	if s.StatusSince != "2026-10-15T09:00:05Z" {
		t.Fatalf("status change: StatusSince = %q", s.StatusSince)
	}

	clock = clock.Add(time.Second)
	r.ClearInteraction(s.ID) // awaiting -> working
	got, _ := r.Get(s.ID)
	if got.Status != session.StatusWorking || got.StatusSince != "2026-10-15T09:00:06Z" {
		t.Fatalf("cleared interaction: %q since %q", got.Status, got.StatusSince)
	}
}

func TestStatusSinceSetOnDiscovery(t *testing.T) {
	r := New()
	r.ReconcileSessions("a", []DiscoveredSession{{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0"}})
	for _, s := range r.Snapshot() {
		if s.StatusSince == "" {
			t.Fatalf("discovered session has no StatusSince: %+v", s)
		}
	}
}
