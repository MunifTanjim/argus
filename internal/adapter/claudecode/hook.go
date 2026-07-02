package claudecode

import (
	"encoding/json"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// HookMethod is the JSON-RPC method the `argus hook` command calls to deliver a
// Claude Code hook event to the node.
const HookMethod = "hook.event"

// HookEvent is the tool-agnostic hook envelope; it lives in the adapter package
// so the node can decode it without importing claudecode. Aliased here to keep
// the historical claudecode.HookEvent spelling working.
type HookEvent = adapter.HookEvent

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
	// Reason is SessionEnd's end cause. "clear" is special: /clear ends in place
	// and immediately starts a fresh session, so it is not a real death.
	Reason string `json:"reason"`
	// Source is SessionStart's start cause. "clear" is the second half of /clear's
	// end-then-start pair.
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
		// Permission prompts and idle waiting both want the user's attention.
		return session.StatusAwaitingInput, true
	case "Stop":
		// Turn ended; surface awaiting-input immediately rather than waiting for
		// the delayed idle Notification (see replacesInteraction).
		return session.StatusAwaitingInput, true
	case "SessionEnd":
		return session.StatusDead, true
	default:
		return "", false
	}
}

// interactionFor builds the pending user interaction implied by a hook event, or
// nil when none.
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
		// Payload carries tool_name + tool_input, so build directly (no transcript
		// read needed).
		return permissionInteraction(p, autoMode)
	case "Notification", "Stop":
		// Both resolve to an idle "waiting for input" prompt.
		return classifyNotification(p)
	}
	return nil
}

// replacesInteraction reports whether an event's interaction overwrites any prior
// one outright rather than deferring to mergeInteraction. Stop ends the turn, so
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
	// Elevated option first: auto mode supersedes auto-accept-edits, so only one
	// is offered. Manual approve follows, reject last.
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
// interaction carrying only the message. It never derives tool details: a
// Notification fires concurrently with PermissionRequest/PreToolUse but can't
// reliably name the right tool, so it must not clobber the authoritative
// tool-specific interactions from those hooks (see registry.mergeInteraction).
func classifyNotification(p hookPayload) *session.Interaction {
	return &session.Interaction{Kind: session.InteractionIdle, Message: p.Message}
}

// frontendFor classifies a session's UI host from its launch entrypoint and
// whether it has a real tmux pane. "claude-vscode" is the paneless VSCode
// extension; "cli" (and unknown) is the terminal, which may run in a tmux pane.
func frontendFor(entrypoint string, hasPane bool) session.Frontend {
	if entrypoint == "claude-vscode" {
		return session.FrontendVSCode
	}
	if hasPane {
		return session.FrontendTmux
	}
	return session.FrontendExternal
}

// serverFromSocket maps a $TMUX socket basename to its logical tmux server.
// argus's private socket is "argus"; everything else maps to the default server.
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

	// A pending interaction forces awaiting-input; no interaction clears the field
	// (see ApplyHook).
	ix := interactionFor(event, p, ev.AutoMode)
	if ix != nil {
		status = session.StatusAwaitingInput
	}
	replace := replacesInteraction(event)

	// /clear resets in place: SessionEnd(reason=clear) then SessionStart(source=
	// clear). Map each to its true meaning instead of removing the session. End →
	// idle, stale prompt cleared (ix nil). Start → awaiting-input with an idle
	// interaction so the compose prompt shows (list flags only awaiting-input;
	// dock needs an interaction); replace forces it over anything still pending.
	switch {
	case event == "SessionEnd" && p.Reason == "clear":
		status = session.StatusIdle
	case event == "SessionStart" && p.Source == "clear":
		status = session.StatusAwaitingInput
		ix = &session.Interaction{Kind: session.InteractionIdle}
		replace = true
	}

	// Refresh the transcript digest only on summary-relevant events.
	var sum *session.Summary
	if refreshesSummary(event) && p.TranscriptPath != "" {
		sum = summarize(p.TranscriptPath)
	}

	// $TMUX/$TMUX_PANE are inherited by every child of the tmux-starting process,
	// including a `claude` in a VSCode terminal opened from a tmux pane. Trust the
	// inherited pane only for a cli session; a non-cli (vscode) session must stay
	// paneless or discovery prunes it from a pane it never occupied. Unknown
	// entrypoint (file missing/racy at first SessionStart) trusts the pane to avoid
	// racing the scan and duplicating a real cli session; a later hook resolves it.
	entry, _ := findProcSessionByID(claudeSessionsDir(), p.SessionID)
	terminal := entry.Entrypoint == "" || entry.Entrypoint == "cli"
	paneID := ev.TmuxPane
	server := serverFromSocket(ev.TmuxSocket)
	if !terminal {
		paneID, server = "", ""
	}

	return reg.ApplyHook(registry.HookUpdate{
		Tool:               Tool,
		Server:             server,
		PaneID:             paneID,
		ClaudeSessionID:    p.SessionID,
		Cwd:                p.Cwd,
		Repo:               repoName(p.Cwd),
		TranscriptPath:     p.TranscriptPath,
		Frontend:           frontendFor(entry.Entrypoint, paneID != ""),
		Status:             status,
		Summary:            sum,
		Interaction:        ix,
		ReplaceInteraction: replace,
	})
}
