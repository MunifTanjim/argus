package opencode

import (
	"context"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

var activeDiscoverer *discoverer

func mapResponse(p api.RespondParams) string {
	if p.Behavior == "deny" || p.OptionValue == "deny" {
		return "reject"
	}
	// provisional: SetMode=="always" as the "remember" signal; verify in Task 10 E2E
	if p.SetMode == "always" {
		return "always"
	}
	return "once"
}

func respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	d := activeDiscoverer
	if d == nil {
		return fmt.Errorf("opencode: no active discoverer")
	}
	d.mu.Lock()
	permID := d.pendPerm[sess.AgentSessionID]
	d.mu.Unlock()
	if permID == "" {
		return fmt.Errorf("opencode: no pending permission for session %s", sess.AgentSessionID)
	}
	c, ok := d.dial()
	if !ok {
		return fmt.Errorf("opencode: service unavailable")
	}
	if err := c.respondPermission(ctx, sess.AgentSessionID, permID, mapResponse(p)); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.pendPerm, sess.AgentSessionID)
	d.mu.Unlock()
	return nil
}
