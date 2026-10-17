package registry

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestLiveShellsEndWithTheSession(t *testing.T) {
	r := New()
	list := []session.RunningShell{{ID: "b1", ToolUseID: "t1"}}
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle, Shells: &list})
	if len(s.Shells) != 1 {
		t.Fatalf("shells not set: %+v", s.Shells)
	}
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusDead})
	if _, ok := r.shells[s.ID]; ok {
		t.Fatal("the running set outlived its session")
	}
	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle})
	if len(s.Shells) != 0 {
		t.Fatalf("a fresh session inherited shells: %+v", s.Shells)
	}
}

func TestSetShellsKeepsSinceForHeldIDs(t *testing.T) {
	r := New()
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	r.setShells("s1", []session.RunningShell{{ID: "b1"}}, t0)
	r.setShells("s1", []session.RunningShell{{ID: "b1"}, {ID: "b2"}}, t0.Add(time.Hour))
	got := r.shells["s1"]
	if len(got) != 2 || got[0].Since != t0.Format(time.RFC3339) || got[1].Since != t0.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("Since not carried over for b1 / stamped for b2: %+v", got)
	}
}

func TestRepeatedShellsSnapshotPublishesOnce(t *testing.T) {
	r := New()
	list := []session.RunningShell{{ID: "b1", Command: "make test"}}
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle})
	events, cancel := r.Subscribe()
	defer cancel()
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Shells: &list})
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Shells: &list})
	if n := len(events); n != 1 {
		t.Fatalf("published %d updates, want 1 for the change and none for the repeat", n)
	}
}
