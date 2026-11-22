package node

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
)

const (
	// briefSlug is the built-in routine that writes an agent's daily brief.
	briefSlug = "_brief"
	evBrief   = "brief"

	proactivityOff  = "off"
	proactivityLow  = "low"
	proactivityHigh = "high"

	briefRunsMax = 20
)

// briefWanted reports whether a writes a daily brief: briefs on and the dial
// not off, and, when the node has a primary agent, only the primary does.
func (d *Node) briefWanted(ctx context.Context, a pagent.Agent, set pagent.Settings) bool {
	if !set.BriefEnabled || set.Proactivity == proactivityOff {
		return false
	}
	primary := d.primaryID(ctx, d.pagents)
	return primary == "" || primary == a.ID
}

// briefRoutine is the built-in brief routine at the agent's brief time.
func briefRoutine(set pagent.Settings) (routineFile, error) {
	at := set.BriefAt
	if at == "" {
		at = "08:00"
	}
	t, err := time.Parse("15:04", at)
	if err != nil {
		return routineFile{}, fmt.Errorf("brief.at %q must be a time such as 08:00", at)
	}
	r := routine.Routine{
		Slug: briefSlug, What: "Write today's brief for the user.",
		Starts: []routine.Start{{Schedule: fmt.Sprintf("%d %d * * *", t.Minute(), t.Hour())}},
		Does:   "tell", Until: untilStopped, Timezone: "auto", IfUnavailable: "report",
		ReportTo: "thread", Overlap: "skip", Enabled: true,
	}
	return routineFile{r: r, hash: "brief@" + at}, nil
}

// briefSection is the run context of a brief: facts Argus gathered, for the
// agent or, for the primary agent, every agent on the node.
func (d *Node) briefSection(ctx context.Context, c *runCtl, set pagent.Settings) string {
	a := c.agent
	loc := agentLoc(set)
	now := d.pagentNow().In(loc)
	since := now.Add(-24 * time.Hour)
	if st, ok := d.routineState(ctx, a.ID, briefSlug); ok && !st.LastRunAt.IsZero() {
		since = st.LastRunAt
	}
	agents := []pagent.Agent{a}
	if d.primaryID(ctx, d.pagents) == a.ID {
		if all, err := d.pagents.ListAgents(ctx, false); err == nil {
			agents = all
		}
	}

	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("## Daily brief")
	line("")
	line("Write the user's brief for %s from the facts below, gathered by Argus since %s. Keep it short: what needs them first, then what happened, then what is coming today.",
		now.Format("Monday 2006-01-02"), since.In(loc).Format(time.RFC3339))
	line("Report it with report_run: the brief text as summary and result progress, or result no_change when there is nothing worth telling (no brief is sent then).")
	for _, ag := range agents {
		line("")
		line("### Agent %s", ag.Name)
		d.briefFactsFor(ctx, &b, ag, since, now)
	}
	return b.String()
}

// briefFactsFor writes one agent's facts.
func (d *Node) briefFactsFor(ctx context.Context, b *strings.Builder, a pagent.Agent, since, now time.Time) {
	line := func(format string, args ...any) { fmt.Fprintf(b, format+"\n", args...) }
	set := d.pagentSettings(a)
	loc := agentLoc(set)
	files := d.routineFiles(a)
	states := d.routineStates(ctx, a.ID)
	slugs := slices.Sorted(maps.Keys(files))

	var needs []string
	if props, err := d.pagents.PendingProposals(ctx, a.ID); err == nil && len(props) > 0 {
		needs = append(needs, fmt.Sprintf("%d proposed changes wait for review", len(props)))
	}
	var results, due, goals []string
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	for _, slug := range slugs {
		f := files[slug]
		if f.err != nil {
			needs = append(needs, fmt.Sprintf("routine %s is invalid: %v", slug, f.err))
			continue
		}
		st := states[slug]
		if st != nil && !st.Enabled && st.PausedReason != "" && st.PausedReason != pausedCompleted && st.PausedReason != pausedByYou {
			needs = append(needs, fmt.Sprintf("routine %s is paused (%s)", slug, st.PausedReason))
		}
		runs, _ := d.pagents.RunsSince(ctx, a.ID, slug, since)
		for _, run := range runs {
			if run.Result == "" && run.State != pagent.RunFailed {
				continue
			}
			res := run.Result
			if res == "" {
				res = "failed: " + run.Error
			}
			s := fmt.Sprintf("%s %s %s", run.StartedAt.In(loc).Format("Mon 15:04"), slug, res)
			if run.Summary != "" {
				s += ": " + firstLineOf(run.Summary)
			}
			if run.Result == resultNeedsYou || run.Result == resultBlocked {
				needs = append(needs, s)
			} else if run.Result != resultNoChange && len(results) < briefRunsMax {
				results = append(results, s)
			}
		}
		if st != nil && st.Enabled && f.r.Enabled {
			next := st.NextDueAt
			if selfPaced(f.r) && !st.CheckBackAt.IsZero() && (next.IsZero() || st.CheckBackAt.Before(next)) {
				next = st.CheckBackAt
			}
			if !next.IsZero() && !next.After(endOfDay) {
				due = append(due, fmt.Sprintf("%s at %s: %s", slug, next.In(loc).Format("15:04"), firstLineOf(f.r.What)))
			}
		}
		if f.r.Until == untilOutcome && (st == nil || st.PausedReason != pausedCompleted) {
			g := fmt.Sprintf("%s: %s", slug, firstLineOf(f.r.Outcome))
			if st != nil {
				g += fmt.Sprintf(" (sessions %d", st.SessionsUsed)
				if f.r.Budget.Sessions > 0 {
					g += fmt.Sprintf(" of %d", f.r.Budget.Sessions)
				}
				g += ")"
			}
			logText, _ := readFileOptional(filepath.Join(a.Dir, routinesDir, slug, "log.md"))
			if last := lastLogEntries(logText, 1); len(last) > 0 {
				g += "; last: " + strings.TrimPrefix(last[0], "- ")
			}
			goals = append(goals, g)
		}
	}
	needs = append(needs, d.healthNotes(a)...)

	list := func(title string, items []string) {
		line("")
		line("%s:", title)
		if len(items) == 0 {
			line("- none")
		}
		for _, it := range items {
			line("- %s", it)
		}
	}
	list("Needs the user", needs)
	list("Run results", results)
	list("Due today", due)
	list("Goals", goals)
}

func firstLineOf(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}
