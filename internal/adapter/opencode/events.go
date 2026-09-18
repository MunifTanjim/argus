package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type globalEvent struct {
	Directory string       `json:"directory"`
	Payload   eventPayload `json:"payload"`
}

type eventPayload struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

func parseSSE(r io.Reader, emit func(globalEvent)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var ev globalEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		emit(ev)
	}
	return sc.Err()
}

func (d *discoverer) runEventPump(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if ctx.Err() != nil {
			return
		}
		c, ok := d.dial()
		if !ok {
			if sleep(ctx, 3*time.Second) {
				return
			}
			continue
		}
		body, err := c.openEvents(ctx)
		if err != nil {
			if sleep(ctx, 3*time.Second) {
				return
			}
			continue
		}
		_ = parseSSE(body, d.applyEvent)
		body.Close()
		if sleep(ctx, time.Second) {
			return
		}
	}
}

func sleep(ctx context.Context, dur time.Duration) (canceled bool) {
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

func (d *discoverer) applyEvent(ev globalEvent) {
	switch ev.Payload.Type {
	case "session.status":
		var p struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		_ = json.Unmarshal(ev.Payload.Properties, &p)
		if p.Status.Type != "idle" {
			d.setStatus(p.SessionID, session.StatusWorking, nil)
		}
	case "message.part.updated":
		var p struct {
			Part struct {
				SessionID string `json:"sessionID"`
			} `json:"part"`
		}
		_ = json.Unmarshal(ev.Payload.Properties, &p)
		d.setStatus(p.Part.SessionID, session.StatusWorking, nil)
	case "session.idle":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(ev.Payload.Properties, &p)
		d.setStatus(p.SessionID, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	case "permission.updated":
		var p struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			Type      string `json:"type"`
		}
		_ = json.Unmarshal(ev.Payload.Properties, &p)
		d.mu.Lock()
		d.pendPerm[p.SessionID] = p.ID
		d.mu.Unlock()
		d.setStatus(p.SessionID, session.StatusAwaitingInput, &session.Interaction{
			Kind:     session.InteractionPermission,
			ToolName: p.Type,
			Options: []session.DecisionOption{
				{Label: "Allow", Value: "allow"},
				{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell OpenCode why"},
			},
		})
	case "permission.replied":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(ev.Payload.Properties, &p)
		d.mu.Lock()
		delete(d.pendPerm, p.SessionID)
		d.mu.Unlock()
		d.clearInteraction(p.SessionID)
	}
}

func (d *discoverer) setStatus(agentSessionID string, st session.Status, in *session.Interaction) {
	if agentSessionID == "" {
		return
	}
	u := registry.HookUpdate{
		Agent:          Agent,
		AgentSessionID: agentSessionID,
		Status:         st,
		Frontend:       session.FrontendExternal,
	}
	if in != nil {
		u.Interaction = in
		u.ReplaceInteraction = true
	}
	d.reg.ApplyHook(u)
}

func (d *discoverer) clearInteraction(agentSessionID string) {
	if id, ok := d.argusIDFor(agentSessionID); ok {
		d.reg.ClearInteraction(id)
	}
}

func (d *discoverer) argusIDFor(agentSessionID string) (string, bool) {
	for _, s := range d.reg.Snapshot() {
		if s.Agent == Agent && s.AgentSessionID == agentSessionID {
			return s.ID, true
		}
	}
	return "", false
}
