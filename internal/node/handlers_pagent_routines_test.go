package node

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestRoutineRPCs(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 2h\nbudget: { sessions: 5, cost: 2.5 }\n")
	r.routine(t, "alert", hookBody)
	ref := api.PAgentRoutineRef{AgentID: r.a.ID, Slug: "inbox"}

	list, err := callPAgent[api.PAgentRoutineListResult](t, r.d.handlePAgentRoutineList, api.PAgentRef{AgentID: r.a.ID})
	if err != nil || len(list.Routines) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	in := list.Routines[1]
	if in.Slug != "inbox" || in.State != "active" || len(in.Starts) != 1 || in.Starts[0] != "every 2 hours" || in.Budget.Sessions != 5 || in.Budget.Cost != 2.5 {
		t.Fatalf("inbox: %+v", in)
	}
	if !list.Routines[0].Webhook {
		t.Fatalf("alert: %+v", list.Routines[0])
	}

	info, err := callPAgent[api.PAgentRoutineInfo](t, r.d.handlePAgentRoutinePause, ref)
	if err != nil || info.State != "paused" || info.PausedReason != "you" {
		t.Fatalf("pause: %+v %v", info, err)
	}
	// Resume clears budget pauses too and recomputes the next due time.
	if err := r.store.SetRoutineEnabled(ctx, r.a.ID, "inbox", false, pausedBudget); err != nil {
		t.Fatal(err)
	}
	if err := r.store.AdvanceNextDue(ctx, r.a.ID, "inbox", time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	info, err = callPAgent[api.PAgentRoutineInfo](t, r.d.handlePAgentRoutineResume, ref)
	if err != nil || info.State != "active" || info.NextDue == nil || !info.NextDue.After(time.Now()) {
		t.Fatalf("resume: %+v %v", info, err)
	}
	if err := r.store.SetRoutineEnabled(ctx, r.a.ID, "inbox", false, pausedCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := callPAgent[api.PAgentRoutineInfo](t, r.d.handlePAgentRoutineResume, ref); err == nil || !strings.Contains(err.Error(), "complete") {
		t.Fatalf("resume completed: %v", err)
	}
	_ = r.store.SetRoutineEnabled(ctx, r.a.ID, "inbox", true, "")

	run, err := callPAgent[api.PAgentRoutineRunResult](t, r.d.handlePAgentRoutineRun, ref)
	if err != nil || run.RunID == "" {
		t.Fatalf("run: %+v %v", run, err)
	}
	if _, err := callPAgent[api.PAgentRoutineRunResult](t, r.d.handlePAgentRoutineRun, ref); err == nil {
		t.Fatal("a second run now while one is active")
	}
	v, _ := r.d.routineRuns.Load(run.RunID)
	c := v.(*runCtl)
	r.until(t, "spawned", func() bool { _, s := c.ids(); return s != "" })
	r.report(t, c, reportRunParams{Result: "progress", Summary: "tested"})
	r.wait(t)

	runs, err := callPAgent[api.PAgentRoutineRunsResult](t, r.d.handlePAgentRoutineRuns, api.PAgentRoutineRunsParams{AgentID: r.a.ID, Slug: "inbox"})
	if err != nil || len(runs.Runs) != 1 || !runs.Runs[0].Test || runs.Runs[0].StartedBy != "you" || runs.Runs[0].Summary != "tested" || runs.Runs[0].ThreadID == "" {
		t.Fatalf("runs: %+v %v", runs, err)
	}

	if _, err := callPAgent[api.PAgentRoutineHookSecretResult](t, r.d.handlePAgentRoutineHookSecret, ref); err == nil {
		t.Fatal("hook secret for a routine without a webhook start")
	}
	sec, err := callPAgent[api.PAgentRoutineHookSecretResult](t, r.d.handlePAgentRoutineHookSecret, api.PAgentRoutineRef{AgentID: r.a.ID, Slug: "alert"})
	if err != nil || !strings.HasPrefix(sec.Secret, "whs_") || sec.Path != "/hooks/"+r.a.Name+"/alert" {
		t.Fatalf("hook secret: %+v %v", sec, err)
	}
	if w := post(r.d.webhookHandler(ctx), sec.Path, "Bearer "+sec.Secret, `{}`); w.Code != 200 {
		t.Fatalf("webhook with the new secret: %d %s", w.Code, w.Body)
	}
	r.endAll(t)

	if _, err := callPAgent[api.PAgentRoutineInfo](t, r.d.handlePAgentRoutinePause, api.PAgentRoutineRef{AgentID: r.a.ID, Slug: "nope"}); err == nil {
		t.Fatal("pause of an unknown routine")
	}
}

func TestRoutineProposalListedAndApplied(t *testing.T) {
	r := newRunRig(t)
	goal := "what: migrate billing\nstarts:\n  - every: 1d\nuntil: outcome\noutcome: old client deleted\n"
	if _, err := r.d.mcpRoutineCreate(context.Background(), r.callerIn(r.chat), routineWriteParams{Slug: "billing", Routine: goal}); err != nil {
		t.Fatal(err)
	}
	props, err := callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID})
	if err != nil || len(props.Proposals) != 1 {
		t.Fatalf("proposals: %+v %v", props, err)
	}
	p := props.Proposals[0]
	if p.Kind != "routine" || p.Slug != "billing" || p.Summary != "create goal billing: old client deleted" || p.Text != goal {
		t.Fatalf("proposal: %+v", p)
	}
	if _, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: p.ID, Accept: true}); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(goal, "old client deleted", "all calls migrated", 1) + "verify: true\n"
	if _, err := r.d.mcpRoutineUpdate(context.Background(), r.callerIn(r.chat), routineWriteParams{Slug: "billing", Routine: changed}); err != nil {
		t.Fatal(err)
	}
	props, _ = callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID})
	if len(props.Proposals) != 1 || props.Proposals[0].Summary != "change goal billing: outcome, verify" {
		t.Fatalf("proposals: %+v", props.Proposals)
	}
}
