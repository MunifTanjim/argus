// Package adapter defines the interface between argus's core and AI-coding-tool
// integrations such as Claude Code, Codex, or Antigravity.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// InstallOptions configures an agent's argus integration. Empty fields keep what
// is already installed.
type InstallOptions struct {
	ArgusBin string // binary the hooks invoke (hook-based agents)
	Socket   string // argusd mod socket (Claude Code, Pi)
}

// ErrAgentNotInstalled reports that an agent's CLI is not available, so there is
// nothing to install into.
var ErrAgentNotInstalled = errors.New("agent not installed")

// BundleRoot is the archive subtree every bundled file is rooted under.
const BundleRoot = "root"

// BundledFile pairs a source file on disk with its path inside an export bundle.
type BundledFile struct {
	AbsPath   string // source path on disk
	RelPath   string // path within the bundle (slash-separated, rooted at BundleRoot)
	Transient bool   // the export handler should remove this file after bundling
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

// MaxHookPayload caps the hook payload argus reads, from `argus hook` stdin or a
// mod request body.
const MaxHookPayload = 8 << 20

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
	Refresh() ([]transcript.Entry, error)
}

// QueueSource is a StreamingTranscript that also follows the agent's prompt
// queue, as of its last Refresh.
type QueueSource interface {
	Queue() []transcript.QueuedPrompt
	ClearQueue()
}

// TaskSource is an optional adapter capability for agents that persist a
// structured task list (Claude Code's TaskCreate/TaskUpdate tools), so
// change-detection and reads live server-side rather than in each client.
type TaskSource interface {
	// ReadTasks returns the session's current task list, ordered by id. A
	// missing task store is empty, not an error.
	ReadTasks(sessionIDs []string, transcriptPath string) ([]api.Task, error)
	// TaskActivityCount counts signals in the folded entries that the task list
	// may have changed. The poller re-reads the transcript each tick anyway, so
	// this adds no I/O; the count only grows, so a rise means new activity to
	// push. hasTaskTool reports whether any task-tool call is present, letting
	// the caller gate teammate-only activity without a disk hit.
	TaskActivityCount(entries []transcript.Entry) (count int, hasTaskTool bool)
}

// Responder answers a pending interaction by an outbound action, for adapters
// that do not park a blocking hook call (for example an HTTP-service agent).
// The node type-asserts this on the session's adapter when no parked decision
// exists for the respond.
type Responder interface {
	Respond(ctx context.Context, sess session.Session, p api.RespondParams) error
}

// InteractionOwner is implemented by a Responder that updates the session's
// interaction itself after Respond; the node then skips ClearInteraction.
type InteractionOwner interface {
	OwnsInteraction() bool
}

// Dismisser removes a session from the live list on the user's request, for
// sessions with no pane to kill (opencode, paneless Claude).
type Dismisser interface {
	Dismiss(ctx context.Context, sess session.Session) error
}

// Renamer renames a session through the agent's own rename feature, so the
// name shows in the agent's UI too. applied is false when the agent takes the
// name later and reports it itself; the node records only an applied name.
type Renamer interface {
	RenameSession(ctx context.Context, sess session.Session, name string) (applied bool, err error)
}

// Prompter delivers a user prompt to a session over the agent's own API. An agent
// that takes no prompt while a turn runs refuses it with api.ErrSessionBusy.
type Prompter interface {
	SendPrompt(ctx context.Context, sess session.Session, text string) error
}

// PromptQueuer is an adapter whose busy sessions take prompts, queued until the
// agent reads them.
type PromptQueuer interface {
	QueuesPrompts(s session.Session) bool
}

// PaneFiller puts a prompt in a busy session's own input box, for the node to
// submit with Enter in its pane. filled is false when the agent took the prompt
// another way and needs no Enter.
type PaneFiller interface {
	FillPrompt(ctx context.Context, sess session.Session, text string) (filled bool, err error)
}

// ShellInput runs input the agent's prompt API would send to the model as text:
// "!" bash mode, and commands only the agent's editor runs. submit is true when
// the input waits in the session's input box for the node to submit with Enter
// in its pane.
type ShellInput interface {
	IsShellInput(text string) bool
	SendShellInput(ctx context.Context, sess session.Session, text string) (submit bool, err error)
}

// ModBridged is an adapter whose sessions keep a mod connection to argusd (Claude
// Code); the node serves the mod socket from its bridge.
type ModBridged interface {
	ModBridge() *modbridge.Bridge
}

// Beaconed is an adapter whose sessions hold one open connection to argusd on
// the mod socket (Pi); the node hands it each connection upgraded there.
type Beaconed interface {
	ServeBeacon(conn net.Conn)
}

// Spawner creates a new session over the agent's own service API, for headless
// agents whose sessions are not launched as a tmux pane process (opencode). It
// returns the agent's own session id, which the registry keys the record by.
type Spawner interface {
	SpawnSession(ctx context.Context, cwd string, opts SpawnOptions) (agentSessionID string, err error)
}

// SpawnOptions are the per-spawn choices an adapter turns into a launch. An empty
// field means the agent's own default: the adapter adds nothing for it.
type SpawnOptions struct {
	Prompt string
	Model  string // a ModelLister id
}

// ModelLister is an adapter that offers a model choice at spawn. The ids it
// returns are what SpawnOptions.Model carries back.
type ModelLister interface {
	ListModels(ctx context.Context) ([]api.ModelInfo, error)
}

// PromptCompleter is an adapter that lists the commands and skills a prompt
// can invoke after a trigger character.
type PromptCompleter interface {
	CompletePrompt(ctx context.Context, sess session.Session, trigger string) ([]api.PromptCompletion, error)
}

// CompactCompletion is /compact for agents whose API runs it.
var CompactCompletion = api.PromptCompletion{
	Name:        "compact",
	Description: "summarize conversation to prevent hitting the context limit",
}

func IsCompact(text string) bool { return strings.TrimSpace(text) == "/"+CompactCompletion.Name }

type Adapter interface {
	Agent() string
	AgentName() string
	AgentColor() string

	// IsHeadless reports that the agent's session runs with no attached terminal by
	// default (a background service): a tmux pane is an optional viewer of it. When
	// true, argus can spawn or respawn a pane for a live session without restarting
	// it; when false, the pane is the session's own process and losing it ends it.
	IsHeadless() bool

	NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) Discoverer

	// SpawnCommand returns the CLI command to launch a session.
	// An empty name means the agent cannot be spawned.
	SpawnCommand(opts SpawnOptions) (name string, args []string)

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

	Install(opts InstallOptions) error
	// Reconcile installs or updates the integration; cheap when nothing changed.
	Reconcile(opts InstallOptions) (added []string, err error)
	Uninstall() error
	SettingsPath() (string, error)
	DefaultHookEvents() []string
}
