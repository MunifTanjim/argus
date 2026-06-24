package claudecode

import (
	"encoding/json"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// HookMethod is the JSON-RPC method the `argus hook` command calls to deliver a
// Claude Code hook event to the node.
const HookMethod = "hook.event"

// HookEvent is the payload `argus hook <event>` sends to the node. Payload is
// the raw JSON Claude Code passed to the hook on stdin; TmuxPane/TmuxSocket come
// from the hook process's environment ($TMUX_PANE / $TMUX) for correlation.
type HookEvent struct {
	Event      string          `json:"event"`       // hook_event_name, e.g. "Stop"
	TmuxPane   string          `json:"tmux_pane"`   // $TMUX_PANE (e.g. "%3")
	TmuxSocket string          `json:"tmux_socket"` // basename of the $TMUX socket
	Payload    json.RawMessage `json:"payload"`     // raw hook stdin JSON
	// AutoMode reports $CLAUDE_CODE_ENABLE_AUTO_MODE=1 in the hook process's
	// environment (the Claude session's env), gating the plan "auto mode" option.
	AutoMode bool `json:"auto_mode"`
}

// hookPayload is the subset of Claude Code's hook stdin JSON that argus uses.
type hookPayload struct {
	SessionID        string          `json:"session_id"`
	TranscriptPath   string          `json:"transcript_path"`
	Cwd              string          `json:"cwd"`
	HookEventName    string          `json:"hook_event_name"`
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	NotificationType string          `json:"notification_type"`
	Message          string          `json:"message"`
	// Reason is SessionEnd's end cause ("clear", "logout", "prompt_input_exit",
	// "other", …). "clear" is special: /clear ends the session in place and
	// immediately starts a fresh one, so it is not a real death.
	Reason string `json:"reason"`
	// Source is SessionStart's start cause ("startup", "resume", "clear",
	// "compact"). "clear" is the second half of /clear's end-then-start pair.
	Source string `json:"source"`
}

// statusFor maps a hook event to a session status. ok is false when the event
// should not change the status.
func statusFor(event string) (session.Status, bool) {
	switch event {
	case "SessionStart":
		return session.StatusIdle, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PreCompact":
		return session.StatusWorking, true
	case "Notification":
		// Claude Code emits Notification for permission prompts and idle
		// waiting — in all cases it wants the user's attention.
		return session.StatusAwaitingInput, true
	case "Stop":
		// The turn ended; the session is now waiting for the user's next message.
		// Surface it immediately rather than waiting for the delayed idle
		// Notification (see replacesInteraction).
		return session.StatusAwaitingInput, true
	case "SessionEnd":
		return session.StatusDead, true
	default:
		return "", false
	}
}

// interactionFor builds the pending user interaction implied by a hook event, or
// nil when the event implies none. AskUserQuestion/ExitPlanMode are detected from
// PreToolUse; permission comes from the authoritative PermissionRequest payload;
// a Notification only ever implies an idle "awaiting input" interaction.
func interactionFor(event string, p hookPayload, autoMode bool) *session.Interaction {
	switch event {
	case "PreToolUse":
		switch p.ToolName {
		case "AskUserQuestion":
			return parseQuestion(p.ToolInput)
		case "ExitPlanMode":
			return parsePlan(p.ToolInput, autoMode)
		}
		return nil
	case "PermissionRequest":
		// The decision point. Its payload carries tool_name + tool_input, so we
		// build the interaction directly (no transcript read needed).
		return permissionInteraction(p, autoMode)
	case "Notification", "Stop":
		// Both resolve to an idle "waiting for input" prompt. Notification is a
		// generic attention signal; Stop means the turn ended.
		return classifyNotification(p)
	}
	return nil
}

// replacesInteraction reports whether an event's interaction should overwrite any
// prior one outright rather than defer to mergeInteraction. Stop ends the turn, so
// its idle prompt supersedes a stale permission/question/plan the user may have
// already resolved in their own terminal.
func replacesInteraction(event string) bool {
	return event == "Stop"
}

// permissionInteraction builds the interaction for a PermissionRequest from its
// tool_name + tool_input.
func permissionInteraction(p hookPayload, autoMode bool) *session.Interaction {
	switch p.ToolName {
	case "AskUserQuestion":
		return parseQuestion(p.ToolInput)
	case "ExitPlanMode":
		return parsePlan(p.ToolInput, autoMode)
	default:
		return &session.Interaction{
			Kind:      session.InteractionPermission,
			ToolName:  p.ToolName,
			ToolInput: string(p.ToolInput),
			Options: []session.DecisionOption{
				{Label: "Allow", Value: "allow"},
				{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell Claude why"},
			},
		}
	}
}

// EventName returns the resolved hook event name (the explicit arg, else the
// payload's hook_event_name).
func EventName(ev HookEvent) string {
	if ev.Event != "" {
		return ev.Event
	}
	var p hookPayload
	_ = json.Unmarshal(ev.Payload, &p)
	return p.HookEventName
}

// PermissionPayload returns the tool name and raw tool input from a hook event
// (used to build the decision's updatedInput).
func PermissionPayload(ev HookEvent) (toolName string, toolInput json.RawMessage) {
	var p hookPayload
	_ = json.Unmarshal(ev.Payload, &p)
	return p.ToolName, p.ToolInput
}

// parseQuestion extracts every question (with its header + option labels) from
// AskUserQuestion tool input into the interaction's Questions list.
func parseQuestion(raw json.RawMessage) *session.Interaction {
	var in struct {
		Questions []struct {
			Header      string `json:"header"`
			Question    string `json:"question"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
				Preview     string `json:"preview"`
			} `json:"options"`
		} `json:"questions"`
	}
	ix := &session.Interaction{Kind: session.InteractionQuestion}
	if json.Unmarshal(raw, &in) == nil {
		for _, q := range in.Questions {
			qs := session.QuestionSpec{
				Header:      q.Header,
				Question:    q.Question,
				MultiSelect: q.MultiSelect,
			}
			for _, o := range q.Options {
				qs.Options = append(qs.Options, o.Label)
				qs.OptionDescriptions = append(qs.OptionDescriptions, o.Description)
				qs.OptionPreviews = append(qs.OptionPreviews, o.Preview)
			}
			ix.Questions = append(ix.Questions, qs)
		}
	}
	return ix
}

// parsePlan extracts the plan text from ExitPlanMode tool input and builds the
// approve/reject options. Approving carries a permission-mode switch; "auto"
// mode is offered only when the Claude session has it enabled (autoMode).
func parsePlan(raw json.RawMessage, autoMode bool) *session.Interaction {
	var in struct {
		Plan string `json:"plan"`
	}
	_ = json.Unmarshal(raw, &in)
	// Elevated option first: auto mode supersedes auto-accept-edits when
	// available, so only one of them is offered. Manual approve follows, reject last.
	var opts []session.DecisionOption
	if autoMode {
		opts = append(opts, session.DecisionOption{Label: "Yes, and use auto mode", Value: "auto"})
	} else {
		opts = append(opts, session.DecisionOption{Label: "Yes, auto-accept edits", Value: "acceptEdits"})
	}
	opts = append(opts,
		session.DecisionOption{Label: "Yes, manually approve edits", Value: "default"},
		session.DecisionOption{
			Label:       "No, keep planning",
			Value:       "deny",
			Reject:      true,
			Placeholder: "Tell Claude what to change",
		},
	)
	return &session.Interaction{Kind: session.InteractionPlan, Plan: in.Plan, Options: opts}
}

// classifyNotification turns a Notification payload into an idle "awaiting input"
// interaction carrying only the message. A Notification never derives tool details:
// it fires concurrently with PermissionRequest/PreToolUse but cannot reliably name
// the right tool (e.g. a subagent's request carries the parent transcript, where
// the still-pending Task tool would be misread). The authoritative tool-specific
// interactions come from those hooks; an idle Notification must not clobber them
// (see registry.mergeInteraction).
func classifyNotification(p hookPayload) *session.Interaction {
	return &session.Interaction{Kind: session.InteractionIdle, Message: p.Message}
}

// serverFromSocket derives which logical tmux server a pane belongs to from the
// $TMUX socket basename. argus's private socket is "argus"; everything else
// (the user's default socket, typically "default") maps to the default server.
func serverFromSocket(socketBasename string) session.TmuxServer {
	if filepath.Base(socketBasename) == "argus" {
		return session.TmuxServerArgus
	}
	return session.TmuxServerDefault
}

// ProcessHook parses a hook event and applies it to the registry, returning the
// resulting session and whether it still exists.
func ProcessHook(reg *registry.Registry, ev HookEvent) (session.Session, bool) {
	var p hookPayload
	_ = json.Unmarshal(ev.Payload, &p)

	event := ev.Event
	if event == "" {
		event = p.HookEventName
	}
	status, _ := statusFor(event) // empty status leaves it unchanged

	// A pending interaction means the session is awaiting the user, overriding
	// the base status. When there's no interaction the field is cleared (see
	// ApplyHook).
	ix := interactionFor(event, p, ev.AutoMode)
	if ix != nil {
		status = session.StatusAwaitingInput
	}
	replace := replacesInteraction(event)

	// /clear resets the session in place: SessionEnd(reason=clear) immediately
	// followed by SessionStart(source=clear). Map each to its true meaning rather
	// than removing the session. The end drops the old conversation: idle, with the
	// stale prompt cleared (ix stays nil → ApplyHook clears it). The start lands the
	// fresh session on awaiting-input with an idle interaction so the respond/compose
	// prompt shows (the list flags only awaiting-input sessions; the dock needs an
	// interaction); replace forces that fresh prompt over anything still pending.
	switch {
	case event == "SessionEnd" && p.Reason == "clear":
		status = session.StatusIdle
	case event == "SessionStart" && p.Source == "clear":
		status = session.StatusAwaitingInput
		ix = &session.Interaction{Kind: session.InteractionIdle}
		replace = true
	}

	// Refresh the cached transcript digest only on summary-relevant events.
	var sum *session.Summary
	if refreshesSummary(event) && p.TranscriptPath != "" {
		sum = summarize(p.TranscriptPath)
	}

	return reg.ApplyHook(registry.HookUpdate{
		Tool:               Tool,
		Server:             serverFromSocket(ev.TmuxSocket),
		PaneID:             ev.TmuxPane,
		ClaudeSessionID:    p.SessionID,
		Cwd:                p.Cwd,
		Repo:               repoName(p.Cwd),
		TranscriptPath:     p.TranscriptPath,
		Status:             status,
		Summary:            sum,
		Interaction:        ix,
		ReplaceInteraction: replace,
	})
}
