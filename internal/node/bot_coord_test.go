package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
)

func TestBotsOverview(t *testing.T) {
	r := newAskRig(t)
	ctx := context.Background()
	if _, err := r.d.mcpBotsOverview(ctx, r.callerIn(r.chat)); err == nil || !strings.Contains(err.Error(), "only for the primary bot") {
		t.Fatalf("not primary: %v", err)
	}
	if _, err := callBot[any](t, r.d.handleBotSetPrimary, api.BotPrimaryParams{BotID: r.a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.mcpBotsOverview(ctx, r.callerIn(r.runThread(t, "th_r", startedByRoutine))); err == nil || !strings.Contains(err.Error(), "only in chats") {
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
	for i, res := range []bot.RunResult{{Result: "done", Summary: "found it"}, {Result: "needs_you", Summary: "login expired"}} {
		run, _, err := r.store.ClaimOccurrence(ctx, r.researcher.ID, handoffSlug, time.Unix(int64(1_700_000_000+i), 0), startedByHandoff, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.store.RecordResult(ctx, run.ID, res); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.InsertProposal(ctx, bot.Proposal{ID: "mp_x", BotID: r.researcher.ID, Ops: []byte(`{"source":"capture","ops":[]}`)}); err != nil {
		t.Fatal(err)
	}

	res, err := r.d.mcpBotsOverview(ctx, r.callerIn(r.chat))
	if err != nil {
		t.Fatal(err)
	}
	ov := res.(botsOverviewResult)
	if len(ov.Bots) != 2 {
		t.Fatalf("bots = %+v", ov.Bots)
	}
	var re botOverview
	for _, o := range ov.Bots {
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

func TestProposeBot(t *testing.T) {
	r := newAskRig(t)
	ctx := context.Background()
	if _, err := r.d.mcpProposeBot(ctx, r.callerIn(r.chat), proposeBotParams{Name: "researcher", Description: "x", Instructions: "y"}); err == nil {
		t.Fatal("an existing name was proposed")
	}
	if _, err := r.d.mcpProposeBot(ctx, r.callerIn(r.chat), proposeBotParams{Name: "../bad", Description: "x", Instructions: "y"}); err == nil {
		t.Fatal("an invalid name was proposed")
	}
	if _, err := r.d.mcpProposeBot(ctx, r.callerIn(r.runThread(t, "th_r", startedByRoutine)), proposeBotParams{Name: "writer", Description: "x", Instructions: "y"}); err == nil {
		t.Fatal("proposed from a run")
	}
	if _, err := r.d.mcpProposeBot(ctx, r.callerIn(r.chat), proposeBotParams{
		Name: "writer", Description: "Drafts release notes", Instructions: "Write short, plain release notes.", Agent: "claude",
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.store.BotByName(ctx, "writer"); ok {
		t.Fatal("the bot was created before the user accepted")
	}
	props, err := callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID})
	if err != nil || len(props.Proposals) != 1 {
		t.Fatalf("proposals = %+v %v", props, err)
	}
	p := props.Proposals[0]
	if p.Kind != proposalKindBot || p.Summary != "create bot writer: Drafts release notes" {
		t.Fatalf("proposal = %+v", p)
	}
	if _, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: p.ID, Accept: true}); err != nil {
		t.Fatal(err)
	}
	w, ok, err := r.store.BotByName(ctx, "writer")
	if err != nil || !ok {
		t.Fatalf("writer not created: %v", err)
	}
	def, err := bot.LoadDefinition(w.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if def.Description != "Drafts release notes" || def.Instructions != "Write short, plain release notes." || def.Agent != "claude" {
		t.Fatalf("definition = %+v", def)
	}
	if _, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: p.ID, Accept: true}); err == nil {
		t.Fatal("a proposal was applied twice")
	}
}
