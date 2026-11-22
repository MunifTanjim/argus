package node

import (
	"context"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/session"
)

func (r *memRig) caller(thread string) callerRef {
	return callerRef{Local: &session.Session{ID: "claude:c1", PAgentID: r.a.ID, PAgentThreadID: thread}}
}

func TestRememberRefusedInRun(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.s.InsertThread(context.Background(), pagent.Thread{ID: "th_run", AgentID: r.a.ID, Kind: "run", StartedBy: "routine"}); err != nil {
		t.Fatal(err)
	}
	_, err := r.d.mcpRemember(context.Background(), r.caller("th_run"), rememberParams{Action: "add", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "memory is written only from chats") {
		t.Fatalf("err = %v", err)
	}
	_, err = r.d.mcpRemember(context.Background(), callerRef{Local: &session.Session{ID: "claude:c2"}}, rememberParams{Action: "add", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "not an agent session") {
		t.Fatalf("err = %v", err)
	}
}

func TestRememberAddsAndForgets(t *testing.T) {
	r := newMemRig(t, 0)
	res, err := r.d.mcpRemember(context.Background(), r.caller(r.th), rememberParams{Action: "add", Text: "likes tea"})
	if err != nil || res.(rememberResult).Status != "saved" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if !strings.Contains(r.read(t, "memory/MEMORY.md"), "likes tea") {
		t.Fatal("not saved")
	}
	if _, err := r.d.mcpRemember(context.Background(), r.caller(r.th), rememberParams{Action: "forget", Match: "likes tea"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.read(t, "memory/MEMORY.md"), "likes tea") {
		t.Fatal("not forgotten")
	}
	if c := r.log(t, 1); len(c) == 0 || c[0].Trailers["Argus-Source"] != "agent" {
		t.Fatalf("commit = %+v", c)
	}
}

func TestRememberProfileGoesToReview(t *testing.T) {
	r := newMemRig(t, 0)
	res, err := r.d.mcpRemember(context.Background(), r.caller(r.th), rememberParams{Action: "add", Text: "works in UTC+6", Scope: "profile"})
	if err != nil || res.(rememberResult).Status != "proposed" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	props, err := r.s.PendingProposals(context.Background(), r.a.ID)
	if err != nil || len(props) != 1 {
		t.Fatalf("props = %v err = %v", props, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); strings.Contains(got, "UTC+6") {
		t.Fatal("applied directly")
	}
}

func TestProposeInstructionsAcceptRewrites(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	if _, err := r.d.mcpProposeInstructions(ctx, r.caller(r.th), proposeInstructionsParams{Instructions: "Be terse.", Reason: "user asked"}); err != nil {
		t.Fatal(err)
	}
	props, _ := r.s.PendingProposals(ctx, r.a.ID)
	if len(props) != 1 {
		t.Fatalf("props = %v", props)
	}
	def, _ := pagent.LoadDefinition(r.a.Dir)
	if def.Instructions == "Be terse." {
		t.Fatal("applied before accept")
	}
	res, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props[0].ID, Accept: true})
	if err != nil || res.Commit == "" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	def, _ = pagent.LoadDefinition(r.a.Dir)
	if def.Instructions != "Be terse." {
		t.Fatalf("instructions = %q", def.Instructions)
	}
	repo, err := agentgit.Open(ctx, r.a.Dir, agentgit.ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.Log(ctx, []string{pagent.DefinitionFile}, 1)
	if err != nil || len(commits) != 1 || commits[0].Subject != "agent: update instructions" || commits[0].Trailers["Argus-Source"] != "agent" {
		t.Fatalf("commits = %+v err = %v", commits, err)
	}
	if _, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props[0].ID, Accept: true}); err == nil {
		t.Fatal("accepted twice")
	}
}

func TestRequestHandoffInteraction(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	hookSession(r.d, "%1", "c1")
	s, _ := r.d.reg.FindByAgentSession("claude", "c1")
	caller := callerRef{Local: &session.Session{ID: s.ID, PAgentID: r.a.ID, PAgentThreadID: r.th}}
	if _, err := r.d.mcpRequestHandoff(ctx, caller, requestHandoffParams{Reason: "login", Steps: "sign in"}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.d.reg.Get(s.ID)
	if got.Status != session.StatusAwaitingInput || got.Interaction == nil || got.Interaction.Kind != session.InteractionHandoff ||
		len(got.Interaction.Options) != 2 || got.Interaction.Steps != "sign in" {
		t.Fatalf("session = %+v", got)
	}

	var sent []string
	r.d.deliverFn = func(_ context.Context, _ session.Session, text string) error {
		sent = append(sent, text)
		return nil
	}
	if _, err := r.d.handleSessionRespond(ctx, mustJSON(api.RespondParams{SessionID: s.ID, OptionValue: "takeover"})); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.d.reg.Get(s.ID); got.Interaction == nil || len(sent) != 0 {
		t.Fatalf("takeover changed state: %+v sent=%v", got, sent)
	}
	if _, err := r.d.handleSessionRespond(ctx, mustJSON(api.RespondParams{SessionID: s.ID, OptionValue: "done"})); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.d.reg.Get(s.ID); got.Interaction != nil || len(sent) != 1 || sent[0] != "The user finished: continue." {
		t.Fatalf("after done: %+v sent=%v", got, sent)
	}
}
