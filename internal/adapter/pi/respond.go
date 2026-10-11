package pi

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// respond answers a parked extension dialog with extension_ui_response.
// The dialog to answer is picked by RequestID when the client echoes it,
// else the single pending dialog; ambiguous state is an error rather than a
// guess at which question the user saw.
func (d *discoverer) respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	proc := d.get(sess.AgentSessionID)
	if proc == nil {
		return fmt.Errorf("pi: unknown session %s", sess.AgentSessionID)
	}
	proc.mu.Lock()
	var pend *pendingUI
	if p.RequestID != "" {
		pend = proc.pending[p.RequestID]
	} else if len(proc.pending) == 1 {
		for _, v := range proc.pending {
			pend = v
		}
	}
	proc.mu.Unlock()
	if pend == nil {
		return fmt.Errorf("pi: no pending dialog for session %s", sess.AgentSessionID)
	}
	resp := map[string]any{"type": "extension_ui_response", "id": pend.uiID}
	denied := p.Behavior == "deny" || p.OptionValue == "deny"
	cancelled := denied || p.QuestionAction != ""
	switch pend.method {
	case "select":
		if cancelled {
			resp["cancelled"] = true
			break
		}
		v := answerText(pend, p)
		if !slices.Contains(pend.options, v) {
			return fmt.Errorf("pi: %q is not an option of %q", v, cmp.Or(pend.title, pend.message))
		}
		resp["value"] = v
	case "confirm":
		resp["confirmed"] = !denied
	case "approval":
		answer := map[string]any{"allow": !denied}
		if denied && p.Reason != "" {
			answer["reason"] = p.Reason
		}
		b, err := json.Marshal(answer)
		if err != nil {
			return err
		}
		resp["value"] = string(b)
	case "input", "editor":
		if cancelled {
			resp["cancelled"] = true
		} else {
			resp["value"] = answerText(pend, p)
		}
	default:
		return fmt.Errorf("pi: cannot answer dialog method %q", pend.method)
	}
	if err := proc.conn.send(resp); err != nil {
		return err
	}
	// The blocked handler resumes into tool execution (or the command
	// completes); refresh the card now so a command-sourced dialog doesn't
	// wedge the session at its prompt. Later events correct the status.
	d.dropOwnedPending(proc, pend.uiID)
	return nil
}

// answerText resolves a dialog's answer: the answer keyed by its question (the
// title, else the message), else the note for it, else empty.
func answerText(pend *pendingUI, p api.RespondParams) string {
	keys := []string{pend.title, pend.message}
	for _, key := range keys {
		switch v := p.Answers[key].(type) {
		case string:
			return v
		case []any:
			if len(v) > 0 {
				if s, ok := v[0].(string); ok {
					return s
				}
			}
		}
	}
	for _, key := range keys {
		if v, ok := p.Notes[key]; ok {
			return v
		}
	}
	return ""
}
