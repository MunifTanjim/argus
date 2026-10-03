package codex

import (
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// cxThread is the subset of the app-server Thread that argus uses.
type cxThread struct {
	ID             string       `json:"id"`
	Cwd            string       `json:"cwd"`
	Path           *string      `json:"path"` // rollout JSONL; null for ephemeral threads
	Name           *string      `json:"name"`
	Preview        string       `json:"preview"`
	Model          string       `json:"model"`
	Ephemeral      bool         `json:"ephemeral"`
	ParentThreadID *string      `json:"parentThreadId"`
	Status         threadStatus `json:"status"`
}

// trackable excludes threads with no rollout, such as the ephemeral thread Codex
// spawns to generate a session title.
func (t cxThread) trackable() bool { return !t.Ephemeral && t.Path != nil && *t.Path != "" }

func (t cxThread) title() string {
	if t.Name != nil && *t.Name != "" {
		return *t.Name
	}
	return firstLine(t.Preview)
}

func (t cxThread) parent() string { return strVal(t.ParentThreadID) }

// threadStatus is notLoaded, idle, systemError, or active with optional
// waitingOnApproval / waitingOnUserInput flags.
type threadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

func (s threadStatus) waiting() bool { return s.Type == "active" && len(s.ActiveFlags) > 0 }

type userInputQuestion struct {
	ID       string `json:"id"`
	Header   string `json:"header"`
	Question string `json:"question"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// pendingRequest is an open server request argus can answer.
type pendingRequest struct {
	id          json.RawMessage
	method      string
	threadID    string // the asking thread; a subagent's requests show on its root
	permissions json.RawMessage
	questions   []userInputQuestion
	elicit      elicitation
	turnID      string // file-change approvals name their patch by turn and item
	itemID      string
	interaction *session.Interaction
	answered    bool     // replied to; kept until the daemon reports it resolved
	conn        *rpcConn // connection the request arrived on; ids are per connection
}

func requestThreadID(params json.RawMessage) string {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &p)
	return p.ThreadID
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// decisionOptions builds Allow / [Allow for session] / Deny. Codex decisions carry
// no message, so Deny has no placeholder.
func decisionOptions(forSession bool) []session.DecisionOption {
	opts := []session.DecisionOption{{Label: "Allow", Value: "allow"}}
	if forSession {
		opts = append(opts, session.DecisionOption{Label: "Allow for session", Value: "acceptForSession"})
	}
	return append(opts, session.DecisionOption{Label: "Deny", Value: "deny", Reject: true})
}

func offersSession(decisions []json.RawMessage) bool {
	for _, d := range decisions {
		var s string
		if json.Unmarshal(d, &s) == nil && s == "acceptForSession" {
			return true
		}
	}
	return false
}

// parseRequest maps a server request to a pendingRequest. ok is false for request
// kinds argus does not answer; those stay with the TUI.
func parseRequest(m inbound) (*pendingRequest, bool) {
	pr := &pendingRequest{id: m.ID, method: m.Method, threadID: requestThreadID(m.Params)}
	switch m.Method {
	case "item/commandExecution/requestApproval":
		var p struct {
			Reason             *string           `json:"reason"`
			Command            string            `json:"command"`
			Cwd                string            `json:"cwd"`
			AvailableDecisions []json.RawMessage `json:"availableDecisions"`
		}
		_ = json.Unmarshal(m.Params, &p)
		in, _ := json.Marshal(map[string]string{"cmd": p.Command, "workdir": p.Cwd})
		pr.interaction = &session.Interaction{
			Kind:      session.InteractionPermission,
			ToolName:  "exec_command",
			ToolInput: string(in),
			Message:   strVal(p.Reason),
			Options:   decisionOptions(offersSession(p.AvailableDecisions)),
		}
	case "item/fileChange/requestApproval":
		var p struct {
			TurnID    string  `json:"turnId"`
			ItemID    string  `json:"itemId"`
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(m.Params, &p)
		pr.turnID, pr.itemID = p.TurnID, p.ItemID
		msg := strVal(p.Reason)
		if root := strVal(p.GrantRoot); root != "" {
			msg = joinLines(msg, "Asks to allow writes under "+root+" for the rest of the session.")
		}
		pr.interaction = &session.Interaction{
			Kind:     session.InteractionPermission,
			ToolName: "apply_patch",
			Message:  msg,
			Options:  decisionOptions(true),
		}
	case "item/permissions/requestApproval":
		var p struct {
			Reason      *string         `json:"reason"`
			Permissions json.RawMessage `json:"permissions"`
		}
		_ = json.Unmarshal(m.Params, &p)
		pr.permissions = p.Permissions
		pr.interaction = &session.Interaction{
			Kind:      session.InteractionPermission,
			ToolName:  "permissions",
			ToolInput: string(p.Permissions),
			Message:   strVal(p.Reason),
			Options:   decisionOptions(false),
		}
	case "item/tool/requestUserInput":
		var p struct {
			Questions []userInputQuestion `json:"questions"`
		}
		_ = json.Unmarshal(m.Params, &p)
		pr.questions = p.Questions
		ix := &session.Interaction{Kind: session.InteractionQuestion, CancelInterrupts: true, AllowUnanswered: true}
		for _, q := range p.Questions {
			spec := session.QuestionSpec{Header: q.Header, Question: q.Question}
			for _, o := range q.Options {
				spec.Options = append(spec.Options, o.Label)
				spec.OptionDescriptions = append(spec.OptionDescriptions, o.Description)
			}
			ix.Questions = append(ix.Questions, spec)
		}
		pr.interaction = ix
	case "mcpServer/elicitation/request":
		pr.elicit, pr.interaction = parseElicitation(m.Params)
	case "item/tool/call":
		pr.interaction = dynamicToolInteraction(m.Params)
	default:
		return nil, false
	}
	return pr, true
}

func joinLines(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n\n" + b
}

func denied(p api.RespondParams) bool { return p.Behavior == "deny" || p.OptionValue == "deny" }

// allowed reports a plain allow. Anything else refuses, so a respond that names
// no decision never approves.
func allowed(p api.RespondParams) bool {
	return p.OptionValue == "allow" || (p.OptionValue == "" && p.Behavior == "allow")
}

// replyFor builds the JSON-RPC result for a pending request from argus's respond
// params.
func replyFor(pr *pendingRequest, p api.RespondParams) any {
	switch pr.method {
	case "item/permissions/requestApproval":
		if !allowed(p) {
			return map[string]any{"permissions": map[string]any{}, "scope": "turn"}
		}
		return map[string]any{"permissions": pr.permissions, "scope": "turn"}
	case "mcpServer/elicitation/request":
		return elicitationReply(pr.elicit, p)
	case "item/tool/call":
		return dynamicToolCancelled
	case "item/tool/requestUserInput":
		answers := map[string]any{}
		for _, q := range pr.questions {
			vals := []string{}
			switch v := p.Answers[q.Question].(type) {
			case string:
				vals = []string{v}
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok {
						vals = append(vals, s)
					}
				}
			}
			answers[q.ID] = map[string]any{"answers": vals}
		}
		return map[string]any{"answers": answers}
	default: // command and file-change approvals
		switch {
		case p.OptionValue == "acceptForSession":
			return map[string]any{"decision": "acceptForSession"}
		case allowed(p):
			return map[string]any{"decision": "accept"}
		default:
			return map[string]any{"decision": "decline"}
		}
	}
}
