package node

import (
	"context"
	"fmt"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// resolveCaller finds the session that ran a command. The nearest agent process
// in the caller's ancestry decides; variables of other agents are ignored.
func (d *Node) resolveCaller(ctx context.Context, c api.CallerInfo) (session.Session, error) {
	table, err := d.procTable(ctx)
	if err != nil {
		return session.Session{}, fmt.Errorf("read process table: %w", err)
	}
	for _, p := range table.Ancestors(c.PID) {
		for _, a := range d.adapterList {
			ci, ok := a.(adapter.CallerIdentity)
			if !ok || !ci.IsAgentProcess(p, c.Env) {
				continue
			}
			id := c.Env[ci.CallerEnv()]
			if id == "" {
				return session.Session{}, fmt.Errorf("%s process found but %s is not set", a.Agent(), ci.CallerEnv())
			}
			return d.findAgentSession(ctx, a, id)
		}
	}
	return session.Session{}, api.ErrNoCaller
}

// findAgentSession finds the session of a's agent session id, falling back to
// the root session of a subagent id.
func (d *Node) findAgentSession(ctx context.Context, a adapter.Adapter, id string) (session.Session, error) {
	if s, found := d.reg.FindByAgentSession(a.Agent(), id); found {
		return s, nil
	}
	r, ok := a.(adapter.RootSessionResolver)
	if !ok {
		return session.Session{}, fmt.Errorf("%w: %s session %s", api.ErrNoCaller, a.Agent(), id)
	}
	root, err := r.RootSession(ctx, id)
	if err != nil {
		return session.Session{}, fmt.Errorf("%w: %s session %s: %w", api.ErrNoCaller, a.Agent(), id, err)
	}
	if s, found := d.reg.FindByAgentSession(a.Agent(), root); found {
		return s, nil
	}
	return session.Session{}, fmt.Errorf("%w: %s session %s", api.ErrNoCaller, a.Agent(), id)
}
