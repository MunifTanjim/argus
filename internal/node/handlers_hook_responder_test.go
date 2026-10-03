package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

type fakeResponder struct {
	adapter.Adapter
	got *api.RespondParams
}

func (f *fakeResponder) Respond(_ context.Context, _ session.Session, p api.RespondParams) error {
	f.got = &p
	return nil
}

func TestHandleSessionRespond_CallsResponderWhenNoParkedDecision(t *testing.T) {
	fr := &fakeResponder{}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["opencode"] = fr

	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent:          "opencode",
		AgentSessionID: "s1",
		Status:         session.StatusIdle,
	})

	params, _ := json.Marshal(api.RespondParams{SessionID: s.ID, Behavior: "allow"})
	if _, err := d.handleSessionRespond(context.Background(), params); err != nil {
		t.Fatalf("handleSessionRespond: %v", err)
	}
	if fr.got == nil {
		t.Fatal("Respond was not called")
	}
	if fr.got.Behavior != "allow" {
		t.Fatalf("got behavior %q", fr.got.Behavior)
	}
}

type fakeOwningResponder struct {
	fakeResponder
}

func (*fakeOwningResponder) OwnsInteraction() bool { return true }

func respondTo(t *testing.T, a adapter.Adapter, agent string) session.Session {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters[agent] = a
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent:          agent,
		AgentSessionID: "s1",
		Status:         session.StatusAwaitingInput,
		Interaction:    &session.Interaction{Kind: session.InteractionPermission, Message: "next"},
	})
	params, _ := json.Marshal(api.RespondParams{SessionID: s.ID, Behavior: "allow"})
	if _, err := d.handleSessionRespond(context.Background(), params); err != nil {
		t.Fatalf("handleSessionRespond: %v", err)
	}
	got, _ := d.reg.Get(s.ID)
	return got
}

func TestHandleSessionRespond_ClearsInteractionForPlainResponder(t *testing.T) {
	got := respondTo(t, &fakeResponder{}, "opencode")
	if got.Interaction != nil || got.Status != session.StatusWorking {
		t.Fatalf("plain responder: interaction=%+v status=%v; want cleared/working", got.Interaction, got.Status)
	}
}

func TestHandleSessionRespond_SkipsClearForInteractionOwner(t *testing.T) {
	got := respondTo(t, &fakeOwningResponder{}, "codex")
	if got.Interaction == nil || got.Interaction.Message != "next" || got.Status != session.StatusAwaitingInput {
		t.Fatalf("interaction owner: interaction=%+v status=%v; want untouched", got.Interaction, got.Status)
	}
}
