// Package api implements argus's client↔node protocol: newline-delimited
// JSON-RPC 2.0 over a stream connection. The same message types serve both the
// unix socket (local) and WebSocket+TLS (remote/gateway) transports.
package api

import (
	"encoding/json"
	"errors"

	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// ErrNoTerminalControl is returned for pane-bound actions on a session argus
// cannot drive (paneless vscode/external). Clients gate the action's UI on it.
var ErrNoTerminalControl = errors.New("session has no terminal control")

const jsonrpcVersion = "2.0"

// Method names exchanged over the wire.
const (
	MethodPing                  = "ping"                    // request: no params; result: nil (latency probe)
	MethodSessionsList          = "sessions.list"           // request: no params; result: []session.Session
	MethodSessionsRefresh       = "sessions.refresh"        // request: no params; rescans, result: []session.Session
	MethodSessionEvent          = "session.event"           // notification: registry.Event
	MethodSessionTranscriptView = "sessions.transcriptView" // request: TranscriptParams; result: transcript.TranscriptView
	MethodSessionToolDetail     = "sessions.toolDetail"     // request: ToolDetailParams; result: ToolDetail
	MethodSessionCapture        = "sessions.capture"        // request: SessionRef; result: CaptureResult
	MethodSessionInput          = "sessions.input"          // request: InputParams; result: nil
	MethodSessionKey            = "sessions.key"            // request: KeyParams; result: nil
	MethodSessionRespond        = "sessions.respond"        // request: RespondParams; result: nil
	MethodSessionSpawn          = "sessions.spawn"          // request: SpawnParams; result: SpawnResult
	MethodSessionResume         = "sessions.resume"         // request: ResumeParams; result: ResumeResult
	// Probed live per call.
	MethodAgentsList   = "agents.list"    // request: AgentsListParams; result: AgentsListResult
	MethodSessionKill  = "sessions.kill"  // request: SessionRef; result: nil
	MethodSessionFocus = "sessions.focus" // request: SessionRef; result: nil (focus the session's tmux pane on its owning node)
	// History (read-only, past sessions discovered on disk). Projects are aggregated
	// across nodes by the gateway; sessions/transcript are routed to the owning node.
	MethodSessionsHistoryProjects   = "sessions.historyProjects"   // request: no params; result: []session.HistoryProject
	MethodSessionsHistorySessions   = "sessions.historySessions"   // request: HistorySessionsParams; result: session.HistorySessionPage
	MethodSessionsHistoryTranscript = "sessions.historyTranscript" // request: HistoryTranscriptParams; result: transcript.TranscriptView
	MethodSessionHistoryToolDetail  = "sessions.historyToolDetail" // request: HistoryToolDetailParams; result: ToolDetail
	MethodNodeIdentify              = "node.identify"              // request: no params; result: IdentifyResult
	MethodServerInfo                = "server.info"                // request: no params; result: ServerInfo
	MethodTranscriptSubscribe       = "transcript.subscribe"       // request: TranscriptSubscribeParams; result: TranscriptDelta
	MethodTranscriptUnsubscribe     = "transcript.unsubscribe"     // request: TranscriptUnsubscribeParams; result: nil
	// Server→client push.
	MethodTranscriptDelta = "transcript.delta" // notification: TranscriptDelta
	MethodTerminalOpen    = "terminal.open"    // request: TerminalOpenParams; result: nil
	MethodTerminalOutput  = "terminal.output"  // notification: TerminalOutput
	MethodTerminalInput   = "terminal.input"   // request: TerminalInputParams; result: nil
	MethodTerminalResize  = "terminal.resize"  // request: TerminalResizeParams; result: nil
	MethodTerminalClose   = "terminal.close"   // request: TerminalCloseParams; result: nil
	MethodTerminalExited  = "terminal.exited"  // notification: TerminalExited (server→client, PTY ended)
	// Client-token management (gateway only, admin/master-token connections).
	// MethodClientsPairStart mints a temporary token + public URL for a pairing QR.
	MethodClientsPairStart = "clients.pairStart" // request: no params; result: PairStartResult
	// MethodClientsPairAwait blocks until the minted token's device connects, or the deadline elapses.
	MethodClientsPairAwait = "clients.pairAwait" // request: PairAwaitParams; result: PairAwaitResult
	// MethodClientsList returns the persisted client tokens.
	MethodClientsList = "clients.list" // request: no params; result: []ClientTokenInfo
	// MethodClientsRemove revokes a client token by deleting its record.
	MethodClientsRemove = "clients.remove" // request: ClientRemoveParams; result: nil
	// MethodPushRegister records (or refreshes) a device's push target, keyed by
	// device_id (so re-registration replaces the prior endpoint).
	MethodPushRegister = "push.register" // request: PushRegisterParams; result: nil
	// MethodPushUnregister drops a device's push target (called on unpair).
	MethodPushUnregister = "push.unregister" // request: PushDeviceRef; result: nil
	// MethodPushTest sends a test notification through the real backend to verify delivery.
	MethodPushTest = "push.test" // request: PushDeviceRef; result: nil
	// MethodPushVAPIDKey returns the gateway's VAPID public key for a device to
	// subscribe with. Empty Key means Web Push is unavailable.
	MethodPushVAPIDKey  = "push.vapidKey"         // request: no params; result: PushVAPIDKey
	MethodPushDesktop   = "push.desktop"          // request: push.Notification; result: nil (render on node if opted in)
	MethodSessionExport = "sessions.exportBundle" // request: ExportBundleParams; result: ExportBundleResult
	// Changed-files review for a live session's working directory (vs HEAD).
	MethodSessionChangedFiles = "sessions.changedFiles" // request: SessionRef; result: ChangedFilesResult
	MethodSessionFileDiff     = "sessions.fileDiff"     // request: FileDiffParams; result: FileDiffResult
	MethodSessionCommits      = "sessions.commits"      // request: SessionRef; result: CommitsResult
	MethodSessionCommitFiles  = "sessions.commitFiles"  // request: CommitFilesParams; result: ChangedFilesResult
)

// ChangedFile is one entry in a session working directory's git status.
type ChangedFile struct {
	Path     string `json:"path"`                // current (working-tree) path
	OrigPath string `json:"orig_path,omitempty"` // rename source (HEAD-side), else ""
	Change   string `json:"change"`              // added|modified|deleted|renamed|untracked
	Staged   bool   `json:"staged"`              // index differs from HEAD
	Unstaged bool   `json:"unstaged,omitempty"`  // working tree differs from index
}

// ChangedFilesResult lists everything git status reports for the session's repo.
type ChangedFilesResult struct {
	Root  string        `json:"root,omitempty"` // repo top-level (for display)
	Files []ChangedFile `json:"files"`
}

// Commit is one entry in a session's branch/unpushed commit log.
type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	UnixSec int64  `json:"unix_sec"` // authored time; client formats
}

