package pi

import (
	"context"
	"fmt"

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
	switch pend.method {
	case "select":
		if denied {
			resp["cancelled"] = true
		} else {
			resp["value"] = selectValue(pend.options, p.OptionValue)
		}
	case "confirm":
		resp["confirmed"] = !denied
	case "input", "editor":
		if denied {
			resp["cancelled"] = true
		} else {
			resp["value"] = inputValue(pend, p)
		}
	default:
		return fmt.Errorf("pi: cannot answer dialog method %q", pend.method)
	}
	if err := proc.conn.send(resp); err != nil {
		return err
	}
	proc.mu.Lock()
	delete(proc.pending, pend.uiID)
	stillPending := len(proc.pending) > 0
	proc.mu.Unlock()
	// The blocked handler resumes into tool execution (or the command
	// completes); refresh the card now so a command-sourced dialog doesn't
	// wedge the session at its prompt. Later events correct the status.
	if !stillPending {
		d.upsert(proc, session.StatusWorking, nil, false)
	}
	return nil
}

// selectValue picks the option confirming an allow decision: the echoed
// OptionValue when it names a real option, else an "Allow"-like label, else
// the first option. The gate extension offers Allow-first options.
func selectValue(options []string, want string) string {
	for _, o := range options {
		if o == want {
			return o
		}
	}
	for _, o := range options {
		if o == "Allow" || o == "allow" || o == "Yes" || o == "yes" {
			return o
		}
	}
	if len(options) > 0 {
		return options[0]
	}
	return "Allow"
}

// inputValue resolves the free-text answer for input/editor dialogs: the
// answer keyed by dialog title, else the note for it, else empty.
func inputValue(pend *pendingUI, p api.RespondParams) string {
	if p.Answers != nil {
		if v, ok := p.Answers[pend.title].(string); ok {
			return v
		}
		if v, ok := p.Answers[pend.message].(string); ok {
			return v
		}
	}
	if p.Notes != nil {
		if v, ok := p.Notes[pend.title]; ok {
			return v
		}
		if v, ok := p.Notes[pend.message]; ok {
			return v
		}
	}
	return ""
}
