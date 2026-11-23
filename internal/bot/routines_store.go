package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/db/gen"
)

// Run states.
const (
	RunClaimed     = "claimed"
	RunRunning     = "running"
	RunDone        = "done"
	RunFailed      = "failed"
	RunSkipped     = "skipped"
	RunMissed      = "missed"
	RunInterrupted = "interrupted"
	// RunQueued waits for the routine's active run to end (overlap: queue).
	RunQueued = "queued"
)

// ClaimLease is how long a freshly claimed run holds its lease before a thread starts renewing it.
const ClaimLease = 2 * time.Minute

// RoutineState is the runtime state of one routine on its home node.
type RoutineState struct {
	BotID, Slug, DefHash     string
	Enabled                  bool
	NextDueAt, LastRunAt     time.Time
	LastResult, PausedReason string
	ConsecutiveFailures      int
	SessionsUsed, TokensUsed int64
	CostUsed                 float64
	CheckBackAt, AnchorAt    time.Time
	UpdatedAt                time.Time
}

// RoutineRun is one occurrence of a routine.
type RoutineRun struct {
	ID, BotID, Slug, StartedBy, State, ThreadID string
	Occurrence                                  time.Time
	Test                                        bool
	LeaseUntil, StartedAt, EndedAt              time.Time
	// RetryOf is the id of the interrupted run this run retries.
	RetryOf string
	RunResult
	Error string
}

