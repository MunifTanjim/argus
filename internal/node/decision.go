package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
)

// decisionTimeout bounds how long a PermissionRequest hook blocks waiting for the
// user, kept just under the hook's own 600s timeout so we return a fall-back
// (no decision) before Claude kills the hook. A var so tests can shrink it.
var decisionTimeout = 590 * time.Second

// pendingDecision is a parked PermissionRequest awaiting the user's answer. The
// blocked hook handler waits on ch; MethodSessionRespond sends the decision JSON.
type pendingDecision struct {
	ch        chan string
	toolName  string
	toolInput json.RawMessage
}

// hookOut is the PreToolUse/PermissionRequest decision envelope Claude reads.
type hookOut struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior           string           `json:"behavior"`
			Message            string           `json:"message,omitempty"`
			UpdatedInput       map[string]any   `json:"updatedInput,omitempty"`
			UpdatedPermissions []map[string]any `json:"updatedPermissions,omitempty"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

// formatAnswer renders an answer value for the clarify message. Lists (multi-
// select; []string from the TUI, []any from a client's JSON) join with ", ".
func formatAnswer(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, ", ")
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, fmt.Sprint(e))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(v)
	}
}

// buildClarifyMessage renders the "Chat about this" feedback: the templated
// preamble followed by each question and its drafted answer (or a no-answer
// marker). Mirrors Claude Code's onReject(feedback) text.
func buildClarifyMessage(toolInput json.RawMessage, answers map[string]any) string {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(toolInput, &in)

	var b strings.Builder
	b.WriteString("The user wants to clarify these questions.\n")
	b.WriteString("This means they may have additional information, context or questions for you.\n")
	b.WriteString("Take their response into account and then reformulate the questions if appropriate.\n")
	b.WriteString("Start by asking them what they would like to clarify.\n\n")
	b.WriteString("Questions asked:\n")
	for _, q := range in.Questions {
		b.WriteString("- \"" + q.Question + "\"\n")
		if a, ok := answers[q.Question]; ok {
			if s := formatAnswer(a); s != "" {
				b.WriteString("  Answer: " + s + "\n")
				continue
			}
		}
		b.WriteString("  (No answer provided)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildDecision turns the user's structured answer into the PermissionRequest
// hookSpecificOutput JSON. For AskUserQuestion (allow) it injects the answers,
// echoing back the original questions array the tool requires.
func buildDecision(pd *pendingDecision, p api.RespondParams) string {
	var out hookOut
	out.HookSpecificOutput.HookEventName = "PermissionRequest"
	// A server-built option the client echoed back maps to behavior + set-mode.
	switch p.OptionValue {
	case "":
		// No server option: use the explicit Behavior/SetMode fields.
	case "deny":
		p.Behavior = "deny"
	case "allow":
		p.Behavior = "allow"
	default:
		p.Behavior = "allow"
		p.SetMode = p.OptionValue
	}
	behavior := p.Behavior
	if behavior == "" {
		behavior = "allow"
	}
	if p.QuestionAction == "chat" {
		behavior = "deny"
	}
	out.HookSpecificOutput.Decision.Behavior = behavior
	switch {
	case p.QuestionAction == "chat":
		out.HookSpecificOutput.Decision.Message = buildClarifyMessage(pd.toolInput, p.Answers)
	case behavior == "deny":
		out.HookSpecificOutput.Decision.Message = p.Reason
	case pd.toolName == "AskUserQuestion" && len(p.Answers) > 0:
		ui := map[string]any{"answers": p.Answers}
		var qin struct {
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal(pd.toolInput, &qin) == nil && len(qin.Questions) > 0 {
			ui["questions"] = qin.Questions
		}
		out.HookSpecificOutput.Decision.UpdatedInput = ui
	}
	// On approval, an optional permission-mode switch (e.g. ExitPlanMode →
	// acceptEdits) so the session leaves plan mode in the chosen mode.
	if behavior == "allow" && p.SetMode != "" {
		out.HookSpecificOutput.Decision.UpdatedPermissions = []map[string]any{
			{"type": "setMode", "destination": "session", "mode": p.SetMode},
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

// park registers a pending decision for a session and returns it plus a cleanup.
func (d *Node) park(sid, toolName string, toolInput json.RawMessage) (*pendingDecision, func()) {
	pd := &pendingDecision{ch: make(chan string, 1), toolName: toolName, toolInput: toolInput}
	d.pendingMu.Lock()
	d.pending[sid] = pd
	d.pendingMu.Unlock()
	return pd, func() {
		d.pendingMu.Lock()
		if d.pending[sid] == pd {
			delete(d.pending, sid)
		}
		d.pendingMu.Unlock()
	}
}

// takePending removes and returns a session's parked decision, if any.
func (d *Node) takePending(sid string) *pendingDecision {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	pd := d.pending[sid]
	if pd != nil {
		delete(d.pending, sid)
	}
	return pd
}

// awaitDecision parks a PermissionRequest for sid and blocks until the user
// answers in argus, the hook goes away (its connection closed because the prompt
// was dismissed/answered in Claude), or the timeout fires. The pending interaction
// is cleared on every exit so a stale prompt never lingers; on the non-answered
// exits it returns "" so the hook prints nothing and Claude uses its own prompt.
func (d *Node) awaitDecision(ctx context.Context, sid string, ev claudecode.HookEvent) string {
	toolName, toolInput := claudecode.PermissionPayload(ev)
	pd, cancel := d.park(sid, toolName, toolInput)
	defer cancel()
	select {
	case out := <-pd.ch: // answered in argus
		d.reg.ClearInteraction(sid)
		return out
	case <-ctx.Done(): // hook gone — dismissed/answered in Claude
		d.reg.ClearInteraction(sid)
		return ""
	case <-time.After(decisionTimeout): // Claude fell back to its own prompt
		d.reg.ClearInteraction(sid)
		return ""
	}
}
