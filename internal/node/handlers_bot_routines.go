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
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/routine"
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
func (d *Node) routineInfo(a bot.Bot, set bot.Settings, slug string, f routineFile, st *bot.RoutineState) api.BotRoutineInfo {
	info := api.BotRoutineInfo{BotID: a.ID, Slug: slug}
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
		info.Budget = api.BotRoutineBudget{Sessions: int64(r.Budget.Sessions), Cost: r.Budget.Cost, Tokens: r.Budget.Tokens}
		info.Webhook = webhookStart(r)
	}
	if st != nil {
		info.PausedReason = st.PausedReason
		info.LastRunAt, info.LastResult = timePtr(st.LastRunAt), st.LastResult
		info.ConsecutiveFailures = st.ConsecutiveFailures
		info.Used = api.BotRoutineBudget{Sessions: st.SessionsUsed, Cost: st.CostUsed, Tokens: st.TokensUsed}
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

func (d *Node) routineStates(ctx context.Context, botID string) map[string]*bot.RoutineState {
	out := map[string]*bot.RoutineState{}
	if sts, err := d.bots.RoutineStates(ctx, botID); err == nil {
		for i := range sts {
			out[sts[i].Slug] = &sts[i]
		}
	}
	return out
}

func (d *Node) handleBotRoutineList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	set := d.botSettings(a)
	files := d.routineFiles(a)
	states := d.routineStates(ctx, a.ID)
	res := api.BotRoutineListResult{Routines: []api.BotRoutineInfo{}}
	for _, slug := range slices.Sorted(maps.Keys(files)) {
		res.Routines = append(res.Routines, d.routineInfo(a, set, slug, files[slug], states[slug]))
	}
	return res, nil
}

// routineTarget resolves the bot and routine file of a routine RPC.
func (d *Node) routineTarget(ctx context.Context, params json.RawMessage) (bot.Bot, routineFile, error) {
	p, err := api.Decode[api.BotRoutineRef](params)
	if err != nil {
		return bot.Bot{}, routineFile{}, err
	}
	s, err := d.botStore()
	if err != nil {
		return bot.Bot{}, routineFile{}, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return a, routineFile{}, err
	}
	f, ok := d.routineFiles(a)[p.Slug]
	if !ok && p.Slug == briefSlug {
		// The built-in brief has no file; it is paused and resumed by slug.
		if set := d.botSettings(a); d.briefWanted(ctx, a, set) {
			f, err = briefRoutine(set)
			if err != nil {
				return a, f, botErr("%v", err)
			}
			ok = true
		}
	}
	if !ok {
		return a, f, botErr("routine %q not found", p.Slug)
	}
	if f.err != nil {
		return a, f, botErr("routine %q is invalid: %v", p.Slug, f.err)
	}
	return a, f, nil
}

