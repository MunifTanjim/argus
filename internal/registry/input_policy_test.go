package registry

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestInputPolicyOverridesPaneInput(t *testing.T) {
	r := New()
	r.SetInputPolicy("claude", func(session.Session) session.InputMode { return session.InputNone })
	s, _ := r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1",
		AgentSessionID: "s1", Status: session.StatusIdle,
	})
	if s.Input != session.InputNone {
		t.Fatalf("Input = %q, want none: the policy wins over the pane", s.Input)
	}
}

func TestInputPolicyLeavesOtherAgentsAlone(t *testing.T) {
	r := New()
	r.SetInputPolicy("claude", func(session.Session) session.InputMode { return session.InputNone })
	s, _ := r.ApplyHook(HookUpdate{
		Agent: "antigravity", Server: session.TmuxServerDefault, PaneID: "%2",
		AgentSessionID: "a1", Status: session.StatusIdle,
	})
	if s.Input != session.InputPane {
		t.Fatalf("Input = %q, want pane", s.Input)
	}
}

func TestRefreshInputPublishesOnChange(t *testing.T) {
	r := New()
	reachable := false
	r.SetInputPolicy("claude", func(session.Session) session.InputMode {
		if reachable {
			return session.InputAPI
		}
		return session.InputNone
	})
	r.ApplyHook(HookUpdate{Agent: "claude", AgentSessionID: "s1", Status: session.StatusIdle})
	events, cancel := r.Subscribe()
	defer cancel()

	reachable = true
	r.RefreshInput("s1")
	select {
	case ev := <-events:
		if ev.Type != EventUpdated || ev.Session.Input != session.InputAPI {
			t.Fatalf("event = %+v, want updated with Input api", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event after the input mode changed")
	}

	r.RefreshInput("s1") // unchanged: must not publish
	select {
	case ev := <-events:
		t.Fatalf("unexpected event %+v for an unchanged input mode", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRefreshInputUnknownSessionIsNoop(t *testing.T) {
	r := New()
	r.RefreshInput("missing") // must not panic
}

func TestSetInputPolicyNilRemovesPolicy(t *testing.T) {
	r := New()
	r.SetInputPolicy("claude", func(session.Session) session.InputMode { return session.InputNone })
	r.SetInputPolicy("claude", nil)
	s, _ := r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%3",
		AgentSessionID: "s3", Status: session.StatusIdle,
	})
	if s.Input != session.InputPane {
		t.Fatalf("Input = %q, want pane once the policy is removed", s.Input)
	}
}
