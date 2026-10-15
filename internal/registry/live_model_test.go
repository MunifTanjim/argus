package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func ptr(s string) *string { return &s }

func TestLiveModelOverridesTranscriptSummary(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle,
		Summary: &session.Summary{ModelName: "Opus 4.8", Task: "t1"}})

	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1",
		Model: &ModelUpdate{Name: "Opus 5.5", Color: "#d3869b", Effort: ptr("high")}})
	if sum := s.Summary; sum.ModelName != "Opus 5.5" || sum.ModelColor != "#d3869b" || sum.Effort != "high" || sum.Task != "t1" {
		t.Fatalf("live model not laid over the summary: %+v", sum)
	}

	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1",
		Summary: &session.Summary{ModelName: "Opus 4.8", Task: "t2"}})
	if sum := s.Summary; sum.ModelName != "Opus 5.5" || sum.Effort != "high" || sum.Task != "t2" {
		t.Fatalf("a refreshed transcript summary dropped the live model: %+v", sum)
	}

	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Model: &ModelUpdate{Name: "Sonnet 5.5"}})
	if sum := s.Summary; sum.ModelName != "Sonnet 5.5" || sum.Effort != "high" {
		t.Fatalf("a switch without effort should keep the effort: %+v", sum)
	}

	s, _ = r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Model: &ModelUpdate{Effort: ptr("")}})
	if sum := s.Summary; sum.ModelName != "Sonnet 5.5" || sum.Effort != "" {
		t.Fatalf("an empty effort should clear it and keep the model: %+v", sum)
	}
}

func TestLiveModelNeverMutatesAPublishedSummary(t *testing.T) {
	r := New()
	ch, cancel := r.Subscribe()
	defer cancel()
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle,
		Summary: &session.Summary{ModelName: "Opus 4.8"}})
	first := (<-ch).Session.Summary
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Model: &ModelUpdate{Name: "Opus 5.5"}})
	if first.ModelName != "Opus 4.8" {
		t.Fatalf("a published summary was mutated: %+v", first)
	}
}

func TestLiveModelEndsWithTheSession(t *testing.T) {
	r := New()
	s, _ := r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusIdle,
		Model: &ModelUpdate{Name: "Opus 5.5"}})
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Status: session.StatusDead})
	if _, ok := r.models[s.ID]; ok {
		t.Fatal("the live model outlived its session")
	}
}

func TestLiveModelSurvivesDiscovery(t *testing.T) {
	r := New()
	r.ReconcileSessions("a", []DiscoveredSession{{AgentSessionID: "s1", Summary: &session.Summary{ModelName: "Opus 4.8"}}})
	r.ApplyHook(HookUpdate{Agent: "a", AgentSessionID: "s1", Model: &ModelUpdate{Name: "Opus 5.5"}})
	r.ReconcileSessions("a", []DiscoveredSession{{AgentSessionID: "s1", Summary: &session.Summary{ModelName: "Opus 4.8"}}})
	for _, s := range r.Snapshot() {
		if s.Summary.ModelName != "Opus 5.5" {
			t.Fatalf("a rescan dropped the live model: %+v", s.Summary)
		}
	}
}
