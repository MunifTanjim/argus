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
