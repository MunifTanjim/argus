package node

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/policy"
)

// Who decided, besides the policy.Decide steps.
const (
	decidedUser   = "user"
	decidedExpiry = "expiry"
)

const defaultActivityLimit = 50

// recordActivity stores one approval decision.
func (d *Node) recordActivity(ctx context.Context, sc approvalScope, a policy.Action, decision, by, rule, reason, hook string) {
	if d.bots == nil {
		return
	}
	secrets := d.botSecrets(ctx, sc.bot.ID)
	r := func(s string) string { return bot.Redact(s, secrets) }
	row := bot.Activity{
		BotID: sc.bot.ID, ThreadID: sc.thread.ID, SessionID: sc.sessionID, StartedBy: sc.thread.StartedBy,
		Tool: a.Tool, Target: r(a.Target), Class: strings.Join(a.Class, ","),
		Decision: decision, DecidedBy: by, Rule: r(rule), Reason: r(reason), Hook: r(hook),
	}
	if _, err := d.bots.InsertActivity(context.WithoutCancel(ctx), row); err != nil {
		d.log.Warn("bot: record activity", "bot", sc.bot.ID, "err", err)
	}
}

// recordCard stores the decision that closed a card.
func (d *Node) recordCard(c *approvalCard, decision, by, rule, reason string) {
	d.recordActivity(context.Background(), c.scope, c.action, decision, by, rule, reason, c.hook)
}

// notifyApproval tells the user that unattended work waits on an approval.
func (d *Node) notifyApproval(ctx context.Context, sc approvalScope, a policy.Action) {
	body := a.Tool
	if a.Target != "" {
		body += " " + a.Target
	}
	d.botNotify(context.WithoutCancel(ctx), sc.bot, sc.thread.ID, NotifyNeedsYou, sc.thread.StartedBy, sc.bot.Name+" asks for approval", body)
}

func (d *Node) handleBotActivity(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotActivityParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = defaultActivityLimit
	}
	rows, err := s.ListActivity(ctx, a.ID, p.Unattended, limit)
	if err != nil {
		return nil, botErr("%v", err)
	}
	res := api.BotActivityResult{Activity: make([]api.BotActivity, len(rows))}
	for i, r := range rows {
		var class []string
		if r.Class != "" {
			class = strings.Split(r.Class, ",")
		}
		res.Activity[i] = api.BotActivity{
			ID: r.ID, ThreadID: r.ThreadID, SessionID: r.SessionID, StartedBy: r.StartedBy,
			Tool: r.Tool, Target: r.Target, Class: class, Decision: r.Decision, DecidedBy: r.DecidedBy,
			Rule: r.Rule, Reason: r.Reason, Hook: r.Hook, CreatedAt: r.CreatedAt,
		}
	}
	return res, nil
}
