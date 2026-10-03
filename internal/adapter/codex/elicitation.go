package codex

import (
	"encoding/json"
	"slices"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// Keys and values of Codex's MCP approval metadata (codex_protocol::mcp_approval_meta).
const (
	approvalKindKey         = "codex_approval_kind"
	approvalKindMCPToolCall = "mcp_tool_call"
	persistKey              = "persist"
	persistSession          = "session"
	persistAlways           = "always"
	toolParamsKey           = "tool_params"
)

// cannotAnswer ends the message of a request argus can only cancel.
const cannotAnswer = "This request needs Codex to answer; open the session in Codex, or cancel it."

var cancelOnly = []session.DecisionOption{{Label: "Cancel", Value: "cancel", Reject: true}}

// elicitation is what replying to an mcpServer/elicitation/request needs.
type elicitation struct {
	answerable   bool // a message-only form: an approval argus can show
	toolApproval bool // an MCP tool-call approval, refused only by Cancel
}

// parseElicitation maps an elicitation the way Codex's TUI does: a message-only
// form is an approval offering Allow, Allow for this session and Always allow as
// its metadata permits, then Cancel for a tool call or Deny and Cancel otherwise.
// Forms with fields, URL and verification requests, and other Codex approval
// kinds, offer only Cancel.
func parseElicitation(params json.RawMessage) (elicitation, *session.Interaction) {
	var p struct {
		Mode            string                     `json:"mode"`
		Meta            map[string]json.RawMessage `json:"_meta"`
		Message         string                     `json:"message"`
		Title           string                     `json:"title"`
		RequestedSchema json.RawMessage            `json:"requestedSchema"`
	}
	_ = json.Unmarshal(params, &p)
	var kind string
	_ = json.Unmarshal(p.Meta[approvalKindKey], &kind)
	el := elicitation{
		answerable:   p.Mode == "form" && messageOnlySchema(p.RequestedSchema) && (kind == "" || kind == approvalKindMCPToolCall),
		toolApproval: kind == approvalKindMCPToolCall,
	}
	msg := p.Message
	if msg == "" {
		msg = p.Title
	}
	ix := &session.Interaction{Kind: session.InteractionPermission, Message: msg}
	if !el.answerable {
		ix.Message = joinLines(msg, cannotAnswer)
		ix.Options = cancelOnly
		return el, ix
	}
	if el.toolApproval {
		if args := p.Meta[toolParamsKey]; len(args) > 0 && string(args) != "null" {
			ix.ToolInput = string(args)
		}
	}
	opts := []session.DecisionOption{{Label: "Allow", Value: "allow"}}
	if persists(p.Meta[persistKey], persistSession) {
		opts = append(opts, session.DecisionOption{Label: "Allow for this session", Value: "acceptForSession"})
	}
	if persists(p.Meta[persistKey], persistAlways) {
		opts = append(opts, session.DecisionOption{Label: "Always allow", Value: "acceptAlways"})
	}
	if el.toolApproval {
		opts = append(opts, cancelOnly...)
	} else {
		opts = append(opts,
			session.DecisionOption{Label: "Deny", Value: "deny", Reject: true},
			session.DecisionOption{Label: "Cancel", Value: "cancel"})
	}
	ix.Options = opts
	return el, ix
}

// messageOnlySchema reports a form with nothing to fill in: no schema, or an
// object schema without properties.
func messageOnlySchema(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var s struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	return json.Unmarshal(raw, &s) == nil && s.Type == "object" && len(s.Properties) == 0
}

// persists reports whether the metadata's persist value, a mode or a list of
// modes, offers mode.
func persists(raw json.RawMessage, mode string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == mode
	}
	var many []string
	return json.Unmarshal(raw, &many) == nil && slices.Contains(many, mode)
}

func elicitationReply(el elicitation, p api.RespondParams) map[string]any {
	action, meta := "cancel", any(nil)
	switch {
	case !el.answerable || p.OptionValue == "cancel":
	case denied(p):
		if !el.toolApproval {
			action = "decline"
		}
	case p.OptionValue == "acceptForSession":
		action, meta = "accept", map[string]any{persistKey: persistSession}
	case p.OptionValue == "acceptAlways":
		action, meta = "accept", map[string]any{persistKey: persistAlways}
	case allowed(p):
		action = "accept"
	}
	return map[string]any{"action": action, "content": nil, "_meta": meta}
}

// dynamicToolInteraction shows a call to a tool a client registered on the
// thread. argus registers none, so it can only cancel the call.
func dynamicToolInteraction(params json.RawMessage) *session.Interaction {
	var p struct {
		Namespace *string `json:"namespace"`
		Tool      string  `json:"tool"`
	}
	_ = json.Unmarshal(params, &p)
	name := p.Tool
	if ns := strVal(p.Namespace); ns != "" {
		name = ns + "." + name
	}
	return &session.Interaction{
		Kind:    session.InteractionPermission,
		Message: joinLines("Codex is waiting on the client tool "+name+".", cannotAnswer),
		Options: cancelOnly,
	}
}

var dynamicToolCancelled = map[string]any{
	"contentItems": []map[string]string{{"type": "inputText", "text": "The user cancelled this tool call."}},
	"success":      false,
}