// CommitsResult carries the commit list. Unpushed is true when the scope is
// unpushed-vs-remote.
type CommitsResult struct {
	Commits  []Commit `json:"commits"`
	Unpushed bool     `json:"unpushed,omitempty"`
}

// CommitFilesParams selects one commit in a session's repo.
type CommitFilesParams struct {
	SessionID string `json:"session_id"`
	SHA       string `json:"sha"`
}

// FileDiffParams selects one changed file in a session's working directory.
type FileDiffParams struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	OrigPath  string `json:"orig_path,omitempty"` // rename source, so the old side resolves
	Rev       string `json:"rev,omitempty"`       // commit sha; empty = HEAD vs worktree
}

// FileDiffResult carries a changed file's HEAD (old) and working-tree (new)
// content. Either side is "" when absent; NotShown (both empty) marks binary or
// oversized files.
type FileDiffResult struct {
	Path       string `json:"path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
	NotShown   bool   `json:"not_shown,omitempty"`
}

// ExportBundleParams selects a session to export. Metadata is supplied by the
// client (what it already displays) and written verbatim into the manifest.
type ExportBundleParams struct {
	NodeID         string          `json:"node_id,omitempty"` // gateway routing
	Agent          string          `json:"agent"`
	TranscriptPath string          `json:"transcript_path"`
	Metadata       bundle.Metadata `json:"metadata"`
}

// ExportBundleResult carries the gzipped-tar bundle. Data marshals to base64.
type ExportBundleResult struct {
	Filename string `json:"filename"`
	Data     []byte `json:"data"`
}

// PushVAPIDKey carries the gateway's VAPID public key (the applicationServerKey a
// device subscribes with).
type PushVAPIDKey struct {
	Key string `json:"key,omitempty"`
}

// PushRegisterParams registers a device's Web Push target. DeviceID is the stable
// storage key; Endpoint is the distributor URL; P256dh/Auth are subscription keys.
type PushRegisterParams struct {
	DeviceID string `json:"device_id"`
	Endpoint string `json:"endpoint,omitempty"`
	P256dh   string `json:"p256dh,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

// PushDeviceRef identifies a device by its stable id (for unregister/test).
type PushDeviceRef struct {
	DeviceID string `json:"device_id"`
}

// PairStartResult carries a freshly minted client token plus the gateway's public
// base URL for the pairing QR.
type PairStartResult struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// PairAwaitParams identifies the minted token to wait on.
type PairAwaitParams struct {
	Token string `json:"token"`
}

// PairAwaitResult reports whether a device connected with the token before the
// pairing window closed.
type PairAwaitResult struct {
	Connected bool `json:"connected"`
}

// ClientTokenInfo is one persisted client token in a clients.list result.
type ClientTokenInfo struct {
	Token     string `json:"token"`
	CreatedAt string `json:"created_at"`
}

// ClientRemoveParams identifies the client token to revoke.
type ClientRemoveParams struct {
	Token string `json:"token"`
}

// NodeCapabilities describes what a node supports, so clients can gate features
// per node. Reported by node.identify and server.info.
type NodeCapabilities struct {
	// SpawnSession reports whether the node can spawn sessions (tmux present).
	SpawnSession bool `json:"spawn_session"`
}

// ServerInfo carries server-wide metadata for a connected client: server version
// and connected nodes. Served by the gateway.
type ServerInfo struct {
	Version string     `json:"version"`
	Nodes   []NodeInfo `json:"nodes"`
}

// IdentifyResult announces a node's identity to the gateway. ID is the stable node
// id (composite-id prefix and routing key).
type IdentifyResult struct {
	ID           string           `json:"id"`
	Label        string           `json:"label"`
	Version      string           `json:"version"` // node's binary version
	Capabilities NodeCapabilities `json:"capabilities"`
}

// NodeInfo identifies a node connected to the gateway (the unit in server.info).
// ID is the routing key a client passes to sessions.spawn as node_id.
type NodeInfo struct {
	ID           string           `json:"id"`
	Label        string           `json:"label"`
	Version      string           `json:"version"` // node's binary version
	Capabilities NodeCapabilities `json:"capabilities"`
}

type SpawnParams struct {
	NodeID  string `json:"node_id,omitempty"` // gateway routing key; ignored node-side
	Name    string `json:"name,omitempty"`    // tmux session name; blank = node-generated default
	Cwd     string `json:"cwd,omitempty"`
	Agent   string `json:"agent,omitempty"`   // node resolves to a command; blank = default
	Command string `json:"command,omitempty"` // explicit launch command; overrides Agent when set
	Prompt  string `json:"prompt,omitempty"`  // initial prompt; how it reaches the CLI is agent-specific (an argument, or a flag like --prompt-interactive)
}

// SpawnResult identifies the newly created session.
type SpawnResult struct {
	SessionID string `json:"session_id"`
	PaneID    string `json:"pane_id"`
}

// ResumeParams asks a node to resume a past session by its agent session id, in
// the session's original working directory.
type ResumeParams struct {
	NodeID         string `json:"node_id,omitempty"` // gateway routing key; ignored node-side
	Agent          string `json:"agent"`             // owning agent id
	AgentSessionID string `json:"agent_session_id"`  // the tool's own session id to resume
	Cwd            string `json:"cwd"`               // original working directory
}

// ResumeResult identifies the session to open, whether newly spawned or an
// already-live session the resume jumped to.
type ResumeResult struct {
	SessionID string `json:"session_id"`
}

type AgentsListParams struct {
	NodeID string `json:"node_id,omitempty"` // gateway routing key; empty = sole node
}

type AgentInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Spawnable bool   `json:"spawnable"`
}

