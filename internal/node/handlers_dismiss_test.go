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

// A paneless session is dismissed via the unified sessions.kill verb; the dismiss
// is refused while the session is working or awaiting a question/permission.
func TestHandleSessionKillDismissesPanelessSession(t *testing.T) {
	fd := &fakeDismisser{}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["opencode"] = fd

	set := func(status session.Status, in *session.Interaction) session.Session {
		s, _ := d.reg.ApplyHook(registry.HookUpdate{
			Agent:              "opencode",
			AgentSessionID:     "ses_1",
			Status:             status,
			Interaction:        in,
			ReplaceInteraction: true,
		})
		return s
	}
	call := func(id string) error {
		params, _ := json.Marshal(api.SessionRef{SessionID: id})
		_, err := d.handleSessionKill(context.Background(), params)
		return err
	}

	// idle → dismissed
	s := set(session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	if err := call(s.ID); err != nil || !fd.called {
		t.Fatalf("idle dismiss: err=%v called=%v", err, fd.called)
	}

	// working → rejected
	fd.called = false
	s = set(session.StatusWorking, nil)
	if err := call(s.ID); err == nil || fd.called {
		t.Fatalf("working dismiss should be rejected: err=%v called=%v", err, fd.called)
	}

	// awaiting permission → rejected
	fd.called = false
	s = set(session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionPermission})
	if err := call(s.ID); err == nil || fd.called {
		t.Fatalf("permission dismiss should be rejected: err=%v called=%v", err, fd.called)
	}

	// awaiting question → rejected
	fd.called = false
	s = set(session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionQuestion})
	if err := call(s.ID); err == nil || fd.called {
		t.Fatalf("question dismiss should be rejected: err=%v called=%v", err, fd.called)
	}
}
