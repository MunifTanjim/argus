package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestSetBranchPublishesUpdate(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a"},
	})
	id := "default:%0"

	ch, cancel := r.Subscribe()
	defer cancel()

	r.SetBranch(id, "feat/x")

	ev := <-ch
	if ev.Type != EventUpdated {
		t.Fatalf("event type = %q, want updated", ev.Type)
	}
	if ev.Session.Branch != "feat/x" {
		t.Fatalf("branch = %q, want feat/x", ev.Session.Branch)
	}

	if s, _ := r.Get(id); s.Branch != "feat/x" {
		t.Fatalf("stored branch = %q, want feat/x", s.Branch)
	}
}

func TestSetBranchNoOpWhenUnchanged(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0"},
	})
	id := "default:%0"
	r.SetBranch(id, "main")

	ch, cancel := r.Subscribe()
	defer cancel()

	r.SetBranch(id, "main") // unchanged: must not publish

	select {
	case ev := <-ch:
		t.Fatalf("unexpected event for unchanged branch: %+v", ev)
	default:
	}
}

func TestSetBranchUnknownIDIsNoOp(t *testing.T) {
	r := New()
	ch, cancel := r.Subscribe()
	defer cancel()

	r.SetBranch("nope", "main") // must not panic or publish

	select {
	case ev := <-ch:
		t.Fatalf("unexpected event for unknown id: %+v", ev)
	default:
	}
}
