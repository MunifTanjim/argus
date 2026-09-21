package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

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

// sessionIDFrom extracts a session ID from an SSE event data payload,
// trying the top-level, info, and part locations in that order.
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
	case "session.execution.started", "session.step.started", "session.step.streamed",
		"session.text.started", "session.text.delta", "session.text.ended":
		id := sessionIDFrom(frame.Data)
		// A reasoning model emits a trailing activity event after form.created or
		// permission.asked (observed with muse). A bare Working upsert would clear the
		// pending prompt (mergeInteraction drops a rich interaction for a nil one). The
		// session waits on the user, not the agent, so hold it at the prompt until the
		// reply event clears the pending entry.
		if d.hasPendingPrompt(id) {
			return
		}
		d.upsert(id, session.StatusWorking, nil)

	case "session.execution.succeeded", "session.execution.failed":
		d.upsert(sessionIDFrom(frame.Data), session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})

	case "permission.asked":
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
		src := ""
		if p.Source != nil {
			src = p.Source.Type
		}
		d.applyPermission(p.SessionID, p.ID, p.Action, src)

	case "permission.replied", "permission.rejected":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(frame.Data, &p)
		d.mu.Lock()
		delete(d.pendPerm, p.SessionID)
		d.mu.Unlock()
		d.upsert(p.SessionID, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})

	case "form.created":
		d.applyFormCreated(frame.Data)

	case "form.replied", "form.cancelled":
		sid := sessionIDFrom(frame.Data)
		if sid == "" {
			return
		}
		d.mu.Lock()
		delete(d.pendForm, sid)
		d.mu.Unlock()
		d.upsert(sid, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})

	case "session.deleted":
		d.remove(sessionIDFrom(frame.Data))

	case "server.connected":
	}
}

// formInfo is the Form.Info payload of a form.created event. OpenCode delivers
// it either nested under data.form or flat as the data object; applyFormCreated
// tolerates both.
type formInfo struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionID"`
	Title     string      `json:"title"`
	Fields    []formField `json:"fields"`
}

type formField struct {
	Key         string       `json:"key"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Required    bool         `json:"required"`
	Type        string       `json:"type"`
	Options     []formOption `json:"options"`
}

type formOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ocPermission is a Permission.Request item from the permission.asked event or the
// per-session permission list.
type ocPermission struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Source *struct {
		Type string `json:"type"`
	} `json:"source"`
}

func (d *discoverer) applyFormCreated(data json.RawMessage) {
	var wrap struct {
		Form *formInfo `json:"form"`
	}
	_ = json.Unmarshal(data, &wrap)
	info := wrap.Form
	if info == nil || info.SessionID == "" {
		var flat formInfo
		if json.Unmarshal(data, &flat) == nil {
			info = &flat
		}
	}
	if info == nil || info.SessionID == "" || info.ID == "" {
		return
	}
	d.applyForm(info)
}

// applyPermission is shared by the permission.asked event and the scan reconcile
// path, so both surface an identical permission interaction.
func (d *discoverer) applyPermission(sessionID, requestID, action, sourceType string) {
	d.mu.Lock()
	d.pendPerm[sessionID] = requestID
	d.mu.Unlock()
	toolName := action
	if toolName == "" {
		toolName = sourceType
	}
	d.upsert(sessionID, session.StatusAwaitingInput, &session.Interaction{
		Kind:     session.InteractionPermission,
		ToolName: toolName,
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell OpenCode why"},
		},
	})
}

// applyForm is shared by the form.created event and the scan reconcile path
// (backfill after a missed event), so both surface an identical question interaction.
func (d *discoverer) applyForm(info *formInfo) {
	pf := &pendingForm{formID: info.ID}
	var questions []session.QuestionSpec
	for _, f := range info.Fields {
		if f.Type == "hidden" || f.Type == "external" {
			continue
		}
		header := f.Title
		if header == "" {
			header = info.Title
		}
		question := f.Description
		if question == "" {
			question = f.Title
		}
		spec := session.QuestionSpec{
			Header:      header,
			Question:    question,
			MultiSelect: f.Type == "multiselect",
		}
		valueByLabel := map[string]string{}
		for _, opt := range f.Options {
			spec.Options = append(spec.Options, opt.Label)
			spec.OptionDescriptions = append(spec.OptionDescriptions, opt.Description)
			valueByLabel[opt.Label] = opt.Value
		}
		questions = append(questions, spec)
		pf.fields = append(pf.fields, pendingField{
			key:          f.Key,
			question:     question,
			multiselect:  f.Type == "multiselect",
			valueByLabel: valueByLabel,
		})
	}

	d.mu.Lock()
	d.pendForm[info.SessionID] = pf
	d.mu.Unlock()

	d.upsert(info.SessionID, session.StatusAwaitingInput, &session.Interaction{
		Kind:      session.InteractionQuestion,
		Message:   info.Title,
		Questions: questions,
	})
}