func (d *Node) routineInfoOf(ctx context.Context, a bot.Bot, f routineFile) api.BotRoutineInfo {
	return d.routineInfo(a, d.botSettings(a), f.r.Slug, f, d.routineStates(ctx, a.ID)[f.r.Slug])
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

func (d *Node) handleBotRoutineRun(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	if !a.ArchivedAt.IsZero() {
		return nil, botErr("bot %q is archived", a.Name)
	}
	if err := routine.Validate(f.r, routine.Limits{MinInterval: d.botSettings(a).Limits.Limits.MinInterval}); err != nil {
		return nil, botErr("routine %q is invalid: %v", f.r.Slug, err)
	}
	active, err := d.bots.ActiveRuns(ctx, a.ID, f.r.Slug)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if len(active) > 0 {
		return nil, botErr("routine %q already has a run in progress", f.r.Slug)
	}
	run, ok, err := d.bots.ClaimOccurrence(ctx, a.ID, f.r.Slug, d.botNow(), startedByYou, true)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if !ok {
		return nil, botErr("a run was just started; try again")
	}
	d.startRoutineRun(d.runCtx(), a, f.r, run)
	d.notifyConns(api.MethodBotChanged)
	return api.BotRoutineRunResult{RunID: run.ID}, nil
}

// ensureRoutineState returns the routine's state, creating it as the
// scheduler would when it has not seen the routine yet.
func (d *Node) ensureRoutineState(ctx context.Context, a bot.Bot, f routineFile) (bot.RoutineState, error) {
	if st, ok := d.routineState(ctx, a.ID, f.r.Slug); ok {
		return st, nil
	}
	now := d.botNow()
	next, _ := routine.NextDue(f.r, now, now, routineLoc(f.r, d.botSettings(a)))
	st := bot.RoutineState{BotID: a.ID, Slug: f.r.Slug, DefHash: f.hash, Enabled: true, NextDueAt: next, AnchorAt: now}
	if selfPaced(f.r) {
		st.CheckBackAt = now.Add(clampCheckBack(d.botSettings(a), 0))
	}
	return st, d.bots.UpsertRoutineState(ctx, st)
}

func (d *Node) handleBotRoutinePause(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	st, err := d.ensureRoutineState(ctx, a, f)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if st.Enabled {
		if err := d.bots.SetRoutineEnabled(ctx, a.ID, f.r.Slug, false, pausedByYou); err != nil {
			return nil, botErr("%v", err)
		}
	}
	d.notifyConns(api.MethodBotChanged)
	return d.routineInfoOf(ctx, a, f), nil
}

func (d *Node) handleBotRoutineResume(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	if !f.r.Enabled {
		return nil, botErr("routine %q has enabled: false in its file; edit the file to turn it on", f.r.Slug)
	}
	st, err := d.ensureRoutineState(ctx, a, f)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if st.Enabled {
		return d.routineInfoOf(ctx, a, f), nil
	}
	if st.PausedReason == pausedCompleted {
		return nil, botErr("goal %q is complete and can't be resumed; to work on it again, change its outcome or create a new goal", f.r.Slug)
	}
	// Due times from the pause are dropped: the routine resumes from now.
	now := d.botNow()
	loc := routineLoc(f.r, d.botSettings(a))
	next, _ := routine.NextDue(f.r, now, st.AnchorAt, loc)
	if err := d.bots.AdvanceNextDue(ctx, a.ID, f.r.Slug, next); err != nil {
		return nil, botErr("%v", err)
	}
	if selfPaced(f.r) && !st.CheckBackAt.After(now) {
		if err := d.bots.SetCheckBackAt(ctx, a.ID, f.r.Slug, now.Add(clampCheckBack(d.botSettings(a), 0))); err != nil {
			return nil, botErr("%v", err)
		}
	}
	if err := d.bots.SetRoutineEnabled(ctx, a.ID, f.r.Slug, true, ""); err != nil {
		return nil, botErr("%v", err)
	}
	d.botHealthResolved(a, healthPausedPrefix+f.r.Slug)
	d.notifyConns(api.MethodBotChanged)
	return d.routineInfoOf(ctx, a, f), nil
}

func (d *Node) handleBotRoutineRuns(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotRoutineRunsParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	if !routine.ValidSlug(p.Slug) {
		return nil, botErr("routine %q not found", p.Slug)
	}
	runs, err := s.RunsSince(ctx, a.ID, p.Slug, time.Time{})
	if err != nil {
		return nil, botErr("%v", err)
	}
	limit := p.Limit
	if limit <= 0 {
		limit = routineRunsDefault
	}
	res := api.BotRoutineRunsResult{Runs: []api.BotRoutineRunInfo{}}
	for i := len(runs) - 1; i >= 0 && len(res.Runs) < limit; i-- {
		r := runs[i]
		occ := r.Occurrence
		if r.RetryOf != "" {
			occ = occ.Add(-time.Nanosecond)
		}
		res.Runs = append(res.Runs, api.BotRoutineRunInfo{
			ID: r.ID, Slug: r.Slug, StartedBy: r.StartedBy, Test: r.Test, State: r.State, ThreadID: r.ThreadID,
			Occurrence: occ, StartedAt: r.StartedAt, EndedAt: timePtr(r.EndedAt),
			Result: r.Result, Summary: r.Summary, ActionsDone: r.ActionsDone, ActionsPending: r.ActionsPending,
			Unverified: r.Unverified, Sources: r.Sources, Cost: r.Cost, Tokens: r.Tokens, Error: r.Error,
		})
	}
	return res, nil
}

func (d *Node) handleBotRoutineHookSecret(ctx context.Context, params json.RawMessage) (any, error) {
	a, f, err := d.routineTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	if !webhookStart(f.r) {
		return nil, botErr("routine %q has no webhook start (starts: - event: webhook)", f.r.Slug)
	}
	secret, err := d.rotateWebhookSecret(ctx, a.ID, f.r.Slug)
	if err != nil {
		return nil, botErr("%v", err)
	}
	return api.BotRoutineHookSecretResult{
		Secret: secret, Path: webhookPrefix + a.Name + "/" + f.r.Slug, Listen: d.botCfg.WebhookListen,
	}, nil
}

// routineProposalSummary says in words what a routine proposal changes.
func routineProposalSummary(a bot.Bot, body memoryProposal) string {
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
