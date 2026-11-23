package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/routine"
)

// evTokenBudgetUnavailable notes once per routine and CLI that a token budget
// is not enforced: the bot's CLI does not report its token usage.
const evTokenBudgetUnavailable = "routine.token_budget_unavailable"

// tokenReportingCLIs are the CLIs whose sessions report their total tokens
// (session.Summary.TotalTokens).
var tokenReportingCLIs = map[string]bool{"claude": true, "codex": true, "opencode": true, "pi": true}

func reportsTotalTokens(cli string) bool { return tokenReportingCLIs[cli] }

// enforcedBudget is b without its token limit when cli does not report tokens.
func enforcedBudget(b routine.Budget, cli string) routine.Budget {
	if !reportsTotalTokens(cli) {
		b.Tokens = 0
	}
	return b
}

// tokenBudgetNotice posts one notice, in the bot's latest chat, that the
// routine's token budget is not enforced for cli. It is a no-op when the
// budget has no token limit, cli reports tokens, or that chat already has the
// notice.
func (d *Node) tokenBudgetNotice(ctx context.Context, a bot.Bot, r routine.Routine, cli string) {
	if r.Budget.Tokens <= 0 || reportsTotalTokens(cli) {
		return
	}
	th, ok, err := d.bots.LatestThread(ctx, a.ID, threadChat)
	if err != nil || !ok {
		return
	}
	evs, err := d.bots.Events(ctx, th.ID, 0)
	if err != nil {
		return
	}
	for _, ev := range evs {
		if ev.Kind != evTokenBudgetUnavailable {
			continue
		}
		var p struct{ Routine, CLI string }
		if json.Unmarshal(ev.Payload, &p) == nil && p.Routine == r.Slug && p.CLI == cli {
			return
		}
	}
	d.logEvent(ctx, th.ID, evTokenBudgetUnavailable, map[string]string{
		"routine": r.Slug, "cli": cli, "message": "token budget unavailable for " + cli,
	}, "")
}
