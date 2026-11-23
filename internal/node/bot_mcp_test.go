package node

import (
	"context"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/botgit"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func (r *memRig) caller(thread string) callerRef {
	return callerRef{Local: &session.Session{ID: "claude:c1", BotID: r.a.ID, BotThreadID: thread}}
}

func TestRememberRefusedInRun(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.s.InsertThread(context.Background(), bot.Thread{ID: "th_run", BotID: r.a.ID, Kind: "run", StartedBy: "routine"}); err != nil {
		t.Fatal(err)
	}
	_, err := r.d.mcpRemember(context.Background(), r.caller("th_run"), rememberParams{Action: "add", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "memory is written only from chats") {
		t.Fatalf("err = %v", err)
	}
	_, err = r.d.mcpRemember(context.Background(), callerRef{Local: &session.Session{ID: "claude:c2"}}, rememberParams{Action: "add", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "not a bot session") {
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
	if c := r.log(t, 1); len(c) == 0 || c[0].Trailers["Argus-Source"] != "bot" {
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
	def, _ := bot.LoadDefinition(r.a.Dir)
	if def.Instructions == "Be terse." {
		t.Fatal("applied before accept")
	}
	res, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props[0].ID, Accept: true})
	if err != nil || res.Commit == "" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	def, _ = bot.LoadDefinition(r.a.Dir)
	if def.Instructions != "Be terse." {
		t.Fatalf("instructions = %q", def.Instructions)
	}
	repo, err := botgit.Open(ctx, r.a.Dir, botgit.ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.Log(ctx, []string{bot.DefinitionFile}, 1)
	if err != nil || len(commits) != 1 || commits[0].Subject != "bot: update instructions" || commits[0].Trailers["Argus-Source"] != "bot" {
		t.Fatalf("commits = %+v err = %v", commits, err)
	}
	if _, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props[0].ID, Accept: true}); err == nil {
		t.Fatal("accepted twice")
	}
}

func TestRequestHandoffInteraction(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	hookSession(r.d, "%1", "c1")
	s, _ := r.d.reg.FindByAgentSession("claude", "c1")
	caller := callerRef{Local: &session.Session{ID: s.ID, BotID: r.a.ID, BotThreadID: r.th}}
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

func handoffRig(t *testing.T) (*memRig, session.Session, *[]string) {
	t.Helper()
	r := newMemRig(t, 0)
	r.d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", AgentSessionID: "c1", Status: session.StatusWorking})
	s, _ := r.d.reg.FindByAgentSession("claude", "c1")
	caller := callerRef{Local: &session.Session{ID: s.ID, BotID: r.a.ID, BotThreadID: r.th}}
	if _, err := r.d.mcpRequestHandoff(context.Background(), caller, requestHandoffParams{Reason: "login", Steps: "sign in"}); err != nil {
		t.Fatal(err)
	}
	sent := &[]string{}
	r.d.deliverFn = func(_ context.Context, _ session.Session, text string) error {
		*sent = append(*sent, text)
		return nil
	}
	return r, s, sent
}

func TestHandoffAndPermissionAnsweredByRequestID(t *testing.T) {
	r, s, sent := handoffRig(t)
	ctx := context.Background()
	r.d.reg.PushDecision(s.ID, session.Interaction{Kind: session.InteractionPermission, RequestID: "perm1"})
	// A permission answer must not reach the handoff.
	r.d.reg.DropDecision(s.ID, "perm1")
	r.d.reg.PushDecision(s.ID, session.Interaction{Kind: session.InteractionPermission, RequestID: "perm1"})
	_, err := r.d.handleSessionRespond(ctx, mustJSON(api.RespondParams{SessionID: s.ID, RequestID: "perm1", OptionValue: "allow"}))
	if err != nil && strings.Contains(err.Error(), "handoff") {
		t.Fatalf("permission answer went to handoff: %v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("sent = %v", *sent)
	}
	if _, ok := r.d.reg.Decision(s.ID, handoffRequestID); !ok {
		t.Fatal("handoff dropped by permission answer")
	}
	r.d.reg.DropDecision(s.ID, "perm1")
	if _, err := r.d.handleSessionRespond(ctx, mustJSON(api.RespondParams{SessionID: s.ID, RequestID: handoffRequestID, OptionValue: "done"})); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 {
		t.Fatalf("sent = %v", *sent)
	}
}

func TestHandoffDoneRefusedWhilePermissionPending(t *testing.T) {
	r, s, sent := handoffRig(t)
	r.d.reg.PushDecision(s.ID, session.Interaction{Kind: session.InteractionPermission, RequestID: "perm1"})
	_, err := r.d.handleSessionRespond(context.Background(), mustJSON(api.RespondParams{SessionID: s.ID, RequestID: handoffRequestID, OptionValue: "done"}))
	if err == nil || !strings.Contains(err.Error(), "answer the bot's pending request first") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := r.d.reg.Decision(s.ID, handoffRequestID); !ok || len(*sent) != 0 {
		t.Fatal("handoff changed")
	}
}

func TestHandoffDroppedWhenNewTurnStarts(t *testing.T) {
	r, s, sent := handoffRig(t)
	ctx := context.Background()
	hook := func(st session.Status) {
		r.d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1", AgentSessionID: "c1", Status: st})
	}
	hook(session.StatusWorking) // tool call return in the same turn
	if _, ok := r.d.reg.Decision(s.ID, handoffRequestID); !ok {
		t.Fatal("dropped within the turn")
	}
	hook(session.StatusIdle) // bot ends its turn
	if _, ok := r.d.reg.Decision(s.ID, handoffRequestID); !ok {
		t.Fatal("dropped at turn end")
	}
	hook(session.StatusWorking) // user typed in the terminal
	if _, ok := r.d.reg.Decision(s.ID, handoffRequestID); ok {
		t.Fatal("not dropped")
	}
	_, err := r.d.handleSessionRespond(ctx, mustJSON(api.RespondParams{SessionID: s.ID, RequestID: handoffRequestID, OptionValue: "done"}))
	if err == nil || !strings.Contains(err.Error(), "no pending handoff") || len(*sent) != 0 {
		t.Fatalf("err = %v sent = %v", err, *sent)
	}
}
