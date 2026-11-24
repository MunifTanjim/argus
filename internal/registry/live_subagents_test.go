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

func TestSetWorkflowsPublishesOnChange(t *testing.T) {
	r := New()
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle})
	events, cancel := r.Subscribe()
	defer cancel()
	list := []session.RunningWorkflow{{RunID: "wf_1", Done: 1, Total: 3}}
	r.SetWorkflows(s.ID, list)
	if n := len(events); n != 1 {
		t.Fatalf("published %d events, want 1", n)
	}
	if ev := <-events; len(ev.Session.Workflows) != 1 || ev.Session.Workflows[0].RunID != "wf_1" {
		t.Fatalf("event lacks workflows: %+v", ev.Session.Workflows)
	}
	r.SetWorkflows(s.ID, list)
	if n := len(events); n != 0 {
		t.Fatalf("unchanged list published %d events", n)
	}
	r.SetWorkflows(s.ID, nil)
	if n := len(events); n != 1 {
		t.Fatalf("clearing published %d events, want 1", n)
	}
	if ev := <-events; len(ev.Session.Workflows) != 0 {
		t.Fatalf("workflows not cleared: %+v", ev.Session.Workflows)
	}
}

func TestWorkflowsSurviveReconcileAndClearOnRemove(t *testing.T) {
	r := New()
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle})
	r.SetWorkflows(s.ID, []session.RunningWorkflow{{RunID: "wf_1"}})
	r.ReconcileSessions("a", []DiscoveredSession{{AgentSessionID: "s1"}})
	if got := r.view(r.sessions[s.ID]); len(got.Workflows) != 1 {
		t.Fatalf("reconcile dropped workflows: %+v", got.Workflows)
	}
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusDead})
	if _, ok := r.workflows[s.ID]; ok {
		t.Fatal("workflows outlived the session")
	}
}

func TestProcStartRecordedOnSessionStart(t *testing.T) {
	clock := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = old })

	r := New()
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle, Started: true})
	if got := r.ProcStart(s.ID); !got.Equal(clock) {
		t.Fatalf("ProcStart = %v, want %v", got, clock)
	}
	clock = clock.Add(time.Hour)
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusWorking})
	if got := r.ProcStart(s.ID); !got.Equal(clock.Add(-time.Hour)) {
		t.Fatalf("a later hook moved ProcStart to %v", got)
	}
}
