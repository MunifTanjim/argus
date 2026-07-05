// Package hookdecision renders a user's permission answer into hookSpecificOutput
// decision JSON.
package hookdecision

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
)

// hookOut is the decision envelope Claude Code reads from hook stdout.
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

// buildClarifyMessage renders feedback for the "Chat about this" action.
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

func FormatDecision(toolName string, toolInput json.RawMessage, p api.RespondParams) string {
	var out hookOut
	out.HookSpecificOutput.HookEventName = "PermissionRequest"
	switch p.OptionValue {
	case "":
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
		out.HookSpecificOutput.Decision.Message = buildClarifyMessage(toolInput, p.Answers)
	case behavior == "deny":
		out.HookSpecificOutput.Decision.Message = p.Reason
	case toolName == "AskUserQuestion" && len(p.Answers) > 0:
		ui := map[string]any{"answers": p.Answers}
		var qin struct {
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal(toolInput, &qin) == nil && len(qin.Questions) > 0 {
			ui["questions"] = qin.Questions
		}
		out.HookSpecificOutput.Decision.UpdatedInput = ui
	}
	// Optional permission-mode switch on approval (e.g. ExitPlanMode → acceptEdits).
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
