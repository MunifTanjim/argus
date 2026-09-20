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
func (f *fakeDismisser) IsHeadless() bool                               { return true }

// A paneless session is removed via the unified sessions.kill verb, unconditionally
// (matching pane kill for other agents) regardless of its status or interaction.
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

	cases := []struct {
		name   string
		status session.Status
		in     *session.Interaction
	}{
		{"idle", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}},
		{"working", session.StatusWorking, nil},
		{"permission", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionPermission}},
		{"question", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionQuestion}},
	}
	for _, tc := range cases {
		fd.called = false
		s := set(tc.status, tc.in)
		if err := call(s.ID); err != nil || !fd.called {
			t.Fatalf("%s: want unconditional dismiss, got err=%v called=%v", tc.name, err, fd.called)
		}
	}
}