// AgentsListResult lists every agent the node knows, in priority order (first =
// default spawn target among the spawnable ones).
type AgentsListResult struct {
	Agents []AgentInfo `json:"agents"`
}

// HistorySessionsParams: Limit <= 0 returns all from Offset.
type HistorySessionsParams struct {
	NodeID     string `json:"node_id,omitempty"`
	ProjectDir string `json:"project_dir"` // cwd merge key (node-local) shared across agents
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}

// HistoryTranscriptParams reads a past session's transcript by node-local path.
// AgentID selects a nested subagent trace.
type HistoryTranscriptParams struct {
	NodeID         string `json:"node_id,omitempty"`
	Agent          string `json:"agent,omitempty"` // owning agent; selects the adapter
	TranscriptPath string `json:"transcript_path"`
	AgentID        string `json:"agent_id,omitempty"`
}

// SessionRef selects a session by id.
type SessionRef struct {
	SessionID string `json:"session_id"`
}

// TranscriptParams selects a session for MethodSessionTranscriptView.
type TranscriptParams = SessionRef

// ToolDetailParams selects one tool item's full body in a live session's
// transcript. AgentID selects a subagent trace; ToolID is the tool_use id from
// the (stripped) chunk item.
type ToolDetailParams struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
	ToolID    string `json:"tool_id"`
}

