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

func TestClaimRetryOnceAndActiveRuns(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	occ := time.Unix(1_700_000_000, 0)
	run, _, _ := s.ClaimOccurrence(ctx, "ag1", "r", occ, "routine", false)
	if err := s.SetRunState(ctx, run.ID, RunInterrupted, "lease expired"); err != nil {
		t.Fatal(err)
	}
	retry, ok, err := s.ClaimRetry(ctx, run)
	if err != nil || !ok || retry.RetryOf != run.ID || retry.StartedBy != "routine" {
		t.Fatalf("retry: %+v %v %v", retry, ok, err)
	}
	if _, ok, _ := s.ClaimRetry(ctx, run); ok {
		t.Fatal("second retry claimed")
	}
	q, _, _ := s.ClaimOccurrence(ctx, "ag1", "r", occ.Add(time.Hour), "routine", false)
	_ = s.SetRunState(ctx, q.ID, RunQueued, "")
	active, err := s.ActiveRuns(ctx, "ag1", "r")
	if err != nil || len(active) != 2 || active[0].ID != retry.ID || active[1].State != RunQueued {
		t.Fatalf("active: %+v %v", active, err)
	}
	last, ok, err := s.LastStartedRun(ctx, "ag1", "r")
	if err != nil || !ok || last.ID != retry.ID {
		t.Fatalf("last started: %+v %v %v", last, ok, err)
	}
}

func TestRoutineStatePartialUpdates(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	anchor := time.Unix(1_700_000_000, 0)
	_ = s.UpsertRoutineState(ctx, RoutineState{AgentID: "ag1", Slug: "r", DefHash: "h", Enabled: true, AnchorAt: anchor, ConsecutiveFailures: 3})
	if err := s.SetRoutineEnabled(ctx, "ag1", "r", false, "away"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRoutineDef(ctx, "ag1", "r", "h2", anchor.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceNextDue(ctx, "ag1", "r", anchor.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st, _ := s.RoutineStates(ctx, "ag1")
	if st[0].Enabled || st[0].PausedReason != "away" || st[0].DefHash != "h2" || !st[0].NextDueAt.Equal(anchor.Add(2*time.Hour)) ||
		!st[0].AnchorAt.Equal(anchor) || st[0].ConsecutiveFailures != 3 {
		t.Fatalf("state: %+v", st[0])
	}
}

func TestSetRunStateGuarded(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	r, _, _ := s.ClaimOccurrence(ctx, "ag1", "r", time.Unix(1, 0), "routine", false)
	if err := s.SetRunState(ctx, r.ID, RunDone, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunState(ctx, r.ID, RunFailed, "late"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late state change: %v", err)
	}
	if err := s.SetRunThread(ctx, r.ID, "th", time.Unix(9, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("thread on finished run: %v", err)
	}
	got, _, _ := s.RoutineRun(ctx, r.ID)
	if got.State != RunDone {
		t.Fatal(got.State)
	}
}

func TestInterruptRunConditional(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	run, _, _ := s.ClaimOccurrence(ctx, "ag1", "r", t0, "routine", false)
	if ok, err := s.InterruptRun(ctx, run.ID, run.LeaseUntil.Add(-time.Second), "lease expired"); err != nil || ok {
		t.Fatalf("lease still held: ok=%v err=%v", ok, err)
	}
	if err := s.RenewLease(ctx, run.ID, run.LeaseUntil.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.InterruptRun(ctx, run.ID, run.LeaseUntil.Add(time.Minute), "lease expired"); ok {
		t.Fatal("renewed lease interrupted")
	}
	if ok, err := s.InterruptRun(ctx, run.ID, run.LeaseUntil.Add(2*time.Hour), "lease expired"); err != nil || !ok {
		t.Fatalf("expired lease: ok=%v err=%v", ok, err)
	}
	if got, _, _ := s.RoutineRun(ctx, run.ID); got.State != RunInterrupted || got.Error != "lease expired" {
		t.Fatalf("run: %+v", got)
	}
	reported, _, _ := s.ClaimOccurrence(ctx, "ag1", "r", t0.Add(time.Hour), "routine", false)
	_ = s.RecordResult(ctx, reported.ID, RunResult{Result: "progress"})
	if ok, _ := s.InterruptRun(ctx, reported.ID, reported.LeaseUntil.Add(time.Hour), "x"); ok {
		t.Fatal("reported run interrupted")
	}
}

func TestRecordRunOutcome(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	_ = s.UpsertRoutineState(ctx, RoutineState{AgentID: "ag1", Slug: "r", Enabled: true, NextDueAt: t0, AnchorAt: t0})
	st, err := s.RecordRunOutcome(ctx, "ag1", "r", RunOutcome{Result: "failed", Failed: true, Sessions: 1, Cost: 0.5, Tokens: 10})
	if err != nil || st.ConsecutiveFailures != 1 || st.SessionsUsed != 1 || st.CostUsed != 0.5 || st.TokensUsed != 10 || st.LastResult != "failed" {
		t.Fatalf("state: %+v %v", st, err)
	}
	st, _ = s.RecordRunOutcome(ctx, "ag1", "r", RunOutcome{Result: "failed", Failed: true, Sessions: 1})
	if st.ConsecutiveFailures != 2 || st.SessionsUsed != 2 || !st.NextDueAt.Equal(t0) {
		t.Fatalf("state: %+v", st)
	}
	st, _ = s.RecordRunOutcome(ctx, "ag1", "r", RunOutcome{Result: "progress", Sessions: 1, Cost: 0.25})
	if st.ConsecutiveFailures != 0 || st.SessionsUsed != 3 || st.CostUsed != 0.75 || st.LastRunAt.IsZero() {
		t.Fatalf("state: %+v", st)
	}
}
