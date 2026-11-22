package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
)

func TestAgentsOverview(t *testing.T) {
	r := newAskRig(t)
	ctx := context.Background()
	if _, err := r.d.mcpAgentsOverview(ctx, r.callerIn(r.chat)); err == nil || !strings.Contains(err.Error(), "only for the primary agent") {
		t.Fatalf("not primary: %v", err)
	}
	if _, err := callPAgent[any](t, r.d.handlePAgentSetPrimary, api.PAgentPrimaryParams{AgentID: r.a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.mcpAgentsOverview(ctx, r.callerIn(r.runThread(t, "th_r", startedByRoutine))); err == nil || !strings.Contains(err.Error(), "only in chats") {
		t.Fatalf("run thread: %v", err)
	}

	// researcher has a work log, two hand-off runs and a pending proposal.
	logDir := filepath.Join(r.researcher.Dir, memoryDir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := range 7 {
		lines = append(lines, "- 2026-11-22 10:0"+string(rune('0'+i))+" did thing "+string(rune('a'+i)))
	}
	if err := os.WriteFile(filepath.Join(logDir, "2026-11.md"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, res := range []pagent.RunResult{{Result: "done", Summary: "found it"}, {Result: "needs_you", Summary: "login expired"}} {
		run, _, err := r.store.ClaimOccurrence(ctx, r.researcher.ID, handoffSlug, time.Unix(int64(1_700_000_000+i), 0), startedByHandoff, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.store.RecordResult(ctx, run.ID, res); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.InsertProposal(ctx, pagent.Proposal{ID: "mp_x", AgentID: r.researcher.ID, Ops: []byte(`{"source":"capture","ops":[]}`)}); err != nil {
		t.Fatal(err)
	}

	res, err := r.d.mcpAgentsOverview(ctx, r.callerIn(r.chat))
	if err != nil {
		t.Fatal(err)
	}
	ov := res.(agentsOverviewResult)
	if len(ov.Agents) != 2 {
		t.Fatalf("agents = %+v", ov.Agents)
	}
	var re agentOverview
	for _, o := range ov.Agents {
		if o.Name == "researcher" {
			re = o
		}
	}
	if len(re.WorkLog) != 5 || !strings.HasSuffix(re.WorkLog[4], "did thing g") || !strings.HasSuffix(re.WorkLog[0], "did thing c") {
		t.Fatalf("work log = %q", re.WorkLog)
	}
	if len(re.RunResults) != 2 || !strings.Contains(re.RunResults[0], "hand-off needs_you: login expired") || !strings.Contains(re.RunResults[1], "hand-off done: found it") {
		t.Fatalf("run results = %q", re.RunResults)
	}
	joined := strings.Join(re.NeedsYou, "\n")
	if !strings.Contains(joined, "1 proposed changes wait for review") || !strings.Contains(joined, "login expired") {
		t.Fatalf("needs you = %q", re.NeedsYou)
	}
}

func TestProposeAgent(t *testing.T) {
	r := newAskRig(t)
	ctx := context.Background()
	if _, err := r.d.mcpProposeAgent(ctx, r.callerIn(r.chat), proposeAgentParams{Name: "researcher", Description: "x", Instructions: "y"}); err == nil {
		t.Fatal("an existing name was proposed")
	}
	if _, err := r.d.mcpProposeAgent(ctx, r.callerIn(r.chat), proposeAgentParams{Name: "../bad", Description: "x", Instructions: "y"}); err == nil {
		t.Fatal("an invalid name was proposed")
	}
	if _, err := r.d.mcpProposeAgent(ctx, r.callerIn(r.runThread(t, "th_r", startedByRoutine)), proposeAgentParams{Name: "writer", Description: "x", Instructions: "y"}); err == nil {
		t.Fatal("proposed from a run")
	}
	if _, err := r.d.mcpProposeAgent(ctx, r.callerIn(r.chat), proposeAgentParams{
		Name: "writer", Description: "Drafts release notes", Instructions: "Write short, plain release notes.", Agent: "claude",
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.store.AgentByName(ctx, "writer"); ok {
		t.Fatal("the agent was created before the user accepted")
	}
	props, err := callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID})
	if err != nil || len(props.Proposals) != 1 {
		t.Fatalf("proposals = %+v %v", props, err)
	}
	p := props.Proposals[0]
	if p.Kind != proposalKindAgent || p.Summary != "create agent writer: Drafts release notes" {
		t.Fatalf("proposal = %+v", p)
	}
	if _, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: p.ID, Accept: true}); err != nil {
		t.Fatal(err)
	}
	w, ok, err := r.store.AgentByName(ctx, "writer")
	if err != nil || !ok {
		t.Fatalf("writer not created: %v", err)
	}
	def, err := pagent.LoadDefinition(w.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if def.Description != "Drafts release notes" || def.Instructions != "Write short, plain release notes." || def.Agent != "claude" {
		t.Fatalf("definition = %+v", def)
	}
	if _, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: p.ID, Accept: true}); err == nil {
		t.Fatal("a proposal was applied twice")
	}
}
