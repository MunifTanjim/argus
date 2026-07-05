package antigravity

import (
	"encoding/json"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type HookEvent = adapter.HookEvent

// hookPayload is the subset of agy's hook stdin JSON that argus reads.
type hookPayload struct {
	ConversationID string   `json:"conversationId"`
	TranscriptPath string   `json:"transcriptPath"`
	ModelName      string   `json:"modelName"`
	WorkspacePaths []string `json:"workspacePaths"`
	// InvocationNum 0 marks the first invocation of a conversation (agy's session-start signal).
	InvocationNum int `json:"invocationNum"`
}

func parsePayload(ev HookEvent) hookPayload {
	var p hookPayload
	_ = json.Unmarshal(ev.Payload, &p)
	return p
}

func EventName(ev HookEvent) string { return ev.Event }

// RescanOnHook triggers a discovery rescan on a conversation's first PreInvocation.
func RescanOnHook(ev HookEvent) bool {
	return EventName(ev) == "PreInvocation" && parsePayload(ev).InvocationNum == 0
}

// PermissionPayload is a no-op: argus does not drive agy's permission prompts.
func PermissionPayload(HookEvent) (toolName string, toolInput json.RawMessage) { return "", nil }

// ShouldBlock is always false; argus only observes agy lifecycle hooks.
func ShouldBlock(HookEvent) bool { return false }

// FormatDecision is never called: argus answers no agy permission requests.
func FormatDecision(string, json.RawMessage, api.RespondParams) string { return "" }

// HookOutput returns stdout for a non-blocking agy hook. Stop needs {"decision":""}
// (empty lets stop proceed); other events accept {}.
func HookOutput(event string) string {
	if event == "Stop" {
		return `{"decision":""}`
	}
	return `{}`
}

func statusFor(event string) (session.Status, bool) {
	switch event {
	case "PreInvocation":
		return session.StatusWorking, true
	case "Stop":
		return session.StatusAwaitingInput, true
	}
	return "", false
}

func serverFromSocket(socketBasename string) session.TmuxServer {
	if filepath.Base(socketBasename) == "argus" {
		return session.TmuxServerArgus
	}
	return session.TmuxServerDefault
}

// ProcessHook applies an Antigravity hook event to the registry, correlating by
// tmux pane or conversation id.
func ProcessHook(reg *registry.Registry, ev HookEvent) (session.Session, bool) {
	p := parsePayload(ev)
	event := EventName(ev)
	status, _ := statusFor(event)

	var ix *session.Interaction
	replace := false
	if event == "Stop" {
		ix = &session.Interaction{Kind: session.InteractionIdle}
		replace = true
	}

	convID := p.ConversationID
	if convID == "" {
		convID = ev.Env["ANTIGRAVITY_CONVERSATION_ID"]
	}
	cwd := ""
	if len(p.WorkspacePaths) > 0 {
		cwd = p.WorkspacePaths[0]
	}

	// Must match discovery's path; a mismatch causes summary wipes.
	transcriptPath := transcriptPathFor(convID)
	if transcriptPath == "" {
		transcriptPath = p.TranscriptPath
	}

	var summary *session.Summary
	if refreshesSummary(event) {
		summary = buildSummary(convID, transcriptPath, p.ModelName)
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
		AgentSessionID:     convID,
		Cwd:                cwd,
		Repo:               repoName(cwd),
		TranscriptPath:     transcriptPath,
		Frontend:           frontend,
		Status:             status,
		Summary:            summary,
		Interaction:        ix,
		ReplaceInteraction: replace,
	})
}
