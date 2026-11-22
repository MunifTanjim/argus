package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
)

const (
	evRoutineChanged  = "routine.changed"
	evRoutineProposed = "routine.proposed"

	proposalKindRoutine = "routine"
	checkBackPrefix     = "check-back-"

	// checkBackDefault is the wait of a self-paced routine that names none.
	checkBackDefault = 24 * time.Hour
	checkBackMinDef  = 15 * time.Minute
	checkBackMaxDef  = 7 * 24 * time.Hour
	maxCheckBacksDef = 10
	maxRoutineBytes  = 16 << 10
)

// selfPaced reports whether r has a check-back start.
func selfPaced(r routine.Routine) bool {
	for _, s := range r.Starts {
		if s.CheckBack != "" {
			return true
		}
	}
	return false
}

// oneShotCheckBack reports whether r is a one-shot check-back created by
// schedule_check_back.
func oneShotCheckBack(r routine.Routine) bool {
	return strings.HasPrefix(r.Slug, checkBackPrefix) && len(r.Starts) == 1 && r.Starts[0].At != ""
}

// checkBackMultiplier scales the default self-paced wait by the agent's
// proactivity dial: low waits twice as long, high half as long.
func checkBackMultiplier(set pagent.Settings) float64 {
	switch set.Proactivity {
	case proactivityLow:
		return 2
	case proactivityHigh:
		return 0.5
	}
	return 1
}

// clampCheckBack returns the wait before a self-paced routine runs again: d
// as asked, or with d zero the default scaled by the dial (the longest wait
// with the dial off, which schedules only what runs ask for), kept within
// the check-back limits and never below min-interval.
func clampCheckBack(set pagent.Settings, d time.Duration) time.Duration {
	lim := set.Limits.Limits
	lo, hi := lim.CheckBackMin, lim.CheckBackMax
	if lo <= 0 {
		lo = checkBackMinDef
	}
	if hi <= 0 {
		hi = checkBackMaxDef
	}
	lo = max(lo, lim.MinInterval)
	if d <= 0 {
		if set.Proactivity == proactivityOff {
			return hi
		}
		d = time.Duration(float64(checkBackDefault) * checkBackMultiplier(set))
	}
	return min(max(d, lo), hi)
}

// setNextCheckBack schedules a self-paced routine's next run.
func (d *Node) setNextCheckBack(ctx context.Context, a pagent.Agent, slug string, want time.Duration) {
	next := d.pagentNow().Add(clampCheckBack(d.pagentSettings(a), want))
	if err := d.pagents.SetCheckBackAt(context.WithoutCancel(ctx), a.ID, slug, next); err != nil {
		d.log.Warn("pagent: check-back", "agent", a.ID, "routine", slug, "err", err)
	}
}

