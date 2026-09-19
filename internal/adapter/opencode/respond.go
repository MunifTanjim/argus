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
	pf := d.pendForm[sess.AgentSessionID]
	d.mu.Unlock()
	if pf != nil {
		return d.replyForm(ctx, sess, p, pf)
	}

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

func (d *discoverer) replyForm(ctx context.Context, sess session.Session, p api.RespondParams, pf *pendingForm) error {
	c, ok := d.dial()
	if !ok {
		return fmt.Errorf("opencode: service unavailable")
	}
	answer := map[string]any{}
	for _, f := range pf.fields {
		raw, ok := p.Answers[f.question]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case string:
			answer[f.key] = f.translate(v)
		case []any:
			vals := make([]string, 0, len(v))
			for _, item := range v {
				if label, ok := item.(string); ok {
					vals = append(vals, f.translate(label))
				}
			}
			answer[f.key] = vals
		}
	}
	if err := c.replyForm(ctx, sess.AgentSessionID, pf.formID, answer); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.pendForm, sess.AgentSessionID)
	d.mu.Unlock()
	return nil
}

func (f pendingField) translate(label string) string {
	if v, ok := f.valueByLabel[label]; ok {
		return v
	}
	return label
}
