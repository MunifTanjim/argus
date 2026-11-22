package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
)

const (
	evRoutineError   = "routine.error"
	evRoutineMissed  = "routine.missed"
	evRoutineSkipped = "routine.skipped"
	evRoutineStale   = "routine.stale"
	evRoutinePaused  = "routine.paused"

	startedByRoutine = "routine"
	routinesDir      = "routines"

	// routineStaleAfter is how long past a due time a routine may go without
	// a run before a stale notice.
	routineStaleAfter = 24 * time.Hour
	// pausedAway is the paused reason of routines paused for the user's absence.
	pausedAway = "away"
)

// routineTick is how often the scheduler checks routines.
var routineTick = 30 * time.Second

// routineFile is a parsed routine file, cached by modification time and size.
type routineFile struct {
	mod  time.Time
	size int64
	hash string
	r    routine.Routine
	err  error
}

func (d *Node) pagentRoutineLoop(ctx context.Context) {
	d.routineTick(ctx)
	t := time.NewTicker(routineTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.routineTick(ctx)
		}
	}
}

// agentNotice records an agent-level event in the agent's most recently
// active chat thread; without one it only logs.
func (d *Node) agentNotice(ctx context.Context, a pagent.Agent, kind string, payload any) {
	th, ok, err := d.pagents.LatestThread(ctx, a.ID, threadChat)
	if err != nil || !ok {
		d.log.Info("pagent: notice without a thread", "agent", a.ID, "kind", kind, "payload", payload)
		return
	}
	d.logEvent(ctx, th.ID, kind, payload, "")
}

// routineTick runs one scheduler pass over every active agent.
func (d *Node) routineTick(ctx context.Context) {
	now := d.pagentNow()
	away := d.clientAway(now)
	expired, err := d.pagents.ExpiredLeases(ctx, now)
	if err != nil {
		d.log.Warn("pagent: expired runs", "err", err)
	}
	agents, err := d.pagents.ListAgents(ctx, false)
	if err != nil {
		d.log.Warn("pagent: list agents", "err", err)
		return
	}
	active := map[string]bool{}
	for _, a := range agents {
		active[a.ID] = true
	}
	byAgent := map[string][]pagent.RoutineRun{}
	for _, run := range expired {
		if d.ownsRun(run.ID) {
			continue
		}
		if active[run.AgentID] {
			byAgent[run.AgentID] = append(byAgent[run.AgentID], run)
		} else {
			d.interruptRun(ctx, run)
		}
	}
	for _, a := range agents {
		if ctx.Err() != nil {
			return
		}
		d.scheduleAgent(ctx, a, now, away, byAgent[a.ID])
	}
}

// ownsRun reports whether this process is executing the run.
func (d *Node) ownsRun(id string) bool {
	_, ok := d.routineRuns.Load(id)
	return ok
}

// interruptRun marks a run whose lease expired interrupted, unless this
// process executes it or its lease was renewed since it was listed. It
// reports whether the run was interrupted.
func (d *Node) interruptRun(ctx context.Context, run pagent.RoutineRun) bool {
	if d.ownsRun(run.ID) {
		return false
	}
	ok, err := d.pagents.InterruptRun(ctx, run.ID, d.pagentNow(), "lease expired")
	if err != nil {
		d.log.Warn("pagent: interrupt run", "run", run.ID, "err", err)
	}
	return ok
}

func defHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// routineFiles returns the agent's routine files in slug order, parsing only
// files that changed since the last call. A file that fails to parse carries
// its error.
func (d *Node) routineFiles(a pagent.Agent) map[string]routineFile {
	dir := filepath.Join(a.Dir, routinesDir)
	entries, err := os.ReadDir(dir)
	d.routineMu.Lock()
	defer d.routineMu.Unlock()
	if d.routineCache == nil {
		d.routineCache = map[string]map[string]routineFile{}
	}
	prev := d.routineCache[a.ID]
	out := map[string]routineFile{}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		d.log.Warn("pagent: read routines", "agent", a.ID, "err", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".yaml") || strings.HasPrefix(name, "_") {
			continue
		}
		slug := strings.TrimSuffix(name, ".yaml")
		info, err := e.Info()
		if err != nil {
			continue
		}
		if f, ok := prev[slug]; ok && f.mod.Equal(info.ModTime()) && f.size == info.Size() {
			out[slug] = f
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		f := routineFile{mod: info.ModTime(), size: info.Size(), hash: defHash(data)}
		f.r, f.err = routine.Parse(slug, data)
		out[slug] = f
	}
	d.routineCache[a.ID] = out
	return out
}

