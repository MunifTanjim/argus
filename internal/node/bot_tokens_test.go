package node

import (
	"context"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/routine"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// setUsage reports the session's total tokens with status st.
func (r *threadRig) setUsage(id string, tokens int64, st session.Status) {
	s, _ := r.d.reg.Get(id)
	r.d.reg.ApplyHook(registry.HookUpdate{
		Agent: s.Agent, Server: s.Tmux.Server, PaneID: s.Tmux.PaneID, Status: st,
		Summary: &session.Summary{Tokens: 1, TotalTokens: tokens},
	})
}

func TestRunRecordsTotalTokensAndSettlesTheLastTurn(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "billing", goalBody+"budget: { tokens: 700 }\n")
	c := r.start(t, rt)
	_, sid := c.ids()
	r.setUsage(sid, 500, session.StatusWorking)
	r.until(t, "usage seen", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.summary != nil && c.summary.TotalTokens == 500
	})
	r.report(t, c, reportRunParams{Result: "progress", Summary: "Half way."})
	r.until(t, "run finished", func() bool { return r.run(t, c.run.ID).State == bot.RunDone })
	if got := r.run(t, c.run.ID); got.Tokens != 500 {
		t.Fatalf("run tokens at report = %d, want 500", got.Tokens)
	}
	if st := r.state(t, "billing"); !st.Enabled {
		t.Fatalf("paused under budget: %+v", st)
	}
	// The turn that called report_run ends after it.
	r.setUsage(sid, 800, session.StatusIdle)
	r.wait(t)
	if got := r.run(t, c.run.ID); got.Tokens != 800 {
		t.Fatalf("run tokens = %d, want 800", got.Tokens)
	}
	st := r.state(t, "billing")
	if st.TokensUsed != 800 || st.Enabled || st.PausedReason != pausedBudget {
		t.Fatalf("state: %+v", st)
	}
}

func TestTokenBudgetUnavailableForCLI(t *testing.T) {
	r := newRunRig(t)
	r.d.adapters["antigravity"] = r.d.adapters["claude"]
	r.d.botCfg.Defaults.Agent = "antigravity"
	rt := r.routine(t, "billing", goalBody+"budget: { tokens: 10 }\n")
	ctx := context.Background()
	// Used tokens from an earlier CLI do not pause it either.
	if _, err := r.store.RecordRunOutcome(ctx, r.a.ID, "billing", bot.RunOutcome{Result: "progress", Tokens: 50}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		c := r.start(t, rt)
		r.report(t, c, reportRunParams{Result: "progress", Summary: "ok"})
		r.wait(t)
	}
	if st := r.state(t, "billing"); !st.Enabled {
		t.Fatalf("token budget enforced: %+v", st)
	}
	ev := r.chatEvents(t, evTokenBudgetUnavailable)
	if len(ev) != 1 || ev[0]["routine"] != "billing" || ev[0]["cli"] != "antigravity" || ev[0]["message"] != "token budget unavailable for antigravity" {
		t.Fatalf("notices: %+v", ev)
	}
}

func TestNoTokenNoticeForReportingCLI(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "billing", goalBody+"budget: { tokens: 1000 }\n")
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "progress", Summary: "ok"})
	r.wait(t)
	if ev := r.chatEvents(t, evTokenBudgetUnavailable); len(ev) != 0 {
		t.Fatalf("notices: %+v", ev)
	}
}

func TestEnforcedBudget(t *testing.T) {
	b := routine.Budget{Sessions: 2, Cost: 1, Tokens: 100}
	if got := enforcedBudget(b, "claude"); got != b {
		t.Fatalf("claude: %+v", got)
	}
	if got := enforcedBudget(b, "antigravity"); got.Tokens != 0 || got.Sessions != 2 || got.Cost != 1 {
		t.Fatalf("antigravity: %+v", got)
	}
}

func TestUsageCountsChatTotalTokens(t *testing.T) {
	r := newRunRig(t)
	res, err := r.send(t, api.BotSendParams{Text: "hello", New: true})
	if err != nil {
		t.Fatal(err)
	}
	links := r.links(t, res.ThreadID)
	if len(links) != 1 {
		t.Fatalf("links: %+v", links)
	}
	r.setUsage(links[0].SessionID, 4321, session.StatusIdle)
	u, err := callBot[api.BotUsageResult](t, r.d.handleBotUsage, api.BotUsageParams{BotID: r.a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if u.Total.Tokens != 4321 {
		t.Fatalf("usage: %+v", u)
	}
}