// HistoryToolDetailParams is the history counterpart of ToolDetailParams,
// addressed by node-local path.
type HistoryToolDetailParams struct {
	NodeID         string `json:"node_id,omitempty"`
	Agent          string `json:"agent,omitempty"` // owning agent; selects the adapter
	TranscriptPath string `json:"transcript_path"`
	AgentID        string `json:"agent_id,omitempty"`
	ToolID         string `json:"tool_id"`
}

// ToolDetail is one tool item's heavy body, fetched on demand. The json tags
// match the Item fields so clients can fill a cached item in place.
type ToolDetail struct {
	ToolInput     string `json:"toolInput,omitempty"`
	Result        string `json:"result,omitempty"`
	ResultIsError bool   `json:"resultIsError,omitempty"`
}

// CaptureResult is the rendered screen of a session's tmux pane.
type CaptureResult struct {
	Screen string `json:"screen"`
}

// InputParams sends text to a session. Submit appends an Enter. Prepare first
// normalizes the pane for input (exit copy mode; leave vim mode in insert) — set
// for discrete composer sends, not live key streaming.
type InputParams struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Submit    bool   `json:"submit"`
	Prepare   bool   `json:"prepare"`
}

// KeyParams sends one or more named keys (e.g. "Escape", "C-c", "Enter").
type KeyParams struct {
	SessionID string   `json:"session_id"`
	Keys      []string `json:"keys"`
}

// RespondParams answers a pending interaction (see session.Interaction). Preferred
// path: a parked PermissionRequest hook turns this into the hook's decision JSON
// (Behavior, Reason, Answers). Without a parked hook it falls back to pane
// keystrokes (Kind/OptionIndex/Text).
type RespondParams struct {
	SessionID string `json:"session_id"`

	// Structured hook decision (PermissionRequest path).
	Behavior string         `json:"behavior,omitempty"` // "allow" | "deny"
	Reason   string         `json:"reason,omitempty"`   // deny message
	Answers  map[string]any `json:"answers,omitempty"`  // AskUserQuestion: question -> label|[label]|text
	// QuestionAction is a non-answer action on an AskUserQuestion prompt.
	// "" = normal answer submit; "chat" = reject with a clarify request.
	QuestionAction string `json:"question_action,omitempty"`
	// SetMode, on an allow decision (e.g. ExitPlanMode approval), switches the
	// session's permission mode: "acceptEdits" | "default" | "auto".
	SetMode string `json:"set_mode,omitempty"`
	// OptionValue echoes a server-built DecisionOption.Value. Node maps it:
	// "deny" → deny; "allow" → plain allow; any other → allow + setMode <value>.
	OptionValue string `json:"option_value,omitempty"`

	// Keystroke fallback (no parked hook / idle).
	Kind        string `json:"kind,omitempty"`
	OptionIndex int    `json:"option_index,omitempty"`
	Text        string `json:"text,omitempty"`
}

