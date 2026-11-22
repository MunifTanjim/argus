package pagent

import (
	"context"
	"time"

	"github.com/MunifTanjim/argus/internal/db/gen"
)

// Activity is one approval decision on an agent action.
type Activity struct {
	ID                             int64
	AgentID, ThreadID, SessionID   string
	StartedBy, Tool, Target, Class string
	// Decision is allow, deny or draft; DecidedBy is floor, policy, hook,
	// rule, grant, default, user or expiry.
	Decision, DecidedBy string
	Rule, Reason        string
	// Hook is the policy hook's answer when it ran in shadow mode.
	Hook      string
	CreatedAt time.Time
}

// InsertActivity stores a, setting CreatedAt.
func (s *Store) InsertActivity(ctx context.Context, a Activity) (int64, error) {
	return s.q.InsertPAgentActivity(ctx, gen.InsertPAgentActivityParams{
		AgentID: a.AgentID, ThreadID: a.ThreadID, SessionID: a.SessionID, StartedBy: a.StartedBy,
		Tool: a.Tool, Target: a.Target, Class: a.Class, Decision: a.Decision, DecidedBy: a.DecidedBy,
		Rule: a.Rule, Reason: a.Reason, Hook: a.Hook, CreatedAt: s.now().UnixNano(),
	})
}

// ListActivity returns the agent's decisions, newest first, at most limit
// (all when limit <= 0); unattended keeps only work not started by the user.
func (s *Store) ListActivity(ctx context.Context, agentID string, unattended bool, limit int) ([]Activity, error) {
	lim := int64(limit)
	if lim <= 0 {
		lim = -1
	}
	var u int64
	if unattended {
		u = 1
	}
	rows, err := s.q.ListPAgentActivity(ctx, gen.ListPAgentActivityParams{AgentID: agentID, Unattended: u, Lim: lim})
	if err != nil {
		return nil, err
	}
	out := make([]Activity, len(rows))
	for i, r := range rows {
		out[i] = Activity{
			ID: r.ID, AgentID: r.AgentID, ThreadID: r.ThreadID, SessionID: r.SessionID, StartedBy: r.StartedBy,
			Tool: r.Tool, Target: r.Target, Class: r.Class, Decision: r.Decision, DecidedBy: r.DecidedBy,
			Rule: r.Rule, Reason: r.Reason, Hook: r.Hook, CreatedAt: fromNanos(r.CreatedAt),
		}
	}
	return out, nil
}
