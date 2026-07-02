package codex

import (
	"encoding/json"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type HookEvent = adapter.HookEvent

// hookPayload is the subset of Codex's hook stdin JSON that argus uses.
type hookPayload struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
}

func parsePayload(ev HookEvent) hookPayload {
	var p hookPayload
	_ = json.Unmarshal(ev.Payload, &p)
	return p
}

// EventName resolves the hook event name, preferring the explicit envelope field
// and falling back to the payload's hook_event_name.
func EventName(ev HookEvent) string {
	if ev.Event != "" {
		return ev.Event
	}
	return parsePayload(ev).HookEventName
}

func PermissionPayload(ev HookEvent) (toolName string, toolInput json.RawMessage) {
	p := parsePayload(ev)
	return p.ToolName, p.ToolInput
}

// Codex has no SessionEnd event; session death is handled by discovery pruning.
func statusFor(event string) (session.Status, bool) {
	switch event {
	case "SessionStart":
		return session.StatusIdle, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return session.StatusWorking, true
	case "PermissionRequest", "Stop":
		return session.StatusAwaitingInput, true
	}
	return "", false
}

// These events replace any pending interaction with the idle prompt.
func idleComposeEvent(event string) bool {
	return event == "SessionStart" || event == "Stop"
}

func toolInputDescription(raw json.RawMessage) string {
	var in struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &in)
	return in.Description
}

func permissionInteraction(p hookPayload) *session.Interaction {
	return &session.Interaction{
		Kind:      session.InteractionPermission,
		ToolName:  p.ToolName,
		ToolInput: string(p.ToolInput),
		Message:   toolInputDescription(p.ToolInput),
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell Codex why"},
		},
	}
}

func serverFromSocket(socketBasename string) session.TmuxServer {
	if filepath.Base(socketBasename) == "argus" {
		return session.TmuxServerArgus
	}
	return session.TmuxServerDefault
}

func ProcessHook(reg *registry.Registry, ev HookEvent) (session.Session, bool) {
	p := parsePayload(ev)
	event := EventName(ev)
	status, _ := statusFor(event) // empty status leaves it unchanged

	var ix *session.Interaction
	replace := false
	switch {
	case idleComposeEvent(event):
		ix = &session.Interaction{Kind: session.InteractionIdle}
		replace = true
	case event == "PermissionRequest":
		ix = permissionInteraction(p)
	}

	paneID := ev.TmuxPane
	frontend := session.FrontendExternal
	if paneID != "" {
		frontend = session.FrontendTmux
	}

	return reg.ApplyHook(registry.HookUpdate{
		Agent:              Agent,
		Server:             serverFromSocket(ev.TmuxSocket),
		PaneID:             paneID,
		AgentSessionID:     p.SessionID,
		Cwd:                p.Cwd,
		Repo:               repoName(p.Cwd),
		TranscriptPath:     p.TranscriptPath,
		Frontend:           frontend,
		Status:             status,
		Interaction:        ix,
		ReplaceInteraction: replace,
	})
}