// RunResult is the structured result a run reports.
type RunResult struct {
	Result, Summary                         string
	ActionsDone, ActionsPending, Unverified []string
	Sources                                 []string
	Cost                                    float64
	Tokens                                  int64
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func stateFrom(r gen.BotRoutineState) RoutineState {
	return RoutineState{
		BotID: r.BotID, Slug: r.Slug, DefHash: r.DefHash, Enabled: r.Enabled != 0,
		NextDueAt: fromNanos(r.NextDueAt), LastRunAt: fromNanos(r.LastRunAt), LastResult: r.LastResult,
		ConsecutiveFailures: int(r.ConsecutiveFailures), PausedReason: r.PausedReason,
		SessionsUsed: r.SessionsUsed, CostUsed: r.CostUsed, TokensUsed: r.TokensUsed,
		CheckBackAt: fromNanos(r.CheckBackAt), AnchorAt: fromNanos(r.AnchorAt), UpdatedAt: fromNanos(r.UpdatedAt),
	}
}

func decodeList(s string) []string {
	var out []string
	if json.Unmarshal([]byte(s), &out) != nil || out == nil {
		return []string{}
	}
	return out
}

func encodeList(l []string) string {
	if l == nil {
		return "[]"
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func runFrom(r gen.BotRoutineRun) RoutineRun {
	return RoutineRun{
		ID: r.ID, BotID: r.BotID, Slug: r.Slug, StartedBy: r.StartedBy, State: r.State, ThreadID: r.ThreadID,
		Occurrence: fromNanos(r.Occurrence), Test: r.Test != 0,
		LeaseUntil: fromNanos(r.LeaseUntil), StartedAt: fromNanos(r.StartedAt), EndedAt: fromNanos(r.EndedAt),
		RunResult: RunResult{
			Result: r.Result, Summary: r.Summary,
			ActionsDone: decodeList(r.ActionsDone), ActionsPending: decodeList(r.ActionsPending),
			Unverified: decodeList(r.Unverified), Sources: decodeList(r.Sources),
			Cost: r.Cost, Tokens: r.Tokens,
		},
		Error: r.Error, RetryOf: r.RetryOf,
	}
}

func runs(rows []gen.BotRoutineRun, err error) ([]RoutineRun, error) {
	if err != nil {
		return nil, err
	}
	out := make([]RoutineRun, len(rows))
	for i, r := range rows {
		out[i] = runFrom(r)
	}
	return out, nil
}

// UpsertRoutineState writes st in full, setting UpdatedAt.
func (s *Store) UpsertRoutineState(ctx context.Context, st RoutineState) error {
	return s.q.UpsertBotRoutineState(ctx, gen.UpsertBotRoutineStateParams{
		BotID: st.BotID, Slug: st.Slug, DefHash: st.DefHash, Enabled: b2i(st.Enabled),
		NextDueAt: nanos(st.NextDueAt), LastRunAt: nanos(st.LastRunAt), LastResult: st.LastResult,
		ConsecutiveFailures: int64(st.ConsecutiveFailures), PausedReason: st.PausedReason,
		SessionsUsed: st.SessionsUsed, CostUsed: st.CostUsed, TokensUsed: st.TokensUsed,
		CheckBackAt: nanos(st.CheckBackAt), AnchorAt: nanos(st.AnchorAt), UpdatedAt: s.now().UnixNano(),
	})
}

// RoutineStates lists the bot's routine states ordered by slug.
func (s *Store) RoutineStates(ctx context.Context, botID string) ([]RoutineState, error) {
	rows, err := s.q.ListBotRoutineStates(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := make([]RoutineState, len(rows))
	for i, r := range rows {
		out[i] = stateFrom(r)
	}
	return out, nil
}

// SetRoutineEnabled pauses (enabled false, with reason) or resumes the routine.
func (s *Store) SetRoutineEnabled(ctx context.Context, botID, slug string, enabled bool, reason string) error {
	return s.q.SetBotRoutineEnabled(ctx, gen.SetBotRoutineEnabledParams{
		Enabled: b2i(enabled), PausedReason: reason, UpdatedAt: s.now().UnixNano(), BotID: botID, Slug: slug,
	})
}

// SetRoutineDef records a new definition hash and the next due time it gives.
func (s *Store) SetRoutineDef(ctx context.Context, botID, slug, defHash string, nextDue time.Time) error {
	return s.q.SetBotRoutineDef(ctx, gen.SetBotRoutineDefParams{
		DefHash: defHash, NextDueAt: nanos(nextDue), UpdatedAt: s.now().UnixNano(), BotID: botID, Slug: slug,
	})
}

// AdvanceNextDue sets the routine's next due time.
func (s *Store) AdvanceNextDue(ctx context.Context, botID, slug string, nextDue time.Time) error {
	return s.q.AdvanceBotRoutineNextDue(ctx, gen.AdvanceBotRoutineNextDueParams{
		NextDueAt: nanos(nextDue), UpdatedAt: s.now().UnixNano(), BotID: botID, Slug: slug,
	})
}

func (s *Store) DeleteRoutineState(ctx context.Context, botID, slug string) error {
	return s.q.DeleteBotRoutineState(ctx, gen.DeleteBotRoutineStateParams{BotID: botID, Slug: slug})
}

// ClaimOccurrence inserts a claimed run for the occurrence. ok is false when
// the occurrence was already claimed.
func (s *Store) ClaimOccurrence(ctx context.Context, botID, slug string, occurrence time.Time, startedBy string, test bool) (RoutineRun, bool, error) {
	return s.claim(ctx, s.q, botID, slug, occurrence, startedBy, test, "")
}

// ClaimRetry claims the one retry of an interrupted run. Its occurrence is
// the original's plus 1ns, so a second retry of the same run is a duplicate.
func (s *Store) ClaimRetry(ctx context.Context, prev RoutineRun) (RoutineRun, bool, error) {
	return s.claim(ctx, s.q, prev.BotID, prev.Slug, prev.Occurrence.Add(time.Nanosecond), prev.StartedBy, prev.Test, prev.ID)
}

// ClaimOccurrenceAdvance claims the occurrence and sets the routine's
// next_due_at in one transaction. Nothing changes when the claim is a duplicate.
func (s *Store) ClaimOccurrenceAdvance(ctx context.Context, botID, slug string, occurrence time.Time, startedBy string, test bool, nextDue time.Time) (RoutineRun, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RoutineRun{}, false, fmt.Errorf("bot: claim: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	run, ok, err := s.claim(ctx, q, botID, slug, occurrence, startedBy, test, "")
	if err != nil || !ok {
		return run, ok, err
	}
	if err := q.AdvanceBotRoutineNextDue(ctx, gen.AdvanceBotRoutineNextDueParams{
		NextDueAt: nanos(nextDue), UpdatedAt: s.now().UnixNano(), BotID: botID, Slug: slug,
	}); err != nil {
		return RoutineRun{}, false, err
	}
	return run, true, tx.Commit()
}

func (s *Store) claim(ctx context.Context, q *gen.Queries, botID, slug string, occurrence time.Time, startedBy string, test bool, retryOf string) (RoutineRun, bool, error) {
	now := s.now()
	id := "rr_" + NewID()[3:]
	n, err := q.InsertBotRoutineRun(ctx, gen.InsertBotRoutineRunParams{
		ID: id, BotID: botID, Slug: slug, Occurrence: occurrence.UnixNano(), StartedBy: startedBy,
		Test: b2i(test), LeaseUntil: now.Add(ClaimLease).UnixNano(), StartedAt: now.UnixNano(), RetryOf: retryOf,
	})
	if err != nil || n == 0 {
		return RoutineRun{}, false, err
	}
	r, err := q.GetBotRoutineRun(ctx, id)
	if err != nil {
		return RoutineRun{}, false, err
	}
	return runFrom(r), true, nil
}

func (s *Store) RoutineRun(ctx context.Context, id string) (RoutineRun, bool, error) {
	r, err := s.q.GetBotRoutineRun(ctx, id)
	return one(r, err, runFrom)
}

// SetRunState moves a run to state, recording errMsg. Terminal states set EndedAt.
func (s *Store) SetRunState(ctx context.Context, id, state, errMsg string) error {
	var ended int64
	switch state {
	case RunDone, RunFailed, RunSkipped, RunMissed, RunInterrupted:
		ended = s.now().UnixNano()
	}
	return affected(s.q.SetBotRoutineRunState(ctx, gen.SetBotRoutineRunStateParams{State: state, Error: errMsg, EndedAt: ended, ID: id}))
}

// SetRunThread attaches the run thread, marks the run running and extends the lease.
func (s *Store) SetRunThread(ctx context.Context, id, threadID string, leaseUntil time.Time) error {
	return affected(s.q.SetBotRoutineRunThread(ctx, gen.SetBotRoutineRunThreadParams{ThreadID: threadID, LeaseUntil: leaseUntil.UnixNano(), ID: id}))
}

// RenewLease extends the lease of an active run; ErrNotFound when it is no longer active.
func (s *Store) RenewLease(ctx context.Context, id string, leaseUntil time.Time) error {
	return affected(s.q.RenewBotRoutineRunLease(ctx, gen.RenewBotRoutineRunLeaseParams{LeaseUntil: leaseUntil.UnixNano(), ID: id}))
}

// ExpiredLeases lists active runs whose lease ended before now, with or without a recorded result.
func (s *Store) ExpiredLeases(ctx context.Context, now time.Time) ([]RoutineRun, error) {
	return runs(s.q.ExpiredBotRoutineRuns(ctx, now.UnixNano()))
}

// RunsSince lists the routine's runs started at or after t.
func (s *Store) RunsSince(ctx context.Context, botID, slug string, t time.Time) ([]RoutineRun, error) {
	return runs(s.q.BotRoutineRunsSince(ctx, gen.BotRoutineRunsSinceParams{BotID: botID, Slug: slug, StartedAt: t.UnixNano()}))
}

// ActiveRuns lists the routine's claimed, running and queued runs, oldest first.
func (s *Store) ActiveRuns(ctx context.Context, botID, slug string) ([]RoutineRun, error) {
	return runs(s.q.ActiveBotRoutineRuns(ctx, gen.ActiveBotRoutineRunsParams{BotID: botID, Slug: slug}))
}

// LastStartedRun returns the routine's most recent non-test run that started
// (not skipped, missed or queued).
func (s *Store) LastStartedRun(ctx context.Context, botID, slug string) (RoutineRun, bool, error) {
	r, err := s.q.LastStartedBotRoutineRun(ctx, gen.LastStartedBotRoutineRunParams{BotID: botID, Slug: slug})
	return one(r, err, runFrom)
}

// RecordResult stores the structured result on the run.
func (s *Store) RecordResult(ctx context.Context, id string, r RunResult) error {
	return affected(s.q.RecordBotRoutineRunResult(ctx, gen.RecordBotRoutineRunResultParams{
		Result: r.Result, Summary: r.Summary, ActionsDone: encodeList(r.ActionsDone),
		ActionsPending: encodeList(r.ActionsPending), Unverified: encodeList(r.Unverified),
		Sources: encodeList(r.Sources), Cost: r.Cost, Tokens: r.Tokens, ID: id,
	}))
}

// FinishRun stores the run's result and marks it done with errMsg in one
// transaction, so an active run never holds a result. With outcome set it
// also records the outcome on the routine's state and returns that state; ok
// is false when the routine has no state. ErrNotFound means the run is no
// longer active and nothing changed.
func (s *Store) FinishRun(ctx context.Context, botID, slug, id string, r RunResult, errMsg string, outcome *RunOutcome) (st RoutineState, ok bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return st, false, fmt.Errorf("bot: finish run: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	if err := affected(q.RecordBotRoutineRunResult(ctx, gen.RecordBotRoutineRunResultParams{
		Result: r.Result, Summary: r.Summary, ActionsDone: encodeList(r.ActionsDone),
		ActionsPending: encodeList(r.ActionsPending), Unverified: encodeList(r.Unverified),
		Sources: encodeList(r.Sources), Cost: r.Cost, Tokens: r.Tokens, ID: id,
	})); err != nil {
		return st, false, err
	}
	now := s.now().UnixNano()
	if err := affected(q.SetBotRoutineRunState(ctx, gen.SetBotRoutineRunStateParams{State: RunDone, Error: errMsg, EndedAt: now, ID: id})); err != nil {
		return st, false, err
	}
	if outcome != nil {
		row, err := q.RecordBotRoutineOutcome(ctx, gen.RecordBotRoutineOutcomeParams{
			LastRunAt: now, LastResult: outcome.Result, Failed: b2i(outcome.Failed), Sessions: outcome.Sessions,
			Cost: outcome.Cost, Tokens: outcome.Tokens, UpdatedAt: now, BotID: botID, Slug: slug,
		})
		switch {
		case err == nil:
			st, ok = stateFrom(row), true
		case !errors.Is(err, sql.ErrNoRows):
			return st, false, err
		}
	}
	return st, ok, tx.Commit()
}

// RunsLastHour counts the bot's non-test runs started in the past hour, excluding skipped and missed ones.
func (s *Store) RunsLastHour(ctx context.Context, botID string) (int, error) {
	n, err := s.q.CountBotRoutineRunsSince(ctx, gen.CountBotRoutineRunsSinceParams{
		BotID: botID, StartedAt: s.now().Add(-time.Hour).UnixNano(),
	})
	return int(n), err
}

// SetWebhookSecretHash stores the hash of the routine's webhook secret, replacing any previous one.
func (s *Store) SetWebhookSecretHash(ctx context.Context, botID, slug, hash string) error {
	return s.q.UpsertBotWebhookSecret(ctx, gen.UpsertBotWebhookSecretParams{BotID: botID, Slug: slug, SecretHash: hash, CreatedAt: s.now().UnixNano()})
}

func (s *Store) WebhookSecretHash(ctx context.Context, botID, slug string) (string, bool, error) {
	h, err := s.q.GetBotWebhookSecretHash(ctx, gen.GetBotWebhookSecretHashParams{BotID: botID, Slug: slug})
	return one(h, err, func(h string) string { return h })
}

// InterruptRun marks an active run interrupted when its lease is still
// expired at now and it has no result. ok is false when the run was renewed,
// ended or reported meanwhile.
func (s *Store) InterruptRun(ctx context.Context, id string, now time.Time, errMsg string) (bool, error) {
	n, err := s.q.InterruptBotRoutineRun(ctx, gen.InterruptBotRoutineRunParams{
		Error: errMsg, EndedAt: s.now().UnixNano(), ID: id, Now: now.UnixNano(),
	})
	return n > 0, err
}

// RunOutcome is what one finished run adds to its routine's state.
type RunOutcome struct {
	Result   string
	Failed   bool
	Sessions int64
	Cost     float64
	Tokens   int64
}

// RecordRunOutcome records a finished run on the routine's state: the last
// result, the failure streak (reset by a non-failure) and the budget used. It
// returns the updated state.
func (s *Store) RecordRunOutcome(ctx context.Context, botID, slug string, o RunOutcome) (RoutineState, error) {
	now := s.now().UnixNano()
	r, err := s.q.RecordBotRoutineOutcome(ctx, gen.RecordBotRoutineOutcomeParams{
		LastRunAt: now, LastResult: o.Result, Failed: b2i(o.Failed), Sessions: o.Sessions,
		Cost: o.Cost, Tokens: o.Tokens, UpdatedAt: now, BotID: botID, Slug: slug,
	})
	if err != nil {
		return RoutineState{}, err
	}
	return stateFrom(r), nil
}

// AddRunUsage adds usage reported after a run finished to the run and, with
// toState, to its routine's budget use.
func (s *Store) AddRunUsage(ctx context.Context, botID, slug, id string, cost float64, tokens int64, toState bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bot: add run usage: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	if err := affected(q.AddBotRoutineRunUsage(ctx, gen.AddBotRoutineRunUsageParams{Cost: cost, Tokens: tokens, ID: id})); err != nil {
		return err
	}
	if toState {
		if err := q.AddBotRoutineUsage(ctx, gen.AddBotRoutineUsageParams{
			Cost: cost, Tokens: tokens, UpdatedAt: s.now().UnixNano(), BotID: botID, Slug: slug,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetCheckBackAt sets when the routine's next self-paced check-back is due.
func (s *Store) SetCheckBackAt(ctx context.Context, botID, slug string, t time.Time) error {
	return s.q.SetBotRoutineCheckBack(ctx, gen.SetBotRoutineCheckBackParams{
		CheckBackAt: nanos(t), UpdatedAt: s.now().UnixNano(), BotID: botID, Slug: slug,
	})
}

// BotRunsSince lists all of the bot's runs started at or after t.
func (s *Store) BotRunsSince(ctx context.Context, botID string, t time.Time) ([]RoutineRun, error) {
	return runs(s.q.BotRoutineRunsByBotSince(ctx, gen.BotRoutineRunsByBotSinceParams{BotID: botID, StartedAt: t.UnixNano()}))
}
