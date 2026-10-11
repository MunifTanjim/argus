package pi

import (
	"context"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// buildUIResponse renders a parked dialog's answer as the extension_ui_response
// record (owned RPC sessions) or approval_decision (foreign beacons) payload.
// Both channels share the decision mapping; callers add the envelope.
func buildUIResponse(pend *pendingUI, p api.RespondParams) (map[string]any, error) {
	resp := map[string]any{}
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
		return nil, fmt.Errorf("pi: cannot answer dialog method %q", pend.method)
	}
	return resp, nil
}

// respond answers a parked extension dialog with extension_ui_response.
// The dialog to answer is picked by RequestID when the client echoes it,
// else the single pending dialog; ambiguous state is an error rather than a
// guess at which question the user saw.
func (d *discoverer) respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	if proc := d.get(sess.AgentSessionID); proc != nil {
		return d.respondOwned(proc, p)
	}
	if fs := d.lookupForeign(sess.AgentSessionID); fs != nil {
		return d.respondForeign(fs, p)
	}
	return fmt.Errorf("pi: unknown session %s", sess.AgentSessionID)
}

func (d *discoverer) respondOwned(proc *sessionProc, p api.RespondParams) error {
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
		return fmt.Errorf("pi: no pending dialog for session %s", proc.id)
	}
	body, err := buildUIResponse(pend, p)
	if err != nil {
		return err
	}
	body["type"] = "extension_ui_response"
	body["id"] = pend.uiID
	if err := proc.conn.send(body); err != nil {
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

// respondForeign answers a beacon approval request with approval_decision.
func (d *discoverer) respondForeign(fs *foreignSession, p api.RespondParams) error {
	fs.mu.Lock()
	var pend *pendingUI
	if p.RequestID != "" {
		pend = fs.pending[p.RequestID]
	} else if len(fs.pending) == 1 {
		for _, v := range fs.pending {
			pend = v
		}
	}
	fs.mu.Unlock()
	if pend == nil {
		return fmt.Errorf("pi: no pending dialog for session %s", fs.id)
	}
	body, err := buildUIResponse(pend, p)
	if err != nil {
		return err
	}
	decision := "allow"
	if c, ok := body["cancelled"].(bool); ok && c {
		decision = "deny"
	} else if c, ok := body["confirmed"].(bool); ok && !c {
		decision = "deny"
	}
	if err := d.beaconSend(fs, map[string]any{
		"type": "approval_decision", "id": pend.uiID, "decision": decision,
	}); err != nil {
		return err
	}
	fs.mu.Lock()
	delete(fs.pending, pend.uiID)
	stillPending := len(fs.pending) > 0
	fs.mu.Unlock()
	if !stillPending {
		d.foreignCard(fs, session.StatusWorking, nil, false)
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
