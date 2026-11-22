package pagent

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestClaimOccurrenceOnce(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	occ := time.Unix(1_700_000_000, 0)
	run, ok, err := s.ClaimOccurrence(ctx, "ag1", "r", occ, "routine", false)
	if err != nil || !ok || run.State != RunClaimed || !run.Occurrence.Equal(occ) || run.StartedBy != "routine" {
		t.Fatalf("first claim: %+v %v %v", run, ok, err)
	}
	if _, ok, err := s.ClaimOccurrence(ctx, "ag1", "r", occ, "routine", false); err != nil || ok {
		t.Fatalf("duplicate claim: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := s.ClaimOccurrence(ctx, "ag1", "r", occ.Add(time.Minute), "routine", false); !ok {
		t.Fatal("different occurrence should claim")
	}
	if _, ok, _ := s.ClaimOccurrence(ctx, "ag1", "other", occ, "you", true); !ok {
		t.Fatal("different slug should claim")
	}
}

func TestClaimOccurrenceAdvance(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	occ := time.Unix(1_700_000_000, 0)
	if err := s.UpsertRoutineState(ctx, RoutineState{AgentID: "ag1", Slug: "r", Enabled: true, NextDueAt: occ, AnchorAt: occ}); err != nil {
		t.Fatal(err)
	}
	next := occ.Add(time.Hour)
	if _, ok, err := s.ClaimOccurrenceAdvance(ctx, "ag1", "r", occ, "routine", false, next); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, _ := s.ClaimOccurrenceAdvance(ctx, "ag1", "r", occ, "routine", false, next.Add(time.Hour)); ok {
		t.Fatal("duplicate")
	}
	sts, _ := s.RoutineStates(ctx, "ag1")
	if len(sts) != 1 || !sts[0].NextDueAt.Equal(next) {
		t.Fatalf("next due should be unchanged by duplicate: %+v", sts)
	}
}

func TestRoutineStateUpsert(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	anchor := time.Unix(1_700_000_000, 0)
	in := RoutineState{AgentID: "ag1", Slug: "r", DefHash: "h", Enabled: true, NextDueAt: anchor.Add(time.Hour),
		LastResult: "progress", ConsecutiveFailures: 2, PausedReason: "", SessionsUsed: 3, CostUsed: 1.25, TokensUsed: 99, AnchorAt: anchor}
	if err := s.UpsertRoutineState(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Enabled, in.PausedReason, in.SessionsUsed = false, "budget", 4
	if err := s.UpsertRoutineState(ctx, in); err != nil {
		t.Fatal(err)
	}
	s.UpsertRoutineState(ctx, RoutineState{AgentID: "ag2", Slug: "x"})
	got, err := s.RoutineStates(ctx, "ag1")
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	g := got[0]
	if g.Enabled || g.PausedReason != "budget" || g.SessionsUsed != 4 || g.CostUsed != 1.25 || !g.AnchorAt.Equal(anchor) || g.UpdatedAt.IsZero() {
		t.Fatalf("%+v", g)
	}
	if err := s.DeleteRoutineState(ctx, "ag1", "r"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.RoutineStates(ctx, "ag1"); len(got) != 0 {
		t.Fatal("not deleted")
	}
}

func TestExpiredLeases(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	base := time.Unix(1_700_000_000, 0)
	mk := func(slug string) RoutineRun {
		r, ok, err := s.ClaimOccurrence(ctx, "ag1", slug, base, "routine", false)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		return r
	}
	stale, renewed, withResult, finished := mk("stale"), mk("renewed"), mk("result"), mk("finished")
	for _, r := range []RoutineRun{stale, renewed, withResult, finished} {
		if err := s.SetRunThread(ctx, r.ID, "th", base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RenewLease(ctx, renewed.ID, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordResult(ctx, withResult.ID, RunResult{Result: "progress", Summary: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunState(ctx, finished.ID, RunDone, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewLease(ctx, finished.ID, base.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renew finished: %v", err)
	}
	got, err := s.ExpiredLeases(ctx, base.Add(10*time.Minute))
	if err != nil || len(got) != 1 || got[0].ID != stale.ID {
		t.Fatalf("expired: %+v %v", got, err)
	}
	if err := s.SetRunState(ctx, stale.ID, RunInterrupted, "node restart"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ExpiredLeases(ctx, base.Add(10*time.Minute)); len(got) != 0 {
		t.Fatalf("interrupted run must not reappear: %+v", got)
	}
	r, ok, _ := s.RoutineRun(ctx, stale.ID)
	if !ok || r.State != RunInterrupted || r.Error != "node restart" || r.EndedAt.IsZero() || r.ThreadID != "th" {
		t.Fatalf("%+v", r)
	}
}

func TestRecordResultRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	r, _, _ := s.ClaimOccurrence(ctx, "ag1", "r", time.Unix(1, 0), "you", true)
	in := RunResult{Result: "done", Summary: "all good", ActionsDone: []string{"a", "b"}, ActionsPending: []string{}, Unverified: []string{"u"}, Sources: []string{"s"}, Cost: 0.5, Tokens: 10}
	if err := s.RecordResult(ctx, r.ID, in); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.RoutineRun(ctx, r.ID)
	if !reflect.DeepEqual(got.RunResult, in) || !got.Test {
		t.Fatalf("%+v", got)
	}
	if err := s.RecordResult(ctx, "missing", in); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestRunsLastHour(t *testing.T) {
	ctx := context.Background()
	sqlStore, _ := newStore(t)
	// each now() call advances the clock by 1s; use a controllable clock instead
	cur := time.Unix(1_700_000_000, 0)
	sqlStore.now = func() time.Time { return cur }
	claim := func(agent, slug string, occ int64, by string, test bool) RoutineRun {
		r, ok, err := sqlStore.ClaimOccurrence(ctx, agent, slug, time.Unix(occ, 0), by, test)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		return r
	}
	claim("ag1", "r", 1, "routine", false) // old, outside window
	cur = cur.Add(2 * time.Hour)
	claim("ag1", "r", 2, "routine", false)
	claim("ag1", "r", 3, "routine", false)
	claim("ag1", "r", 4, "you", true) // test: excluded
	sk := claim("ag1", "r", 5, "routine", false)
	sqlStore.SetRunState(ctx, sk.ID, RunSkipped, "overlap")
	claim("ag2", "r", 2, "routine", false)
	cur = cur.Add(30 * time.Minute)
	n, err := sqlStore.RunsLastHour(ctx, "ag1")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, _ := sqlStore.RunsSince(ctx, "ag1", "r", cur.Add(-time.Hour))
	if len(got) != 4 {
		t.Fatalf("since: %d", len(got))
	}
}

func TestWebhookSecretHash(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	if _, ok, _ := s.WebhookSecretHash(ctx, "ag1", "r"); ok {
		t.Fatal("unexpected")
	}
	s.SetWebhookSecretHash(ctx, "ag1", "r", "h1")
	s.SetWebhookSecretHash(ctx, "ag1", "r", "h2")
	if h, ok, _ := s.WebhookSecretHash(ctx, "ag1", "r"); !ok || h != "h2" {
		t.Fatal(h, ok)
	}
}

func TestPurgeAgentRemovesRoutineData(t *testing.T) {
	ctx := context.Background()
	s, sqlDB := newStore(t)
	s.UpsertRoutineState(ctx, RoutineState{AgentID: "ag1", Slug: "r"})
	s.ClaimOccurrence(ctx, "ag1", "r", time.Unix(1, 0), "you", false)
	s.SetWebhookSecretHash(ctx, "ag1", "r", "h")
	if err := s.PurgeAgent(ctx, "ag1"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"pagent_routine_state", "pagent_routine_run", "pagent_webhook_secret"} {
		var n int
		sqlDB.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n)
		if n != 0 {
			t.Errorf("%s not purged", tbl)
		}
	}
}
