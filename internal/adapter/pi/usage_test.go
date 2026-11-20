package pi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func fullStats(cost float64, tokens int, percent float64) map[string]any {
	return map[string]any{"cost": cost, "contextUsage": map[string]any{"tokens": tokens, "contextWindow": 200000, "percent": percent}}
}

func spawnWithStats(t *testing.T, f *fakePi) (*discoverer, *registry.Registry, string) {
	t.Helper()
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d, reg, id
}

func waitSummary(t *testing.T, reg *registry.Registry, ok func(session.Session) bool) session.Session {
	t.Helper()
	var last session.Session
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		for _, s := range reg.Snapshot() {
			last = s
			if s.Summary != nil && ok(s) {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("summary never matched; last = %+v", last.Summary)
	return session.Session{}
}

func settle(d *discoverer, id string) {
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled"}`))
}

func TestSpawnReportsUsage(t *testing.T) {
	_, reg, _ := spawnWithStats(t, &fakePi{t: t, stats: fullStats(0.45, 84000, 42)})
	waitSummary(t, reg, func(s session.Session) bool {
		m := s.Summary
		return m.HasContext && m.Tokens == 84000 && m.ContextPct == 42 && m.HasCost && m.CostUSD == 0.45
	})
}

func TestUsageWithoutContextKeepsCost(t *testing.T) {
	f := &fakePi{t: t, stats: fullStats(0.45, 84000, 42)}
	d, reg, id := spawnWithStats(t, f)
	f.mu.Lock()
	f.stats = map[string]any{"cost": 0, "contextUsage": map[string]any{"tokens": nil, "contextWindow": 200000, "percent": nil}}
	f.mu.Unlock()
	settle(d, id)
	waitSummary(t, reg, func(s session.Session) bool {
		m := s.Summary
		return !m.HasContext && m.Tokens == 0 && m.HasCost && m.CostUSD == 0
	})
}

func TestUsageStatsErrorKeepsCard(t *testing.T) {
	f := &fakePi{t: t, statsFail: true, state: map[string]any{"model": map[string]any{"id": "m1", "reasoning": false}}}
	_, reg, id := spawnWithStats(t, f)
	s := snapshotByID(t, reg, id)
	if s.Summary == nil || s.Summary.ModelName != "m1" || s.Summary.HasContext || s.Summary.HasCost {
		t.Fatalf("summary = %+v", s.Summary)
	}
}

func TestUsageFollowsNewSession(t *testing.T) {
	f := &fakePi{t: t, stats: fullStats(0.45, 84000, 42)}
	d, reg, id := spawnWithStats(t, f)
	f.mu.Lock()
	f.state["sessionId"] = "ses-2"
	f.stats = fullStats(0.01, 1000, 0.5)
	f.mu.Unlock()
	settle(d, id)
	waitSummary(t, reg, func(s session.Session) bool {
		return s.AgentSessionID == "ses-2" && s.Summary.CostUSD == 0.01 && s.Summary.Tokens == 1000
	})
}

func TestUsageRefreshesAfterManualCompact(t *testing.T) {
	f := &fakePi{t: t, stats: fullStats(0.45, 84000, 42)}
	d, reg, id := spawnWithStats(t, f)
	f.mu.Lock()
	f.stats = map[string]any{"cost": 0.5, "contextUsage": map[string]any{"tokens": nil, "contextWindow": 200000, "percent": nil}}
	f.mu.Unlock()
	p := d.get(id)
	if err := d.compact(context.Background(), p, ""); err != nil {
		t.Fatal(err)
	}
	waitSummary(t, reg, func(s session.Session) bool {
		return !s.Summary.HasContext && s.Summary.CostUSD == 0.5
	})
}

func TestUsageKeptOnANullStatsReply(t *testing.T) {
	f := &fakePi{t: t, stats: fullStats(0.45, 84000, 42), state: map[string]any{"model": map[string]any{"id": "m1"}}}
	d, reg, id := spawnWithStats(t, f)
	f.mu.Lock()
	f.stats = nil
	f.state["model"] = map[string]any{"id": "m2"}
	f.mu.Unlock()
	settle(d, id)
	s := waitSummary(t, reg, func(s session.Session) bool { return s.Summary.ModelName == "m2" })
	if !s.Summary.HasContext || s.Summary.Tokens != 84000 || s.Summary.CostUSD != 0.45 {
		t.Fatalf("summary = %+v, want the last usage kept", s.Summary)
	}
}
