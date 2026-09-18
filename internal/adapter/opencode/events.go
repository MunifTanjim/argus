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

// eventIdleTimeout closes an /api/event connection that goes silent for longer
// than this. The live v2.0.8 server emits ": heartbeat" every ~15s, so 3x the
// cadence tolerates a missed beat while still catching a half-open TCP socket
// that would otherwise wedge the scanner forever.
const eventIdleTimeout = 45 * time.Second

// idleReader wraps an SSE body and closes the underlying connection when no
// bytes arrive within timeout, unblocking a stalled bufio.Scanner so the pump
// can reconnect. The timer is reset on every Read attempt (each frame,
// heartbeat, or comment line resets it).
type idleReader struct {
	rc      io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
}

func newIdleReader(rc io.ReadCloser, timeout time.Duration) *idleReader {
	ir := &idleReader{rc: rc, timeout: timeout}
	ir.timer = time.AfterFunc(timeout, ir.onIdle)
	return ir
}

func (ir *idleReader) onIdle() {
	_ = ir.rc.Close()
}

func (ir *idleReader) Read(p []byte) (int, error) {
	ir.timer.Reset(ir.timeout)
	return ir.rc.Read(p)
}

func (ir *idleReader) Close() error {
	ir.timer.Stop()
	return ir.rc.Close()
}

// sseFrame is the envelope for every SSE event emitted by /api/event.
// Verified against the live v2.0.8 stream: {"id":string,"type":string,"data":{...}}.
// Event type names for session-idle and permission flows are matched by string
// in applyEvent; unknown types are silently ignored.
type sseFrame struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func parseSSE(r io.Reader, emit func(sseFrame)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var frame sseFrame
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			continue
		}
		emit(frame)
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
		ir := newIdleReader(body, eventIdleTimeout)
		_ = parseSSE(ir, d.applyEvent)
		ir.Close()
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

// sessionIDFrom extracts a session ID from an SSE event data payload.
// It tries the top-level sessionID field, then info.id, then part.sessionID.
func sessionIDFrom(data json.RawMessage) string {
	var p struct {
		SessionID string `json:"sessionID"`
		Info      struct {
			ID string `json:"id"`
		} `json:"info"`
		Part struct {
			SessionID string `json:"sessionID"`
		} `json:"part"`
	}
	_ = json.Unmarshal(data, &p)
	if p.SessionID != "" {
		return p.SessionID
	}
	if p.Info.ID != "" {
		return p.Info.ID
	}
	return p.Part.SessionID
}

func (d *discoverer) applyEvent(frame sseFrame) {
	switch frame.Type {
	case "message.part.updated":
		sid := sessionIDFrom(frame.Data)
		d.setStatus(sid, session.StatusWorking, nil)

	case "session.status":
		var p struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		_ = json.Unmarshal(frame.Data, &p)
		if p.SessionID != "" && p.Status.Type != "" && p.Status.Type != "idle" {
			d.setStatus(p.SessionID, session.StatusWorking, nil)
		}

	case "session.idle":
		sid := sessionIDFrom(frame.Data)
		d.setStatus(sid, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})

	case "permission.updated", "permission.request":
		var p struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			Action    string `json:"action"`
			Source    *struct {
				Type string `json:"type"`
			} `json:"source"`
		}
		_ = json.Unmarshal(frame.Data, &p)
		if p.SessionID == "" || p.ID == "" {
			return
		}
		d.mu.Lock()
		d.pendPerm[p.SessionID] = p.ID
		d.mu.Unlock()
		toolName := p.Action
		if toolName == "" && p.Source != nil {
			toolName = p.Source.Type
		}
		d.setStatus(p.SessionID, session.StatusAwaitingInput, &session.Interaction{
			Kind:     session.InteractionPermission,
			ToolName: toolName,
			Options: []session.DecisionOption{
				{Label: "Allow", Value: "allow"},
				{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell OpenCode why"},
			},
		})

	case "permission.replied":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(frame.Data, &p)
		d.mu.Lock()
		delete(d.pendPerm, p.SessionID)
		d.mu.Unlock()
		d.clearInteraction(p.SessionID)

	case "server.connected":
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
