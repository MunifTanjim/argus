package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

// A rename touches nothing but the name.
func TestRenameSessionSetsNameOnly(t *testing.T) {
	r := New()
	ch, cancel := r.Subscribe()
	defer cancel()

	s, alive := r.ApplyHook(HookUpdate{
		Agent:              "claude",
		AgentSessionID:     "s1",
		Status:             session.StatusAwaitingInput,
		Interaction:        &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"},
		ReplaceInteraction: true,
	})
	if !alive {
		t.Fatal("session should exist after ApplyHook")
	}
	<-ch // added

	r.RenameSession(s.ID, "New name")
	got, _ := r.Get(s.ID)
	if got.Name != "New name" {
		t.Errorf("Name = %q, want New name", got.Name)
	}
	if got.Status != session.StatusAwaitingInput {
		t.Errorf("Status = %q, want it untouched", got.Status)
	}
	if got.Interaction == nil || got.Interaction.ToolName != "Bash" {
		t.Errorf("Interaction = %+v, want the pending prompt preserved", got.Interaction)
	}
	ev := <-ch
	if ev.Type != EventUpdated || ev.Session.Name != "New name" {
		t.Errorf("event = %+v, want an update carrying the new name", ev)
	}

	r.RenameSession(s.ID, "New name")
	r.RenameSession("missing", "x")
	select {
	case ev := <-ch:
		t.Errorf("event = %+v, want none for an unchanged name or an unknown id", ev)
	default:
	}
}
