package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func cost(v float64) *float64 { return &v }

func TestLiveCostOverlaysTheSummary(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle,
		Summary: &session.Summary{ModelName: "Opus 4.8", Effort: "high", Task: "t1"}})

	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", CostUSD: cost(1.25)})
	if sum := s.Summary; !sum.HasCost || sum.CostUSD != 1.25 || sum.ModelName != "Opus 4.8" || sum.Effort != "high" {
		t.Fatalf("cost not laid over the summary, or it disturbed the model: %+v", sum)
	}

	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1",
		Summary: &session.Summary{ModelName: "Opus 4.8", Task: "t2"}})
	if sum := s.Summary; !sum.HasCost || sum.CostUSD != 1.25 || sum.Task != "t2" {
		t.Fatalf("a refreshed transcript summary dropped the cost: %+v", sum)
	}

	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", CostUSD: cost(0)})
	if sum := s.Summary; !sum.HasCost || sum.CostUSD != 0 {
		t.Fatalf("a reset cost should still show: %+v", sum)
	}
}

func TestLiveCostEndsWithTheSession(t *testing.T) {
	r := New()
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle, CostUSD: cost(2)})
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusDead})
	if _, ok := r.live[s.ID]; ok {
		t.Fatal("the live cost outlived its session")
	}
}

func TestLiveCostResetsOnClear(t *testing.T) {
	r := New()
	hook := func(u HookUpdate) session.Session {
		u.Agent, u.Server, u.PaneID, u.Status = "claude", session.TmuxServerDefault, "%0", session.StatusIdle
		s, _ := r.ApplyHook(u)
		return s
	}
	hook(HookUpdate{AgentSessionID: "c0", Model: &ModelUpdate{Name: "Opus 4.8"}, CostUSD: cost(2)})
	s := hook(HookUpdate{AgentSessionID: "c1", Cleared: true})
	if sum := s.Summary; sum.HasCost || sum.CostUSD != 0 || sum.ModelName != "Opus 4.8" {
		t.Fatalf("/clear should drop the old cost and keep the model: %+v", sum)
	}
	s = hook(HookUpdate{AgentSessionID: "c1", CostUSD: cost(0.1)})
	if sum := s.Summary; !sum.HasCost || sum.CostUSD != 0.1 {
		t.Fatalf("a cost after /clear should show: %+v", sum)
	}
}
