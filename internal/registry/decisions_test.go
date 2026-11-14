package registry

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func newWorkingSession(t *testing.T, r *Registry) string {
	t.Helper()
	s, _ := r.ApplyHook(HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", AgentSessionID: "s1", Status: session.StatusWorking})
	return s.ID
}

func permission(requestID string) session.Interaction {
	return session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", RequestID: requestID}
}

func headRequestID(t *testing.T, r *Registry, id string) string {
	t.Helper()
	s, ok := r.Get(id)
	if !ok {
		t.Fatalf("session %q missing", id)
	}
	if s.Interaction == nil {
		return ""
	}
	return s.Interaction.RequestID
}

func TestDecisionOverlaysBase(t *testing.T) {
	r := New()
	id := newWorkingSession(t, r)
	r.PushDecision(id, permission("r1"))

	s, _ := r.Get(id)
	if s.Status != session.StatusAwaitingInput {
		t.Fatalf("status = %q, want awaiting_input", s.Status)
	}
	if s.Interaction == nil || s.Interaction.RequestID != "r1" || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("interaction = %+v, want permission r1", s.Interaction)
	}
}

func TestDecisionStatusSinceIsFirstPush(t *testing.T) {
	clock := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = old })

	r := New()
	id := newWorkingSession(t, r)
	clock = clock.Add(5 * time.Second)
	r.PushDecision(id, permission("r1"))
	clock = clock.Add(5 * time.Second)
	r.PushDecision(id, permission("r2"))
	r.DropDecision(id, "r1")

	s, _ := r.Get(id)
	if s.StatusSince != "2026-10-23T09:00:05Z" {
		t.Fatalf("StatusSince = %q, want first push time", s.StatusSince)
	}
	for _, snap := range r.Snapshot() {
		if snap.Status != session.StatusAwaitingInput || snap.StatusLabel != session.StatusAwaitingInput.Label() {
			t.Fatalf("snapshot = %q label %q", snap.Status, snap.StatusLabel)
		}
	}

	r.DropDecision(id, "r2")
	s, _ = r.Get(id)
	if s.StatusSince != "2026-10-23T09:00:00Z" {
		t.Fatalf("base StatusSince = %q", s.StatusSince)
	}
}

func TestDecisionHeadIsOldest(t *testing.T) {
	r := New()
	id := newWorkingSession(t, r)
	r.PushDecision(id, permission("r1"))
	r.PushDecision(id, permission("r2"))

	if got := headRequestID(t, r, id); got != "r1" {
		t.Fatalf("head = %q, want r1", got)
	}
	r.DropDecision(id, "r1")
	if got := headRequestID(t, r, id); got != "r2" {
		t.Fatalf("head = %q, want r2", got)
	}
	r.DropDecision(id, "r2")
	s, _ := r.Get(id)
	if s.Status != session.StatusWorking || s.Interaction != nil {
		t.Fatalf("after drops: status %q interaction %+v, want working nil", s.Status, s.Interaction)
	}
}

func TestPushDecisionReplacesDuplicateInPlace(t *testing.T) {
	r := New()
	id := newWorkingSession(t, r)
	r.PushDecision(id, permission("r1"))
	r.PushDecision(id, permission("r2"))
	again := permission("r1")
	again.ToolName = "Edit"
	r.PushDecision(id, again)

	s, _ := r.Get(id)
	if s.Interaction == nil || s.Interaction.RequestID != "r1" || s.Interaction.ToolName != "Edit" {
		t.Fatalf("head = %+v, want r1 with ToolName Edit", s.Interaction)
	}
	r.DropDecision(id, "r1")
	if got := headRequestID(t, r, id); got != "r2" {
		t.Fatalf("head after one drop of r1 = %q, want r2", got)
	}
}

func TestHookEventsDoNotDropDecision(t *testing.T) {
	r := New()
	id := newWorkingSession(t, r)
	r.PushDecision(id, permission("r1"))

	s, _ := r.ApplyHook(HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", Status: session.StatusWorking})
	if s.Status != session.StatusAwaitingInput || s.Interaction == nil || s.Interaction.RequestID != "r1" {
		t.Fatalf("after PreToolUse: %q %+v", s.Status, s.Interaction)
	}
	s, _ = r.ApplyHook(HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", Status: session.StatusIdle,
		ReplaceInteraction: true, Interaction: &session.Interaction{Kind: session.InteractionIdle}})
	if s.Status != session.StatusAwaitingInput || s.Interaction == nil || s.Interaction.RequestID != "r1" {
		t.Fatalf("after Stop: %q %+v", s.Status, s.Interaction)
	}
	if got := headRequestID(t, r, id); got != "r1" {
		t.Fatalf("head = %q, want r1", got)
	}

	r.DropDecision(id, "r1")
	s, _ = r.Get(id)
	if s.Status != session.StatusIdle || s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("after drop: %q %+v, want idle base", s.Status, s.Interaction)
	}
}

func TestPublishedEventsCarryOverlay(t *testing.T) {
	r := New()
	id := newWorkingSession(t, r)
	ch, cancel := r.Subscribe()
	defer cancel()

	r.PushDecision(id, permission("r1"))
	select {
	case ev := <-ch:
		if ev.Type != EventUpdated || ev.Session.Interaction == nil || ev.Session.Interaction.RequestID != "r1" {
			t.Fatalf("event = %s %+v", ev.Type, ev.Session.Interaction)
		}
		if ev.Session.Status != session.StatusAwaitingInput {
			t.Fatalf("event status = %q", ev.Session.Status)
		}
	default:
		t.Fatal("no event published")
	}

	r.RenameSession(id, "renamed")
	select {
	case ev := <-ch:
		if ev.Session.Interaction == nil || ev.Session.Interaction.RequestID != "r1" {
			t.Fatalf("rename event interaction = %+v", ev.Session.Interaction)
		}
	case <-time.After(time.Second):
		t.Fatal("no rename event published")
	}
}

func TestBaseInteractionKindIgnoresOverlay(t *testing.T) {
	r := New()
	s, _ := r.ApplyHook(HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionQuestion}})
	r.PushDecision(s.ID, permission("r1"))

	kind, ok := r.BaseInteractionKind(s.ID)
	if !ok || kind != session.InteractionQuestion {
		t.Fatalf("BaseInteractionKind = %q %v, want question", kind, ok)
	}

	r.ClearInteraction(s.ID)
	if kind, ok := r.BaseInteractionKind(s.ID); !ok || kind != "" {
		t.Fatalf("BaseInteractionKind after clear = %q %v, want empty", kind, ok)
	}
	if _, ok := r.BaseInteractionKind("missing"); ok {
		t.Fatal("unknown session reported as present")
	}
}

func TestRemoveDropsDecisions(t *testing.T) {
	r := New()
	id := newWorkingSession(t, r)
	r.PushDecision(id, permission("r1"))

	r.ApplyHook(HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", Status: session.StatusDead})
	s, _ := r.ApplyHook(HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", AgentSessionID: "s2", Status: session.StatusWorking})
	if s.ID != id {
		t.Fatalf("recreated id = %q, want %q", s.ID, id)
	}
	if s.Status != session.StatusWorking || s.Interaction != nil {
		t.Fatalf("recreated: %q %+v, want working nil", s.Status, s.Interaction)
	}
}

func TestPushDecisionUnknownSessionIsNoop(t *testing.T) {
	r := New()
	r.PushDecision("missing", permission("r1"))
	if len(r.decisions) != 0 {
		t.Fatalf("decisions = %v, want empty", r.decisions)
	}
}
