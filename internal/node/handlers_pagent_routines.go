package node

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
)

const (
	pausedByYou = "you"

	routineActive    = "active"
	routinePaused    = "paused"
	routineDisabled  = "disabled"
	routineCompleted = "completed"
	routineInvalid   = "invalid"

	routineRunsDefault = 20
)

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// routineInfo renders one routine file and its state.
func (d *Node) routineInfo(a pagent.Agent, set pagent.Settings, slug string, f routineFile, st *pagent.RoutineState) api.PAgentRoutineInfo {
	info := api.PAgentRoutineInfo{AgentID: a.ID, Slug: slug}
	err := f.err
	if err == nil {
		err = routine.Validate(f.r, routine.Limits{MinInterval: set.Limits.Limits.MinInterval})
	}
	r := f.r
	if f.err == nil {
		info.What, info.Does, info.Until, info.Outcome, info.Verify, info.ReportTo = r.What, r.Does, r.Until, r.Outcome, r.Verify, r.ReportTo
		for _, s := range r.Starts {
			info.Starts = append(info.Starts, routine.Describe(s))
		}
		info.Budget = api.PAgentRoutineBudget{Sessions: int64(r.Budget.Sessions), Cost: r.Budget.Cost, Tokens: r.Budget.Tokens}
		info.Webhook = webhookStart(r)
	}
	if st != nil {
		info.PausedReason = st.PausedReason
		info.LastRunAt, info.LastResult = timePtr(st.LastRunAt), st.LastResult
		info.ConsecutiveFailures = st.ConsecutiveFailures
		info.Used = api.PAgentRoutineBudget{Sessions: st.SessionsUsed, Cost: st.CostUsed, Tokens: st.TokensUsed}
		next := st.NextDueAt
		if f.err == nil && selfPaced(r) && !st.CheckBackAt.IsZero() && (next.IsZero() || st.CheckBackAt.Before(next)) {
			next = st.CheckBackAt
		}
		info.NextDue = timePtr(next)
	}
	switch {
	case err != nil:
		info.State, info.Error, info.NextDue = routineInvalid, err.Error(), nil
	case !r.Enabled:
		info.State, info.NextDue = routineDisabled, nil
	case st != nil && !st.Enabled && st.PausedReason == pausedCompleted:
		info.State, info.NextDue = routineCompleted, nil
	case st != nil && !st.Enabled:
		info.State, info.NextDue = routinePaused, nil
	default:
		info.State = routineActive
	}
	return info
}

func (d *Node) routineStates(ctx context.Context, agentID string) map[string]*pagent.RoutineState {
	out := map[string]*pagent.RoutineState{}
	if sts, err := d.pagents.RoutineStates(ctx, agentID); err == nil {
		for i := range sts {
			out[sts[i].Slug] = &sts[i]
		}
	}
	return out
}

func (d *Node) handlePAgentRoutineList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	set := d.pagentSettings(a)
	files := d.routineFiles(a)
	states := d.routineStates(ctx, a.ID)
	res := api.PAgentRoutineListResult{Routines: []api.PAgentRoutineInfo{}}
	for _, slug := range slices.Sorted(maps.Keys(files)) {
		res.Routines = append(res.Routines, d.routineInfo(a, set, slug, files[slug], states[slug]))
	}
	return res, nil
}

// routineTarget resolves the agent and routine file of a routine RPC.
func (d *Node) routineTarget(ctx context.Context, params json.RawMessage) (pagent.Agent, routineFile, error) {
	p, err := api.Decode[api.PAgentRoutineRef](params)
	if err != nil {
		return pagent.Agent{}, routineFile{}, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return pagent.Agent{}, routineFile{}, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return a, routineFile{}, err
	}
	f, ok := d.routineFiles(a)[p.Slug]
	if !ok {
		return a, f, pagentErr("routine %q not found", p.Slug)
	}
	if f.err != nil {
		return a, f, pagentErr("routine %q is invalid: %v", p.Slug, f.err)
	}
	return a, f, nil
}

func (d *Node) routineInfoOf(ctx context.Context, a pagent.Agent, f routineFile) api.PAgentRoutineInfo {
	return d.routineInfo(a, d.pagentSettings(a), f.r.Slug, f, d.routineStates(ctx, a.ID)[f.r.Slug])
}

// runCtx is the context runs started outside the scheduler live on.
func (d *Node) runCtx() context.Context {
	d.routineMu.Lock()
	defer d.routineMu.Unlock()
	if d.routineCtx != nil {
		return d.routineCtx
	}
	return context.Background()
}

func (d *Node) handlePAgentRoutineRun(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	if !a.ArchivedAt.IsZero() {
		return nil, pagentErr("agent %q is archived", a.Name)
	}
	if err := routine.Validate(f.r, routine.Limits{MinInterval: d.pagentSettings(a).Limits.Limits.MinInterval}); err != nil {
		return nil, pagentErr("routine %q is invalid: %v", f.r.Slug, err)
	}
	active, err := d.pagents.ActiveRuns(ctx, a.ID, f.r.Slug)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if len(active) > 0 {
		return nil, pagentErr("routine %q already has a run in progress", f.r.Slug)
	}
	run, ok, err := d.pagents.ClaimOccurrence(ctx, a.ID, f.r.Slug, d.pagentNow(), startedByYou, true)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if !ok {
		return nil, pagentErr("a run was just started; try again")
	}
	d.startRoutineRun(d.runCtx(), a, f.r, run)
	d.notifyConns(api.MethodPAgentChanged)
	return api.PAgentRoutineRunResult{RunID: run.ID}, nil
}

