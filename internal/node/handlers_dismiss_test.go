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

type fakeDismisser struct {
	adapter.Adapter
	called bool
}

func (f *fakeDismisser) Dismiss(context.Context, session.Session) error { f.called = true; return nil }

func TestHandleSessionDismiss(t *testing.T) {
	fd := &fakeDismisser{}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["opencode"] = fd

	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent:          "opencode",
		AgentSessionID: "ses_1",
		Status:         session.StatusIdle,
	})

	call := func() error {
		params, _ := json.Marshal(api.SessionRef{SessionID: s.ID})
		_, err := d.handleSessionDismiss(context.Background(), params)
		return err
	}

	// idle → allowed
	if err := call(); err != nil || !fd.called {
		t.Fatalf("idle dismiss: err=%v called=%v", err, fd.called)
	}

	// working → rejected
	fd.called = false
	s, _ = d.reg.ApplyHook(registry.HookUpdate{
		Agent:          "opencode",
		AgentSessionID: "ses_1",
		Status:         session.StatusWorking,
	})
	if err := call(); err == nil || fd.called {
		t.Fatalf("working dismiss should be rejected: err=%v called=%v", err, fd.called)
	}

	// awaiting permission → rejected
	fd.called = false
	s, _ = d.reg.ApplyHook(registry.HookUpdate{
		Agent:              "opencode",
		AgentSessionID:     "ses_1",
		Status:             session.StatusAwaitingInput,
		Interaction:        &session.Interaction{Kind: session.InteractionPermission},
		ReplaceInteraction: true,
	})
	if err := call(); err == nil || fd.called {
		t.Fatalf("permission dismiss should be rejected: err=%v called=%v", err, fd.called)
	}
}
