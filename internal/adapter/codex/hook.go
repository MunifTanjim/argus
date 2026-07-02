package codex

import (
	"encoding/json"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// HookMethod is the JSON-RPC method the `argus hook --tool codex` command calls
// to deliver a Codex hook event to the node. It is distinct from other adapters'
// methods so the node can register one handler per tool without collision.
const HookMethod = "codex.hook.event"

// HookEvent is the tool-agnostic hook envelope, aliased for the historical
// per-adapter spelling.
type HookEvent = adapter.HookEvent

// hookPayload is the subset of Codex's hook stdin JSON that argus uses. Field
// names follow codex-rs/hooks/schema (session_id, transcript_path, cwd,
// hook_event_name, tool_name, tool_input); transcript_path may be null.
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

// PermissionPayload returns the tool being acted on, when present. Codex
// permission gating is not wired in this cut (the PermissionRequest hook is not
// installed); this exists to satisfy the adapter contract and to log the tool.
func PermissionPayload(ev HookEvent) (toolName string, toolInput json.RawMessage) {
	p := parsePayload(ev)
	return p.ToolName, p.ToolInput
}

// statusFor maps a Codex hook event to a session status. An unrecognized event
// leaves status unchanged. Codex has no Notification/SessionEnd events: turn end
// is "Stop" (→ awaiting the user), and session death is handled by discovery
// pruning when the process vanishes.
func statusFor(event string) (session.Status, bool) {
	switch event {
	case "SessionStart":
		return session.StatusIdle, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return session.StatusWorking, true
	case "Stop":
		return session.StatusAwaitingInput, true
	}
	return "", false
}

// serverFromSocket maps a $TMUX socket basename to its logical tmux server.
func serverFromSocket(socketBasename string) session.TmuxServer {
	if filepath.Base(socketBasename) == "argus" {
		return session.TmuxServerArgus
	}
	return session.TmuxServerDefault
}

// ProcessHook parses a Codex hook event and applies it to the registry,
// returning the resulting session and whether it still exists.
func ProcessHook(reg *registry.Registry, ev HookEvent) (session.Session, bool) {
	p := parsePayload(ev)
	event := EventName(ev)
	status, _ := statusFor(event) // empty status leaves it unchanged

	// Stop ends the turn: show the idle compose prompt and supersede any stale
	// pending interaction.
	var ix *session.Interaction
	replace := false
	if event == "Stop" {
		ix = &session.Interaction{Kind: session.InteractionIdle}
		replace = true
	}

	paneID := ev.TmuxPane
	server := serverFromSocket(ev.TmuxSocket)
	frontend := session.FrontendExternal
	if paneID != "" {
		frontend = session.FrontendTmux
	}

	return reg.ApplyHook(registry.HookUpdate{
		Tool:               Tool,
		Server:             server,
		PaneID:             paneID,
		ClaudeSessionID:    p.SessionID,
		Cwd:                p.Cwd,
		Repo:               repoName(p.Cwd),
		TranscriptPath:     p.TranscriptPath,
		Frontend:           frontend,
		Status:             status,
		Interaction:        ix,
		ReplaceInteraction: replace,
	})
}
