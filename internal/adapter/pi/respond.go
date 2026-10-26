package pi

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func denied(p api.RespondParams) bool {
	return p.Behavior == "deny" || p.OptionValue == "deny"
}

// buildUIResponse renders a parked dialog's answer as an extension_ui_response
// payload; the caller adds the envelope.
func buildUIResponse(pend *pendingUI, p api.RespondParams) (map[string]any, error) {
	resp := map[string]any{}
	denied := denied(p)
	cancelled := denied || p.QuestionAction != ""
	switch pend.method {
	case "select":
		if cancelled {
			resp["cancelled"] = true
			break
		}
		v := answerText(pend, p)
		if !slices.Contains(pend.options, v) {
			return nil, fmt.Errorf("pi: %q is not an option of %q", v, cmp.Or(pend.title, pend.message))
		}
		resp["value"] = v
	case "confirm":
		resp["confirmed"] = !denied
	case "input", "editor":
		if cancelled {
			resp["cancelled"] = true
		} else {
			resp["value"] = answerText(pend, p)
		}
	default:
		return nil, fmt.Errorf("pi: cannot answer dialog method %q", pend.method)
	}
	return resp, nil
}

// respond answers a parked extension dialog.
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
	pend, err := proc.pickPending(p.RequestID)
	if err != nil {
		return err
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
	// The blocked handler resumes into tool execution (or the command
	// completes); refresh the card now so a command-sourced dialog doesn't
	// wedge the session at its prompt. Later events correct the status.
	d.dropOwnedPending(proc, pend.uiID)
	return nil
}

// respondForeign answers a dialog the gate forwarded from pi.
func (d *discoverer) respondForeign(fs *foreignSession, p api.RespondParams) error {
	pend, err := fs.pickPending(p.RequestID)
	if err != nil {
		return err
	}
	body, err := buildUIResponse(pend, p)
	if err != nil {
		return err
	}
	body["type"] = "ui_response"
	body["id"] = pend.uiID
	if err := d.beaconSend(fs, body); err != nil {
		return err
	}
	d.dropForeignPending(fs, pend.uiID)
	return nil
}

// dropForeignPending removes an answered dialog and, once none is left,
// settles the card on what pi is doing.
func (d *discoverer) dropForeignPending(fs *foreignSession, uiID string) {
	if fs.dropPending(uiID) {
		d.foreignSettle(fs)
	}
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
