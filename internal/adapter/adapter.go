// Package adapter defines the boundary between argus's core (registry, node, API,
// TUI) and a specific AI-coding-tool integration such as Claude Code or Codex.
// The core talks only to the Adapter interface and routes work by the session's
// Tool string, so a new tool is added by implementing Adapter — nothing in the
// core names any tool.
package adapter

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// HookEvent is the payload `argus hook <event>` sends to the node. TmuxPane/
// TmuxSocket come from the hook process's environment for pane correlation. The
// envelope is tool-agnostic; Payload carries the tool's raw hook stdin JSON.
type HookEvent struct {
	Event      string          `json:"event"`       // hook_event_name, e.g. "Stop"
	TmuxPane   string          `json:"tmux_pane"`   // $TMUX_PANE (e.g. "%3")
	TmuxSocket string          `json:"tmux_socket"` // basename of the $TMUX socket
	Payload    json.RawMessage `json:"payload"`     // raw hook stdin JSON
	// AutoMode reports whether the session's environment enables plan "auto mode"
	// (e.g. $CLAUDE_CODE_ENABLE_AUTO_MODE=1), gating the plan auto-approve option.
	AutoMode bool `json:"auto_mode"`
}

// PaneController is the slice of *tmux.Client the input-preparation logic needs,
// as an interface so it stays testable without a live tmux server.
type PaneController interface {
	PaneInMode(ctx context.Context, paneID string) (bool, error)
	CancelMode(ctx context.Context, paneID string) error
	SendKeys(ctx context.Context, paneID string, keys ...string) error
}

// Discoverer scans for the adapter's live sessions and reconciles them into the
// registry. Its concrete type owns any per-scan caches.
type Discoverer interface {
	ScanOnce(ctx context.Context) error
}

// StreamingTranscript incrementally folds a growing transcript file, returning
// the full folded chunk list on each Refresh. Not safe for concurrent use.
type StreamingTranscript interface {
	Refresh() ([]transcript.Chunk, error)
}

// Adapter is one AI-coding-tool integration (e.g. claude-code, codex). All
// tool-specific behavior — discovery, hook processing, transcript parsing, input
// injection, and hook installation — lives behind this interface.
type Adapter interface {
	// Tool is the stable identifier stored on session.Session.Tool and used by the
	// core to route work back to this adapter.
	Tool() string

	// --- Discovery ---
	NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) Discoverer

	// --- Hooks ---
	// HookMethod is the JSON-RPC method the `argus hook` command calls to deliver
	// this tool's hook events to the node. The HookEvent envelope is decoded by the
	// core (it is tool-agnostic); the adapter interprets its Payload.
	HookMethod() string
	ProcessHook(reg *registry.Registry, ev HookEvent) (session.Session, bool)
	EventName(ev HookEvent) string
	PermissionPayload(ev HookEvent) (toolName string, toolInput json.RawMessage)

	// --- Transcript (live) ---
	ReadTranscriptView(path string) (transcript.TranscriptView, error)
	ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error)
	FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error)
	NewStreamingTranscript(path, rootPath string, isSubagent bool) StreamingTranscript
	// SubagentFilePath resolves a subagent trace file for agentID under rootPath.
	SubagentFilePath(rootPath, agentID string) (string, bool)

	// --- History (past sessions on disk) ---
	ListHistoryProjects() ([]session.HistoryProject, error)
	ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error)
	ReadHistoryTranscript(path string) (transcript.TranscriptView, error)
	ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error)
	FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error)

	// --- Input ---
	PrepareTextInput(ctx context.Context, pc PaneController, paneID string) error

	// --- Installer ---
	Install(argusBin string) error
	ReconcileIfInstalled(argusBin string) (added []string, err error)
	Uninstall() error
	SettingsPath() (string, error)
	DefaultHookEvents() []string
}
