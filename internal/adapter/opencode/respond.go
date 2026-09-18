package opencode

import (
	"context"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func mapResponse(p api.RespondParams) string {
	if p.Behavior == "deny" || p.OptionValue == "deny" {
		return "reject"
	}
	return "once"
}

func (d *discoverer) respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	d.mu.Lock()
	requestID := d.pendPerm[sess.AgentSessionID]
	d.mu.Unlock()
	if requestID == "" {
		return fmt.Errorf("opencode: no pending permission for session %s", sess.AgentSessionID)
	}
	c, ok := d.dial()
	if !ok {
		return fmt.Errorf("opencode: service unavailable")
	}
	decision := mapResponse(p)
	message := ""
	if decision == "reject" {
		message = p.Reason
	}
	if err := c.respondPermission(ctx, sess.AgentSessionID, requestID, decision, message); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.pendPerm, sess.AgentSessionID)
	d.mu.Unlock()
	return nil
}