// ensureRoutineState returns the routine's state, creating it as the
// scheduler would when it has not seen the routine yet.
func (d *Node) ensureRoutineState(ctx context.Context, a pagent.Agent, f routineFile) (pagent.RoutineState, error) {
	if st, ok := d.routineState(ctx, a.ID, f.r.Slug); ok {
		return st, nil
	}
	now := d.pagentNow()
	next, _ := routine.NextDue(f.r, now, now, routineLoc(f.r, d.pagentSettings(a)))
	st := pagent.RoutineState{AgentID: a.ID, Slug: f.r.Slug, DefHash: f.hash, Enabled: true, NextDueAt: next, AnchorAt: now}
	if selfPaced(f.r) {
		st.CheckBackAt = now.Add(clampCheckBack(d.pagentSettings(a), 0))
	}
	return st, d.pagents.UpsertRoutineState(ctx, st)
}

func (d *Node) handlePAgentRoutinePause(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	st, err := d.ensureRoutineState(ctx, a, f)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if st.Enabled {
		if err := d.pagents.SetRoutineEnabled(ctx, a.ID, f.r.Slug, false, pausedByYou); err != nil {
			return nil, pagentErr("%v", err)
		}
	}
	d.notifyConns(api.MethodPAgentChanged)
	return d.routineInfoOf(ctx, a, f), nil
}

func (d *Node) handlePAgentRoutineResume(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	if !f.r.Enabled {
		return nil, pagentErr("routine %q has enabled: false in its file; edit the file to turn it on", f.r.Slug)
	}
	st, err := d.ensureRoutineState(ctx, a, f)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if st.Enabled {
		return d.routineInfoOf(ctx, a, f), nil
	}
	if st.PausedReason == pausedCompleted {
		return nil, pagentErr("goal %q is complete and can't be resumed; to work on it again, change its outcome or create a new goal", f.r.Slug)
	}
	// Due times from the pause are dropped: the routine resumes from now.
	now := d.pagentNow()
	loc := routineLoc(f.r, d.pagentSettings(a))
	next, _ := routine.NextDue(f.r, now, st.AnchorAt, loc)
	if err := d.pagents.AdvanceNextDue(ctx, a.ID, f.r.Slug, next); err != nil {
		return nil, pagentErr("%v", err)
	}
	if selfPaced(f.r) && !st.CheckBackAt.After(now) {
		if err := d.pagents.SetCheckBackAt(ctx, a.ID, f.r.Slug, now.Add(clampCheckBack(d.pagentSettings(a), 0))); err != nil {
			return nil, pagentErr("%v", err)
		}
	}
	if err := d.pagents.SetRoutineEnabled(ctx, a.ID, f.r.Slug, true, ""); err != nil {
		return nil, pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	return d.routineInfoOf(ctx, a, f), nil
}

func (d *Node) handlePAgentRoutineRuns(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentRoutineRunsParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	if !routine.ValidSlug(p.Slug) {
		return nil, pagentErr("routine %q not found", p.Slug)
	}
	runs, err := s.RunsSince(ctx, a.ID, p.Slug, time.Time{})
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	limit := p.Limit
	if limit <= 0 {
		limit = routineRunsDefault
	}
	res := api.PAgentRoutineRunsResult{Runs: []api.PAgentRoutineRunInfo{}}
	for i := len(runs) - 1; i >= 0 && len(res.Runs) < limit; i-- {
		r := runs[i]
		occ := r.Occurrence
		if r.RetryOf != "" {
			occ = occ.Add(-time.Nanosecond)
		}
		res.Runs = append(res.Runs, api.PAgentRoutineRunInfo{
			ID: r.ID, Slug: r.Slug, StartedBy: r.StartedBy, Test: r.Test, State: r.State, ThreadID: r.ThreadID,
			Occurrence: occ, StartedAt: r.StartedAt, EndedAt: timePtr(r.EndedAt),
			Result: r.Result, Summary: r.Summary, ActionsDone: r.ActionsDone, ActionsPending: r.ActionsPending,
			Unverified: r.Unverified, Sources: r.Sources, Cost: r.Cost, Tokens: r.Tokens, Error: r.Error,
		})
	}
	return res, nil
}

func (d *Node) handlePAgentRoutineHookSecret(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	if !webhookStart(f.r) {
		return nil, pagentErr("routine %q has no webhook start (starts: - event: webhook)", f.r.Slug)
	}
	secret, err := d.rotateWebhookSecret(ctx, a.ID, f.r.Slug)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	return api.PAgentRoutineHookSecretResult{
		Secret: secret, Path: webhookPrefix + a.Name + "/" + f.r.Slug, Listen: d.pagentCfg.WebhookListen,
	}, nil
}

// routineProposalSummary says in words what a routine proposal changes.
func routineProposalSummary(a pagent.Agent, body memoryProposal) string {
	r, err := routine.Parse(body.Slug, []byte(body.Text))
	if err != nil {
		return "change routine " + body.Slug
	}
	kind := "routine"
	if r.Until == untilOutcome {
		kind = "goal"
	}
	data, err := os.ReadFile(filepath.Join(a.Dir, routinesDir, body.Slug+".yaml"))
	if err != nil {
		return fmt.Sprintf("create %s %s: %s", kind, body.Slug, strings.TrimSpace(r.Outcome))
	}
	old, err := routine.Parse(body.Slug, data)
	if err != nil {
		return fmt.Sprintf("replace %s %s", kind, body.Slug)
	}
	var fields []string
	if old.Until != r.Until {
		fields = append(fields, "until")
	}
	if old.Outcome != r.Outcome {
		fields = append(fields, "outcome")
	}
	if old.Verify != r.Verify {
		fields = append(fields, "verify")
	}
	if len(fields) == 0 {
		return fmt.Sprintf("change %s %s", kind, body.Slug)
	}
	return fmt.Sprintf("change %s %s: %s", kind, body.Slug, strings.Join(fields, ", "))
}
