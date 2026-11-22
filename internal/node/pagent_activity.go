package node

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
)

// Who decided, besides the policy.Decide steps.
const (
	decidedUser   = "user"
	decidedExpiry = "expiry"
)

const defaultActivityLimit = 50

// recordActivity stores one approval decision.
func (d *Node) recordActivity(ctx context.Context, sc approvalScope, a policy.Action, decision, by, rule, reason, hook string) {
	if d.pagents == nil {
		return
	}
	secrets := d.agentSecrets(ctx, sc.agent.ID)
	r := func(s string) string { return pagent.Redact(s, secrets) }
	row := pagent.Activity{
		AgentID: sc.agent.ID, ThreadID: sc.thread.ID, SessionID: sc.sessionID, StartedBy: sc.thread.StartedBy,
		Tool: a.Tool, Target: r(a.Target), Class: strings.Join(a.Class, ","),
		Decision: decision, DecidedBy: by, Rule: r(rule), Reason: r(reason), Hook: r(hook),
	}
	if _, err := d.pagents.InsertActivity(context.WithoutCancel(ctx), row); err != nil {
		d.log.Warn("pagent: record activity", "agent", sc.agent.ID, "err", err)
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
	d.pagentNotify(context.WithoutCancel(ctx), sc.agent, sc.thread.ID, NotifyNeedsYou, sc.thread.StartedBy, sc.agent.Name+" asks for approval", body)
}

func (d *Node) handlePAgentActivity(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentActivityParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = defaultActivityLimit
	}
	rows, err := s.ListActivity(ctx, a.ID, p.Unattended, limit)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	res := api.PAgentActivityResult{Activity: make([]api.PAgentActivity, len(rows))}
	for i, r := range rows {
		var class []string
		if r.Class != "" {
			class = strings.Split(r.Class, ",")
		}
		res.Activity[i] = api.PAgentActivity{
			ID: r.ID, ThreadID: r.ThreadID, SessionID: r.SessionID, StartedBy: r.StartedBy,
			Tool: r.Tool, Target: r.Target, Class: class, Decision: r.Decision, DecidedBy: r.DecidedBy,
			Rule: r.Rule, Reason: r.Reason, Hook: r.Hook, CreatedAt: r.CreatedAt,
		}
	}
	return res, nil
}
