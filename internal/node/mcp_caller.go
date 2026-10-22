package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// resolveMCPCaller finds the session behind an MCP tools/call from the identity
// the agent runtime put in _meta.
func (d *Node) resolveMCPCaller(ctx context.Context, agent string, meta map[string]json.RawMessage) (session.Session, error) {
	a, ok := d.adapters[agent]
	if !ok {
		return session.Session{}, fmt.Errorf("unknown agent %q", agent)
	}
	mi, ok := a.(adapter.MCPIdentity)
	if !ok {
		return session.Session{}, fmt.Errorf("%w: %s has no mcp identity", api.ErrNoCaller, agent)
	}
	id, err := mi.MCPSessionID(meta)
	if err != nil {
		return session.Session{}, fmt.Errorf("%s: %w", agent, err)
	}
	if s, found := d.reg.FindByAgentSession(agent, id); found {
		return s, nil
	}
	r, ok := a.(adapter.RootSessionResolver)
	if !ok {
		return session.Session{}, fmt.Errorf("%w: %s session %s", api.ErrNoCaller, agent, id)
	}
	root, err := r.RootSession(ctx, id)
	if err != nil {
		return session.Session{}, fmt.Errorf("%w: %s session %s: %v", api.ErrNoCaller, agent, id, err)
	}
	if s, found := d.reg.FindByAgentSession(agent, root); found {
		return s, nil
	}
	return session.Session{}, fmt.Errorf("%w: %s session %s", api.ErrNoCaller, agent, id)
}
