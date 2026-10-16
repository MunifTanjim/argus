package registry

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestLiveSubagentsEndWithTheSession(t *testing.T) {
	r := New()
	list := []session.RunningSubagent{{ID: "a1", Type: "Explore"}}
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle, Subagents: &list})
	if len(s.Subagents) != 1 {
		t.Fatalf("subagents not set: %+v", s.Subagents)
	}
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusDead})
	if _, ok := r.subagents[s.ID]; ok {
		t.Fatal("the running set outlived its session")
	}
	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle})
	if len(s.Subagents) != 0 {
		t.Fatalf("a fresh session inherited subagents: %+v", s.Subagents)
	}
}

func TestSetSubagentsKeepsSinceForHeldIDs(t *testing.T) {
	r := New()
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	r.setSubagents("s1", []session.RunningSubagent{{ID: "a1"}}, t0)
	r.setSubagents("s1", []session.RunningSubagent{{ID: "a1"}, {ID: "a2"}}, t0.Add(time.Hour))
	got := r.subagents["s1"]
	if len(got) != 2 || got[0].Since != t0.Format(time.RFC3339) || got[1].Since != t0.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("Since not carried over for a1 / stamped for a2: %+v", got)
	}
}

func TestRepeatedSubagentsSnapshotPublishesOnce(t *testing.T) {
	r := New()
	list := []session.RunningSubagent{{ID: "a1", Type: "Explore"}}
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle})
	events, cancel := r.Subscribe()
	defer cancel()
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Subagents: &list})
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Subagents: &list})
	if n := len(events); n != 1 {
		t.Fatalf("published %d updates, want 1 for the change and none for the repeat", n)
	}
}
