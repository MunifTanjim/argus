// Package adapter defines the interface between argus's core and AI-coding-tool
// integrations such as Claude Code, Codex, or Antigravity.
package adapter

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// BundleRoot is the archive subtree every bundled file is rooted under.
const BundleRoot = "root"

// BundledFile pairs a source file on disk with its path inside an export bundle.
type BundledFile struct {
	AbsPath string // source path on disk
	RelPath string // path within the bundle (slash-separated, rooted at BundleRoot)
}

// RootedFile pairs p with its path inside an export bundle, rooted under
// BundleRoot relative to home. ok is false when p escapes home.
func RootedFile(home, p string) (BundledFile, bool) {
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return BundledFile{}, false
	}
	return BundledFile{
		AbsPath: p,
		RelPath: filepath.ToSlash(filepath.Join(BundleRoot, rel)),
	}, true
}

// WithinDir reports whether p lies inside dir (cleaned paths only, no symlink
// resolution), confining an export entry to its session subtree.
func WithinDir(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// HookMethod is the JSON-RPC method every argus hook invocation calls.
const HookMethod = "hook.event"

// HookEvent is the payload argus hook <event> sends to the node.
type HookEvent struct {
	Agent      string          `json:"agent"`       // originating coding agent, e.g. "claude"
	Event      string          `json:"event"`       // hook_event_name, e.g. "Stop"
	TmuxPane   string          `json:"tmux_pane"`   // $TMUX_PANE (e.g. "%3")
	TmuxSocket string          `json:"tmux_socket"` // basename of the $TMUX socket
	Payload    json.RawMessage `json:"payload"`     // raw hook stdin JSON
	// AutoMode reports whether the session enables plan auto mode.
	AutoMode bool `json:"auto_mode"`
	// Env carries whitelisted environment variables captured by argus hook.
	// nil for agents that don't use it.
	Env map[string]string `json:"env,omitempty"`
}

// PaneController is the tmux pane interface used by input preparation.
type PaneController interface {
	PaneInMode(ctx context.Context, paneID string) (bool, error)
	CancelMode(ctx context.Context, paneID string) error
	SendKeys(ctx context.Context, paneID string, keys ...string) error
}

// Discoverer scans for live sessions and reconciles them into the registry.
type Discoverer interface {
	ScanOnce(ctx context.Context) error
}

// StreamingTranscript incrementally folds a transcript file. Not safe for concurrent use.
type StreamingTranscript interface {
	Refresh() ([]transcript.Chunk, error)
}

// TaskSource is an optional adapter capability for agents that persist a
// structured task list (Claude Code's TaskCreate/TaskUpdate tools), so
// change-detection and reads live server-side rather than in each client.
type TaskSource interface {
	// ReadTasks returns the session's current task list, ordered by id. A
	// missing task store is empty, not an error.
	ReadTasks(transcriptPath string) ([]api.Task, error)
	// TaskActivityCount counts signals in the folded chunks that the task list
	// may have changed. The poller re-reads the transcript each tick anyway, so
	// this adds no I/O; the count only grows, so a rise means new activity to
	// push. hasTaskTool reports whether any task-tool call is present, letting
	// the caller gate teammate-only activity without a disk hit.
	TaskActivityCount(chunks []transcript.Chunk) (count int, hasTaskTool bool)
}

type Adapter interface {
	Agent() string
	AgentName() string
	AgentColor() string

	NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) Discoverer

	// SpawnCommand returns the CLI command to launch a session.
	// An empty name means the agent cannot be spawned.
	SpawnCommand(prompt string) (name string, args []string)

	// ResumeCommand returns the CLI invocation to resume a past session by its
	// agent session id. ok=false means this agent cannot resume by id; probe with
	// an empty id to test resume capability.
	ResumeCommand(agentSessionID string) (name string, args []string, ok bool)

	ProcessHook(reg *registry.Registry, ev HookEvent) (session.Session, bool)
	EventName(ev HookEvent) string
	RescanOnHook(ev HookEvent) bool
	PermissionPayload(ev HookEvent) (toolName string, toolInput json.RawMessage)
	// ShouldBlock reports whether the hook must wait for the user's decision.
	ShouldBlock(ev HookEvent) bool
	// FormatDecision renders the user's answer into the stdout the hook expects.
	FormatDecision(toolName string, toolInput json.RawMessage, p api.RespondParams) string
	// HookOutput returns stdout for an unblocked hook. "" means print nothing.
	HookOutput(ev HookEvent) string

	// CollectSessionFiles returns every on-disk file the session consists of
	// (transcript, subagents, team members, tasks, config). Vanished optionals are skipped.
	CollectSessionFiles(transcriptPath string) ([]BundledFile, error)

	ReadTranscriptView(path string) (transcript.TranscriptView, error)
	ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error)
	FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error)
	NewStreamingTranscript(path, rootPath string, isSubagent bool) StreamingTranscript
	SubagentFilePath(rootPath, agentID string) (string, bool)

	ListHistoryProjects() ([]session.HistoryProject, error)
	ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error)
	ReadHistoryTranscript(path string) (transcript.TranscriptView, error)
	ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error)
	FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error)

	PrepareTextInput(ctx context.Context, pc PaneController, paneID string) error

	Install(argusBin string) error
	ReconcileIfInstalled(argusBin string) (added []string, err error)
	Uninstall() error
	SettingsPath() (string, error)
	DefaultHookEvents() []string
}
