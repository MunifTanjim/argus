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
			s, found := d.reg.FindByAgentSession(a.Agent(), id)
			if !found {
				return session.Session{}, fmt.Errorf("%w: %s session %s", api.ErrNoCaller, a.Agent(), id)
			}
			return s, nil
		}
	}
	return session.Session{}, api.ErrNoCaller
}