// HookResult is the node's reply to a hook.event call. Output is the
// hookSpecificOutput JSON `argus hook` prints to stdout (blocking PermissionRequest path).
type HookResult struct {
	Output string `json:"output,omitempty"`
}

// message is the unified JSON-RPC envelope. A request has Method and ID; a
// notification has Method and no ID; a response has ID and Result/Error.
type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

func (m *message) isRequest() bool      { return m.Method != "" && m.ID != nil }
func (m *message) isNotification() bool { return m.Method != "" && m.ID == nil }

// Decode unmarshals JSON-RPC params into T, centralizing the decode-and-check
// boilerplate shared by node and gateway handlers. Empty params yield the zero value
// (handlers validate required fields themselves).
func Decode[T any](params json.RawMessage) (T, error) {
	var v T
	if len(params) == 0 {
		return v, nil
	}
	err := json.Unmarshal(params, &v)
	return v, err
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Standard JSON-RPC error codes used by argus.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInternalError  = -32603
)

// Application-defined error codes (outside the JSON-RPC reserved range).
const (
	// CodePushGone marks a push.test against a permanently dead target (404/410).
	// The gateway has already pruned it; the client should fetch a fresh endpoint
	// rather than re-register the dead one.
	CodePushGone = 410
)

// TranscriptSubscribeParams opens a subscription. AgentID selects a subagent
// trace. HaveChunks is the client's cached chunk count, for a minimal catch-up.
type TranscriptSubscribeParams struct {
	SubID      string `json:"sub_id"`
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id,omitempty"`
	HaveChunks int    `json:"have_chunks"`
}

// TranscriptUnsubscribeParams closes the subscription identified by SubID.
type TranscriptUnsubscribeParams struct {
	SubID string `json:"sub_id"`
}

// TranscriptDelta is both the subscribe result (initial catch-up) and the push
// payload. The client truncates its cached chunks to FromIndex, then appends Chunks.
type TranscriptDelta struct {
	SubID     string             `json:"sub_id"`
	FromIndex int                `json:"from_index"`
	Chunks    []transcript.Chunk `json:"chunks"`
}

// TerminalOpenParams opens a terminal session with the given dimensions.
type TerminalOpenParams struct {
	TermID    string `json:"term_id"`
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	// ClientPane is the caller's own tmux pane ($TMUX_PANE) when co-located on the
	// session's server; empty otherwise (e.g. the mobile app). The node uses it to
	// refuse an open that would share the agent pane's window.
	ClientPane string `json:"client_pane,omitempty"`
}

// TerminalOutput is server→client output from a terminal session (base64-encoded data).
type TerminalOutput struct {
	TermID string `json:"term_id"`
	Data   string `json:"data"`
}

// TerminalExited notifies the client that a terminal attach ended. Reason tells
// the client why so it can show a fitting message (see the TermExited* consts).
type TerminalExited struct {
	TermID string `json:"term_id"`
	// Reason is why the attach ended; empty is treated as TermExitedProcess.
	Reason string `json:"reason,omitempty"`
}

// Reasons carried by TerminalExited.Reason.
const (
	// TermExitedProcess: the PTY ended on its own (process exited / mirror died).
	TermExitedProcess = "exited"
	// TermExitedEvicted: booted because the same session was opened elsewhere
	// (single-viewer enforcement; last opener wins).
	TermExitedEvicted = "evicted"
)

// TerminalInputParams sends input data to a terminal session.
type TerminalInputParams struct {
	TermID string `json:"term_id"`
	Data   string `json:"data"`
}

// TerminalResizeParams resizes a terminal session.
type TerminalResizeParams struct {
	TermID string `json:"term_id"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// TerminalCloseParams closes a terminal session.
type TerminalCloseParams struct {
	TermID string `json:"term_id"`
}