// routineErrorOnce posts a routine.error notice unless one was already posted
// for the slug with the same key (the definition hash).
func (d *Node) routineErrorOnce(ctx context.Context, a pagent.Agent, slug, key, msg string) {
	k := a.ID + "/" + slug
	d.routineMu.Lock()
	if d.routineErrSent == nil {
		d.routineErrSent = map[string]string{}
	}
	sent := d.routineErrSent[k] == key
	d.routineErrSent[k] = key
	d.routineMu.Unlock()
	if !sent {
		d.agentNotice(ctx, a, evRoutineError, map[string]string{"routine": slug, "message": msg})
	}
}

func (d *Node) clearRoutineError(a pagent.Agent, slug string) {
	d.routineMu.Lock()
	delete(d.routineErrSent, a.ID+"/"+slug)
	d.routineMu.Unlock()
}

// routineLoc is the routine's timezone; "auto" is the agent's timezone, else
// the node's.
func routineLoc(r routine.Routine, set pagent.Settings) *time.Location {
	local := time.Local
	if set.Timezone != "" {
		if loc, err := time.LoadLocation(set.Timezone); err == nil {
			local = loc
		}
	}
	loc, err := routine.Location(r, local)
	if err != nil {
		return local
	}
	return loc
}

// scheduleAgent validates the agent's routines, handles its interrupted runs
// and dispatches due occurrences.
func (d *Node) scheduleAgent(ctx context.Context, a pagent.Agent, now time.Time, away bool, expired []pagent.RoutineRun) {
	set := d.pagentSettings(a)
	lim := set.Limits.Limits
	files := d.routineFiles(a)
	slugs := make([]string, 0, len(files))
	for slug := range files {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	states := map[string]*pagent.RoutineState{}
	if list, err := d.pagents.RoutineStates(ctx, a.ID); err != nil {
		d.log.Warn("pagent: routine states", "agent", a.ID, "err", err)
		return
	} else {
		for i := range list {
			states[list[i].Slug] = &list[i]
		}
	}

	var scheduled []routineFile
	enabled := 0
	for _, slug := range slugs {
		f := files[slug]
		err := f.err
		if err == nil {
			err = routine.Validate(f.r, routine.Limits{MinInterval: lim.MinInterval})
		}
		if err != nil {
			d.routineErrorOnce(ctx, a, slug, f.hash, err.Error())
			continue
		}
		if !f.r.Enabled {
			d.clearRoutineError(a, slug)
			continue
		}
		enabled++
		if lim.MaxEnabledRoutines > 0 && enabled > lim.MaxEnabledRoutines {
			d.routineErrorOnce(ctx, a, slug, f.hash+"#cap",
				fmt.Sprintf("more than %d enabled routines; this one is not scheduled", lim.MaxEnabledRoutines))
			continue
		}
		d.clearRoutineError(a, slug)
		scheduled = append(scheduled, f)
	}

	byslug := map[string]routineFile{}
	for _, f := range scheduled {
		byslug[f.r.Slug] = f
	}
	for _, run := range expired {
		if !d.interruptRun(ctx, run) {
			continue
		}
		f, ok := byslug[run.Slug]
		st := states[run.Slug]
		if run.RetryOf != "" || run.Test || !ok || st == nil || !st.Enabled || away {
			continue
		}
		retry, ok, err := d.pagents.ClaimRetry(ctx, run)
		if err != nil {
			d.log.Warn("pagent: claim retry", "run", run.ID, "err", err)
		}
		if ok {
			d.startRoutineRun(ctx, a, f.r, retry)
		}
	}

	var paused []string
	for _, f := range scheduled {
		if ctx.Err() != nil {
			return
		}
		if d.scheduleRoutine(ctx, a, set, f, states[f.r.Slug], now, away) {
			paused = append(paused, f.r.Slug)
		}
	}
	if len(paused) > 0 {
		d.agentNotice(ctx, a, evRoutinePaused, map[string]any{"reason": pausedAway, "routines": paused})
	}
}

// scheduleRoutine brings one routine's state up to date and dispatches its
// due occurrence. It reports whether it paused the routine for absence.
func (d *Node) scheduleRoutine(ctx context.Context, a pagent.Agent, set pagent.Settings, f routineFile, st *pagent.RoutineState, now time.Time, away bool) bool {
	r := f.r
	loc := routineLoc(r, set)
	if st == nil {
		next, _ := routine.NextDue(r, now, now, loc)
		ns := pagent.RoutineState{AgentID: a.ID, Slug: r.Slug, DefHash: f.hash, Enabled: !away, NextDueAt: next, AnchorAt: now}
		if away {
			ns.PausedReason = pausedAway
		}
		if err := d.pagents.UpsertRoutineState(ctx, ns); err != nil {
			d.log.Warn("pagent: routine state", "agent", a.ID, "routine", r.Slug, "err", err)
		}
		return away
	}
	if st.AnchorAt.IsZero() {
		st.AnchorAt = now
		st.DefHash = ""
		if err := d.pagents.UpsertRoutineState(ctx, *st); err != nil {
			d.log.Warn("pagent: routine state", "agent", a.ID, "routine", r.Slug, "err", err)
			return false
		}
	}
	if st.DefHash != f.hash {
		next, _ := routine.NextDue(r, now, st.AnchorAt, loc)
		if err := d.pagents.SetRoutineDef(ctx, a.ID, r.Slug, f.hash, next); err != nil {
			d.log.Warn("pagent: routine state", "agent", a.ID, "routine", r.Slug, "err", err)
			return false
		}
		st.DefHash, st.NextDueAt = f.hash, next
	}
	if !st.Enabled {
		return false
	}
	if away {
		if err := d.pagents.SetRoutineEnabled(ctx, a.ID, r.Slug, false, pausedAway); err != nil {
			d.log.Warn("pagent: pause routine", "agent", a.ID, "routine", r.Slug, "err", err)
			return false
		}
		return true
	}

	d.promoteQueued(ctx, a, r)

	if st.NextDueAt.IsZero() {
		return false
	}
	if st.NextDueAt.After(now) {
		d.checkStale(ctx, a, r, st, loc, now)
		return false
	}
	occ := append([]time.Time{st.NextDueAt}, routine.DueBetween(r, st.NextDueAt, now, st.AnchorAt, loc)...)
	next, _ := routine.NextDue(r, now, st.AnchorAt, loc)
	newest := occ[len(occ)-1]
	window := set.Limits.Limits.CatchUpWindow
	runNewest := window <= 0 || now.Sub(newest) <= window
	missed := occ[:len(occ)-1]
	if !runNewest {
		missed = occ
	}
	if len(missed) > 0 {
		d.recordMissed(ctx, a, r.Slug, missed)
	}
	if !runNewest {
		if err := d.pagents.AdvanceNextDue(ctx, a.ID, r.Slug, next); err != nil {
			d.log.Warn("pagent: advance routine", "agent", a.ID, "routine", r.Slug, "err", err)
		}
		return false
	}
	d.dispatchOccurrence(ctx, a, set, r, newest, next)
	d.checkStale(ctx, a, r, st, loc, now)
	return false
}

// recordMissed records the missed occurrences as one missed run whose
// occurrence is the oldest.
func (d *Node) recordMissed(ctx context.Context, a pagent.Agent, slug string, missed []time.Time) {
	first, last := missed[0], missed[len(missed)-1]
	run, ok, err := d.pagents.ClaimOccurrence(ctx, a.ID, slug, first, startedByRoutine, false)
	if err != nil {
		d.log.Warn("pagent: record missed", "agent", a.ID, "routine", slug, "err", err)
	}
	if !ok {
		return
	}
	msg := fmt.Sprintf("missed %d occurrences from %s to %s", len(missed), first.Format(time.RFC3339), last.Format(time.RFC3339))
	if len(missed) == 1 {
		msg = "missed the occurrence at " + first.Format(time.RFC3339)
	}
	if err := d.pagents.SetRunState(ctx, run.ID, pagent.RunMissed, msg); err != nil {
		d.log.Warn("pagent: record missed", "agent", a.ID, "routine", slug, "err", err)
	}
	d.agentNotice(ctx, a, evRoutineMissed, map[string]any{
		"routine": slug, "count": len(missed),
		"first": first.Format(time.RFC3339), "last": last.Format(time.RFC3339),
	})
}

// dispatchOccurrence claims occ and starts its run, unless the overlap rule
// or the hourly cap skips (or queues) it. next becomes the routine's next due
// time in the same transaction as the claim.
func (d *Node) dispatchOccurrence(ctx context.Context, a pagent.Agent, set pagent.Settings, r routine.Routine, occ, next time.Time) {
	active, err := d.pagents.ActiveRuns(ctx, a.ID, r.Slug)
	if err != nil {
		d.log.Warn("pagent: active runs", "agent", a.ID, "routine", r.Slug, "err", err)
		return
	}
	running, queued := 0, 0
	for _, run := range active {
		if run.State == pagent.RunQueued {
			queued++
		} else {
			running++
		}
	}
	claim := func() (pagent.RoutineRun, bool) {
		run, ok, err := d.pagents.ClaimOccurrenceAdvance(ctx, a.ID, r.Slug, occ, startedByRoutine, false, next)
		if err != nil {
			d.log.Warn("pagent: claim occurrence", "agent", a.ID, "routine", r.Slug, "err", err)
		}
		return run, ok
	}
	skip := func(reason string) {
		run, ok := claim()
		if !ok {
			return
		}
		if err := d.pagents.SetRunState(ctx, run.ID, pagent.RunSkipped, reason); err != nil {
			d.log.Warn("pagent: skip run", "run", run.ID, "err", err)
		}
		d.skippedNotice(ctx, a, r.Slug, reason, occ)
	}
	if running > 0 || queued > 0 {
		if r.Overlap == "queue" && queued == 0 {
			if run, ok := claim(); ok {
				if err := d.pagents.SetRunState(ctx, run.ID, pagent.RunQueued, ""); err != nil {
					d.log.Warn("pagent: queue run", "run", run.ID, "err", err)
				}
			}
			return
		}
		skip("previous run still active")
		return
	}
	if max := set.Limits.Limits.MaxRunsPerHour; max > 0 {
		n, err := d.pagents.RunsLastHour(ctx, a.ID)
		if err != nil {
			d.log.Warn("pagent: runs last hour", "agent", a.ID, "err", err)
			return
		}
		if n >= max {
			skip(fmt.Sprintf("max-runs-per-hour (%d) reached", max))
			return
		}
	}
	if run, ok := claim(); ok {
		d.startRoutineRun(ctx, a, r, run)
	}
}

// skippedNotice posts routine.skipped once per streak of skips for the same reason.
func (d *Node) skippedNotice(ctx context.Context, a pagent.Agent, slug, reason string, occ time.Time) {
	k := a.ID + "/" + slug
	d.routineMu.Lock()
	if d.routineSkip == nil {
		d.routineSkip = map[string]string{}
	}
	sent := d.routineSkip[k] == reason
	d.routineSkip[k] = reason
	d.routineMu.Unlock()
	if !sent {
		d.agentNotice(ctx, a, evRoutineSkipped, map[string]string{"routine": slug, "reason": reason, "occurrence": occ.Format(time.RFC3339)})
	}
}

// promoteQueued starts the routine's queued run once no run is active.
func (d *Node) promoteQueued(ctx context.Context, a pagent.Agent, r routine.Routine) {
	active, err := d.pagents.ActiveRuns(ctx, a.ID, r.Slug)
	if err != nil || len(active) == 0 {
		return
	}
	for _, run := range active {
		if run.State != pagent.RunQueued {
			return
		}
	}
	run := active[0]
	if err := d.pagents.SetRunState(ctx, run.ID, pagent.RunClaimed, ""); err != nil {
		return
	}
	if err := d.pagents.RenewLease(ctx, run.ID, d.pagentNow().Add(pagent.ClaimLease)); err != nil {
		return
	}
	run.State = pagent.RunClaimed
	d.startRoutineRun(ctx, a, r, run)
}

func (d *Node) startRoutineRun(ctx context.Context, a pagent.Agent, r routine.Routine, run pagent.RoutineRun) {
	d.routineMu.Lock()
	delete(d.routineSkip, a.ID+"/"+r.Slug)
	d.routineMu.Unlock()
	if err := d.routineStartFn(ctx, a, r, run); err != nil {
		d.log.Warn("pagent: start run", "agent", a.ID, "routine", r.Slug, "run", run.ID, "err", err)
		if err := d.pagents.SetRunState(ctx, run.ID, pagent.RunFailed, err.Error()); err != nil {
			d.log.Warn("pagent: fail run", "run", run.ID, "err", err)
		}
	}
}

// checkStale posts routine.stale once when the routine has gone without a
// run for routineStaleAfter past a due time while not failing.
func (d *Node) checkStale(ctx context.Context, a pagent.Agent, r routine.Routine, st *pagent.RoutineState, loc *time.Location, now time.Time) {
	if st.ConsecutiveFailures > 0 {
		return
	}
	since := st.AnchorAt
	if last, ok, err := d.pagents.LastStartedRun(ctx, a.ID, r.Slug); err != nil {
		return
	} else if ok && last.StartedAt.After(since) {
		since = last.StartedAt
	}
	due, ok := routine.NextDue(r, since, st.AnchorAt, loc)
	if !ok || now.Sub(due) < routineStaleAfter {
		return
	}
	k := a.ID + "/" + r.Slug
	d.routineMu.Lock()
	if d.routineStale == nil {
		d.routineStale = map[string]time.Time{}
	}
	sent := d.routineStale[k].Equal(due)
	d.routineStale[k] = due
	d.routineMu.Unlock()
	if !sent {
		d.agentNotice(ctx, a, evRoutineStale, map[string]string{"routine": r.Slug, "due": due.Format(time.RFC3339)})
	}
}