// removeOneShot deletes a one-shot check-back once its run has ended.
func (d *Node) removeOneShot(ctx context.Context, c *runCtl) {
	if !oneShotCheckBack(c.r) || ctx.Err() != nil {
		return
	}
	th, _ := c.ids()
	a, slug := c.agent, c.r.Slug
	rel := filepath.Join(routinesDir, slug+".yaml")
	_, err := d.commitRoutineFile(ctx, a, th, sourceRun, "routine: remove "+slug, rel, func() error {
		err := os.Remove(filepath.Join(a.Dir, rel))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
	if err != nil {
		d.log.Warn("pagent: remove check-back", "agent", a.ID, "routine", slug, "err", err)
		return
	}
	if err := d.pagents.DeleteRoutineState(context.WithoutCancel(ctx), a.ID, slug); err != nil {
		d.log.Warn("pagent: remove check-back state", "agent", a.ID, "routine", slug, "err", err)
	}
}

// commitRoutineFile commits the user's edits of rel, applies change and
// commits rel as source. It returns the commit, "" when nothing changed.
func (d *Node) commitRoutineFile(ctx context.Context, a pagent.Agent, threadID, source, subject, rel string, change func() error) (string, error) {
	ctx = context.WithoutCancel(ctx)
	defer lockMemory(a.ID)()
	repo, err := agentgit.Open(ctx, a.Dir, d.pagentGitMode())
	if err != nil {
		return "", err
	}
	if err := d.commitHandEdits(ctx, repo, a, threadID, []string{rel}); err != nil {
		return "", err
	}
	if err := change(); err != nil {
		return "", err
	}
	return repo.CommitPaths(ctx, subject, memoryTrailers(a, source, threadID), []string{rel}, true)
}

type scheduleCheckBackParams struct {
	In   string `json:"in"`
	Note string `json:"note"`
}

type routineRefParams struct {
	Slug string `json:"slug"`
}

type routineWriteParams struct {
	Slug    string `json:"slug"`
	Routine string `json:"routine"`
	Reason  string `json:"reason"`
}

type routineToolResult struct {
	Status string `json:"status"`
	Slug   string `json:"slug,omitempty"`
	Commit string `json:"commit,omitempty"`
	Note   string `json:"note,omitempty"`
}

// routineInfo is one routine in routine_list.
type routineInfo struct {
	Slug         string   `json:"slug"`
	What         string   `json:"what,omitempty"`
	Starts       []string `json:"starts,omitempty"`
	Does         string   `json:"does,omitempty"`
	Until        string   `json:"until,omitempty"`
	Outcome      string   `json:"outcome,omitempty"`
	Verify       string   `json:"verify,omitempty"`
	Enabled      bool     `json:"enabled"`
	PausedReason string   `json:"paused_reason,omitempty"`
	NextDue      string   `json:"next_due,omitempty"`
	LastResult   string   `json:"last_result,omitempty"`
	SessionsUsed int64    `json:"sessions_used"`
	CostUsed     float64  `json:"cost_used"`
	Error        string   `json:"error,omitempty"`
}

// routineChatCaller resolves an MCP caller that may use the routine tools:
// a session of a chat thread.
func (d *Node) routineChatCaller(ctx context.Context, caller callerRef) (pagent.Agent, pagent.Thread, error) {
	a, th, err := d.pagentCaller(ctx, caller)
	if err != nil {
		return a, th, err
	}
	if th.Kind != threadChat {
		return a, th, fmt.Errorf("routine tools work only in chats")
	}
	return a, th, nil
}

func (d *Node) mcpScheduleCheckBack(ctx context.Context, caller callerRef, p scheduleCheckBackParams) (any, error) {
	a, th, err := d.pagentCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	switch {
	case th.Kind == threadChat && th.StartedBy == startedByYou:
	case th.Kind == threadRun && th.StartedBy == startedByCheckBack:
	default:
		return nil, fmt.Errorf("schedule_check_back works only in chats and check-back runs")
	}
	note := strings.Join(strings.Fields(p.Note), " ")
	if note == "" {
		return nil, fmt.Errorf("note is required")
	}
	if rule, bad := memory.LooksSecret(note); bad {
		return nil, fmt.Errorf("looks like a secret (%s); not saved", rule)
	}
	in, err := routine.ParseDuration(strings.TrimSpace(p.In))
	if err != nil || in <= 0 {
		return nil, fmt.Errorf("in must be a duration such as 2h or 3d")
	}
	set := d.pagentSettings(a)
	limit := set.Limits.Limits.MaxCheckBacks
	if limit <= 0 {
		limit = maxCheckBacksDef
	}
	existing, _ := filepath.Glob(filepath.Join(a.Dir, routinesDir, checkBackPrefix+"*.yaml"))
	if len(existing) >= limit {
		return nil, fmt.Errorf("already %d check-backs pending (the limit)", len(existing))
	}
	in = clampCheckBack(set, in)
	r := routine.Routine{What: note, Does: "act", Until: untilStopped, Timezone: "auto", IfUnavailable: "report", ReportTo: "thread", Overlap: "skip", Enabled: true}
	loc := routineLoc(r, set)
	at := d.pagentNow().Add(in).In(loc)
	// Rounded up to the minute: an at time has minute precision.
	if t := at.Truncate(time.Minute); t.Before(at) {
		at = t.Add(time.Minute)
	}
	r.Starts = []routine.Start{{At: at.Format("2006-01-02T15:04")}}
	slug := checkBackPrefix + strconv.FormatInt(at.Unix(), 10)
	for i := 1; fileExists(filepath.Join(a.Dir, routinesDir, slug+".yaml")); i++ {
		slug = fmt.Sprintf("%s%d-%d", checkBackPrefix, at.Unix(), i)
	}
	r.Slug = slug
	rel := filepath.Join(routinesDir, slug+".yaml")
	hash, err := d.commitRoutineFile(ctx, a, th.ID, sourceAgent, "routine: check back "+slug, rel, func() error {
		return routine.Save(filepath.Join(a.Dir, routinesDir), r)
	})
	if err != nil {
		return nil, err
	}
	d.routineEvent(ctx, th.ID, "check-back", slug, hash)
	return routineToolResult{Status: "scheduled", Slug: slug, Commit: hash, Note: "runs at " + at.Format(time.RFC3339)}, nil
}

func (d *Node) routineEvent(ctx context.Context, threadID, action, slug, commit string) {
	d.logEvent(ctx, threadID, evRoutineChanged, map[string]any{"action": action, "routine": slug, "commit": commit}, "")
}

func (d *Node) mcpRoutineList(ctx context.Context, caller callerRef) (any, error) {
	a, _, err := d.routineChatCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	states := map[string]pagent.RoutineState{}
	if sts, err := d.pagents.RoutineStates(ctx, a.ID); err == nil {
		for _, st := range sts {
			states[st.Slug] = st
		}
	}
	files := d.routineFiles(a)
	out := []routineInfo{}
	for _, slug := range slices.Sorted(maps.Keys(files)) {
		f := files[slug]
		info := routineInfo{Slug: slug}
		if f.err != nil {
			info.Error = f.err.Error()
			out = append(out, info)
			continue
		}
		r := f.r
		info.What, info.Does, info.Until, info.Outcome, info.Verify, info.Enabled = r.What, r.Does, r.Until, r.Outcome, r.Verify, r.Enabled
		for _, s := range r.Starts {
			info.Starts = append(info.Starts, routine.Describe(s))
		}
		if st, ok := states[slug]; ok {
			info.Enabled = info.Enabled && st.Enabled
			info.PausedReason, info.LastResult = st.PausedReason, st.LastResult
			info.SessionsUsed, info.CostUsed = st.SessionsUsed, st.CostUsed
			next := st.NextDueAt
			if selfPaced(r) && !st.CheckBackAt.IsZero() && (next.IsZero() || st.CheckBackAt.Before(next)) {
				next = st.CheckBackAt
			}
			if !next.IsZero() {
				info.NextDue = next.Format(time.RFC3339)
			}
		}
		out = append(out, info)
	}
	return map[string]any{"routines": out}, nil
}

// parseRoutineText validates the text of a routine file for slug.
func (d *Node) parseRoutineText(a pagent.Agent, slug, text string) (routine.Routine, error) {
	if !routine.ValidSlug(slug) {
		return routine.Routine{}, fmt.Errorf("slug must be lowercase letters, digits and dashes")
	}
	if strings.HasPrefix(slug, checkBackPrefix) {
		return routine.Routine{}, fmt.Errorf("slugs starting with %q are reserved for check-backs", checkBackPrefix)
	}
	if len(text) > maxRoutineBytes {
		return routine.Routine{}, fmt.Errorf("routine is over %d KB", maxRoutineBytes>>10)
	}
	if rule, bad := memory.LooksSecret(text); bad {
		return routine.Routine{}, fmt.Errorf("looks like a secret (%s); not saved", rule)
	}
	r, err := routine.Parse(slug, []byte(text))
	if err != nil {
		return r, err
	}
	lim := d.pagentSettings(a).Limits.Limits
	return r, routine.Validate(r, routine.Limits{MinInterval: lim.MinInterval})
}

func routinePath(a pagent.Agent, slug string) (rel, abs string) {
	rel = filepath.Join(routinesDir, slug+".yaml")
	return rel, filepath.Join(a.Dir, rel)
}

// goalChange reports whether replacing old (nil when new) with r changes what
// only the user decides: a goal's outcome, its verify command, or whether it
// is a goal.
func goalChange(old *routine.Routine, r routine.Routine) bool {
	if old == nil {
		return r.Until == untilOutcome
	}
	if old.Until != untilOutcome && r.Until != untilOutcome {
		return false
	}
	return old.Until != r.Until || old.Outcome != r.Outcome || old.Verify != r.Verify
}

func (d *Node) mcpRoutineCreate(ctx context.Context, caller callerRef, p routineWriteParams) (any, error) {
	return d.routineWrite(ctx, caller, p, true)
}

func (d *Node) mcpRoutineUpdate(ctx context.Context, caller callerRef, p routineWriteParams) (any, error) {
	return d.routineWrite(ctx, caller, p, false)
}

func (d *Node) routineWrite(ctx context.Context, caller callerRef, p routineWriteParams, create bool) (any, error) {
	a, th, err := d.routineChatCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	r, err := d.parseRoutineText(a, p.Slug, p.Routine)
	if err != nil {
		return nil, err
	}
	rel, abs := routinePath(a, p.Slug)
	var old *routine.Routine
	data, err := os.ReadFile(abs)
	switch {
	case err == nil && create:
		return nil, fmt.Errorf("routine %s already exists; use routine_update", p.Slug)
	case err == nil:
		if o, perr := routine.Parse(p.Slug, data); perr == nil {
			old = &o
		}
	case errors.Is(err, os.ErrNotExist) && !create:
		return nil, fmt.Errorf("routine %s not found", p.Slug)
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	text := strings.TrimRight(p.Routine, "\n") + "\n"
	if goalChange(old, r) {
		return d.proposeRoutine(ctx, a, th, p.Slug, text, p.Reason)
	}
	action := "update"
	if create {
		action = "create"
	}
	hash, err := d.commitRoutineFile(ctx, a, th.ID, sourceAgent, fmt.Sprintf("routine: %s %s", action, p.Slug), rel, func() error {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte(text), 0o644)
	})
	if err != nil {
		return nil, err
	}
	d.routineEvent(ctx, th.ID, action, p.Slug, hash)
	note := ""
	if create {
		note = "it waits for its next due time"
	}
	return routineToolResult{Status: action + "d", Slug: p.Slug, Commit: hash, Note: note}, nil
}

// proposeRoutine stages a routine file for the user's review.
func (d *Node) proposeRoutine(ctx context.Context, a pagent.Agent, th pagent.Thread, slug, text, reason string) (any, error) {
	body, err := json.Marshal(memoryProposal{
		Kind: proposalKindRoutine, Source: sourceAgent, Slug: slug, Text: text, Reason: strings.TrimSpace(reason),
	})
	if err != nil {
		return nil, err
	}
	id := "mp_" + strings.TrimPrefix(pagent.NewID(), "ag_")
	if err := d.pagents.InsertProposal(ctx, pagent.Proposal{ID: id, AgentID: a.ID, ThreadID: th.ID, Ops: body}); err != nil {
		return nil, err
	}
	d.logEvent(ctx, th.ID, evRoutineProposed, map[string]any{"proposal_id": id, "routine": slug, "reason": strings.TrimSpace(reason)}, "")
	d.notifyConns(api.MethodPAgentChanged)
	return routineToolResult{Status: "proposed", Slug: slug, Note: "a goal's outcome and verify are the user's; they review this change"}, nil
}

// applyRoutineProposal writes an accepted routine proposal and commits it.
func (d *Node) applyRoutineProposal(ctx context.Context, a pagent.Agent, pr pagent.Proposal, body memoryProposal) (string, error) {
	if _, err := d.parseRoutineText(a, body.Slug, body.Text); err != nil {
		return "", pagentErr("can't apply: %v", err)
	}
	rel, abs := routinePath(a, body.Slug)
	hash, err := d.commitRoutineFile(ctx, a, pr.ThreadID, sourceAgent, "routine: update "+body.Slug, rel, func() error {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte(body.Text), 0o644)
	})
	if err != nil {
		return "", pagentErr("%v", err)
	}
	return hash, nil
}

var enabledLine = regexp.MustCompile(`(?m)^enabled:.*$`)

func (d *Node) mcpRoutineSetEnabled(ctx context.Context, caller callerRef, p routineRefParams, enabled bool) (any, error) {
	a, th, err := d.routineChatCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	if !routine.ValidSlug(p.Slug) {
		return nil, fmt.Errorf("routine %s not found", p.Slug)
	}
	rel, abs := routinePath(a, p.Slug)
	data, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("routine %s not found", p.Slug)
	} else if err != nil {
		return nil, err
	}
	if enabled {
		if st, ok := d.routineState(ctx, a.ID, p.Slug); ok && !st.Enabled {
			switch st.PausedReason {
			case pausedBudget:
				return nil, fmt.Errorf("routine %s is paused because its budget is used up; only the user can resume it", p.Slug)
			case pausedCompleted:
				return nil, fmt.Errorf("goal %s is complete; only the user can reopen it", p.Slug)
			}
		}
	}
	line := fmt.Sprintf("enabled: %t", enabled)
	text := string(data)
	if enabledLine.MatchString(text) {
		text = enabledLine.ReplaceAllString(text, line)
	} else {
		text = strings.TrimRight(text, "\n") + "\n" + line + "\n"
	}
	action := "pause"
	if enabled {
		action = "resume"
	}
	hash, err := d.commitRoutineFile(ctx, a, th.ID, sourceAgent, fmt.Sprintf("routine: %s %s", action, p.Slug), rel, func() error {
		return os.WriteFile(abs, []byte(text), 0o644)
	})
	if err != nil {
		return nil, err
	}
	if enabled {
		if st, ok := d.routineState(ctx, a.ID, p.Slug); ok && !st.Enabled {
			if err := d.pagents.SetRoutineEnabled(ctx, a.ID, p.Slug, true, ""); err != nil {
				return nil, err
			}
		}
	}
	d.routineEvent(ctx, th.ID, action, p.Slug, hash)
	return routineToolResult{Status: action + "d", Slug: p.Slug, Commit: hash}, nil
}

func (d *Node) mcpRoutineDelete(ctx context.Context, caller callerRef, p routineRefParams) (any, error) {
	a, th, err := d.routineChatCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	if !routine.ValidSlug(p.Slug) {
		return nil, fmt.Errorf("routine %s not found", p.Slug)
	}
	rel, abs := routinePath(a, p.Slug)
	if !fileExists(abs) {
		return nil, fmt.Errorf("routine %s not found", p.Slug)
	}
	hash, err := d.commitRoutineFile(ctx, a, th.ID, sourceAgent, "routine: delete "+p.Slug, rel, func() error {
		return os.Remove(abs)
	})
	if err != nil {
		return nil, err
	}
	if err := d.pagents.DeleteRoutineState(ctx, a.ID, p.Slug); err != nil {
		d.log.Warn("pagent: delete routine state", "agent", a.ID, "routine", p.Slug, "err", err)
	}
	d.routineEvent(ctx, th.ID, "delete", p.Slug, hash)
	return routineToolResult{Status: "deleted", Slug: p.Slug, Commit: hash, Note: "the goal folder, if any, is kept"}, nil
}
