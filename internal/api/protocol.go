// Package api implements argus's client↔node protocol: newline-delimited
// JSON-RPC 2.0 over a stream connection. The same message types serve both the
// unix socket (local) and WebSocket+TLS (remote/gateway) transports.
package api

import (
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
)

const jsonrpcVersion = "2.0"

// Method names exchanged over the wire.
const (
	MethodPing            = "ping"             // request: no params; result: nil (latency probe)
	MethodSessionsList    = "sessions.list"    // request: no params; result: []session.Session
	MethodSessionsRefresh = "sessions.refresh" // request: no params; rescans, result: []session.Session
	MethodSessionEvent    = "session.event"    // notification: registry.Event
	// MethodSessionTranscriptView returns the grouped, display-ready chunk view.
	MethodSessionTranscriptView = "sessions.transcriptView" // request: TranscriptParams; result: claudecode.TranscriptView
	// MethodSessionToolDetail returns one tool item's full input/result, fetched
	// on demand (transcript chunks ship without these heavy bodies).
	MethodSessionToolDetail = "sessions.toolDetail" // request: ToolDetailParams; result: ToolDetail
	MethodSessionCapture    = "sessions.capture"    // request: SessionRef; result: CaptureResult
	MethodSessionInput          = "sessions.input"          // request: InputParams; result: nil
	MethodSessionKey            = "sessions.key"            // request: KeyParams; result: nil
	MethodSessionRespond        = "sessions.respond"        // request: RespondParams; result: nil
	MethodSessionSpawn          = "sessions.spawn"          // request: SpawnParams; result: SpawnResult
	MethodSessionKill           = "sessions.kill"           // request: SessionRef; result: nil
	// History (read-only, past sessions discovered on disk). Projects are aggregated
	// across nodes by the gateway; sessions/transcript are routed to the owning node.
	MethodSessionsHistoryProjects   = "sessions.historyProjects"   // request: no params; result: []session.HistoryProject
	MethodSessionsHistorySessions   = "sessions.historySessions"   // request: HistorySessionsParams; result: session.HistorySessionPage
	MethodSessionsHistoryTranscript = "sessions.historyTranscript" // request: HistoryTranscriptParams; result: claudecode.TranscriptView
	// MethodSessionHistoryToolDetail is the history counterpart of
	// MethodSessionToolDetail: one tool item's full input/result from a past
	// session's transcript, addressed by node-local path.
	MethodSessionHistoryToolDetail = "sessions.historyToolDetail" // request: HistoryToolDetailParams; result: ToolDetail
	// MethodNodesList returns the nodes currently connected to the gateway, so a
	// client can offer a spawn target without first having a session on each node.
	MethodNodesList = "nodes.list" // request: no params; result: []NodeInfo
	// MethodNodeIdentify is called by the gateway over a node uplink to learn the
	// node's stable id and label before aggregating it.
	MethodNodeIdentify = "node.identify" // request: no params; result: IdentifyResult
	// MethodTranscriptSubscribe opens a streaming transcript subscription.
	MethodTranscriptSubscribe = "transcript.subscribe" // request: TranscriptSubscribeParams; result: TranscriptDelta
	// MethodTranscriptUnsubscribe closes a streaming transcript subscription.
	MethodTranscriptUnsubscribe = "transcript.unsubscribe" // request: TranscriptUnsubscribeParams; result: nil
	// MethodTranscriptDelta is a server->client push of new/changed chunks.
	MethodTranscriptDelta = "transcript.delta" // notification: TranscriptDelta
	// Client-token management (gateway only, admin/master-token connections).
	// MethodClientsPairStart mints a temporary token and returns it with the
	// gateway's public base URL so the caller can render a pairing QR.
	MethodClientsPairStart = "clients.pairStart" // request: no params; result: PairStartResult
	// MethodClientsPairAwait blocks until the minted token's device connects, or
	// the call's deadline elapses.
	MethodClientsPairAwait = "clients.pairAwait" // request: PairAwaitParams; result: PairAwaitResult
	// MethodClientsList returns the persisted client tokens.
	MethodClientsList = "clients.list" // request: no params; result: []ClientTokenInfo
	// MethodClientsRemove revokes a client token by deleting its record.
	MethodClientsRemove = "clients.remove" // request: ClientRemoveParams; result: nil
	// Push-notification device registration (gateway only). A client registers the
	// target its OS push backend gave it so the gateway can reach the device when
	// the app is backgrounded or killed.
	// MethodPushRegister records (or refreshes) a device's push target, keyed by
	// the caller's device_id (so re-registration replaces the prior endpoint).
	MethodPushRegister = "push.register" // request: PushRegisterParams; result: nil
	// MethodPushUnregister drops a device's push target (called on unpair).
	MethodPushUnregister = "push.unregister" // request: PushDeviceRef; result: nil
	// MethodPushTest sends a test notification to the device's registered target
	// through the real backend, so it can verify end-to-end delivery.
	MethodPushTest = "push.test" // request: PushDeviceRef; result: nil
	// MethodPushVAPIDKey returns the gateway's VAPID public key so a device can
	// register a Web Push subscription bound to it (e.g. the embedded FCM
	// distributor). Empty Key means Web Push is unavailable.
	MethodPushVAPIDKey = "push.vapidKey" // request: no params; result: PushVAPIDKey
)

// PushVAPIDKey carries the gateway's VAPID public key (base64url, uncompressed
// P-256 point) — the applicationServerKey a device subscribes with.
type PushVAPIDKey struct {
	Key string `json:"key,omitempty"`
}

// PushRegisterParams registers a device's Web Push target. DeviceID is a stable
// per-device id (the storage key). Endpoint is the distributor URL the gateway
// POSTs to; P256dh/Auth are the Web Push subscription keys (RFC 8291).
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

// PairStartResult carries a freshly minted client token plus the gateway's
// public base URL (no path) used to build the pairing QR.
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

// IdentifyResult announces a node's identity to the gateway. ID is the stable
// node id (the composite-id prefix and routing key); Label is human-friendly.
type IdentifyResult struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// NodeInfo identifies a node connected to the gateway. It is the unit returned by
// nodes.list and the routing key (NodeID) a client passes to sessions.spawn.
type NodeInfo struct {
	NodeID    string `json:"node_id"`
	NodeLabel string `json:"node_label"`
}

// SpawnParams launches a new Claude Code session on argus's private tmux server.
// NodeID, set by a gateway client, selects which node spawns the session; the
// node itself ignores it.
type SpawnParams struct {
	NodeID  string `json:"node_id,omitempty"` // gateway routing key; ignored node-side
	Name    string `json:"name"`              // tmux session name
	Cwd     string `json:"cwd,omitempty"`     // working directory
	Command string `json:"command,omitempty"` // launch command (default: "claude")
}

// SpawnResult identifies the newly created session.
type SpawnResult struct {
	SessionID string `json:"session_id"`
	PaneID    string `json:"pane_id"`
}

// HistorySessionsParams lists one project's past sessions. NodeID selects the
// owning node (gateway routing key; ignored node-side); ProjectDir is that node's
// local project path from a HistoryProject. Limit <= 0 returns all from Offset.
type HistorySessionsParams struct {
	NodeID     string `json:"node_id,omitempty"`
	ProjectDir string `json:"project_dir"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}

// HistoryTranscriptParams reads a past session's transcript by its node-local path.
// NodeID selects the owning node (gateway routing key; ignored node-side).
type HistoryTranscriptParams struct {
	NodeID         string `json:"node_id,omitempty"`
	TranscriptPath string `json:"transcript_path"`
}

// SessionRef selects a session by id.
type SessionRef struct {
	SessionID string `json:"session_id"`
}

// TranscriptParams selects a session for MethodSessionTranscriptView.
type TranscriptParams = SessionRef

// ToolDetailParams selects one tool item's full body in a live session's
// transcript. AgentID selects a subagent trace file; empty means the session
// transcript. ToolID is the tool_use id carried on the (stripped) chunk item.
type ToolDetailParams struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
	ToolID    string `json:"tool_id"`
}

// HistoryToolDetailParams is the history counterpart: a tool item's full body in
// a past session's transcript, addressed by node-local path. NodeID selects the
// owning node (gateway routing key; ignored node-side).
type HistoryToolDetailParams struct {
	NodeID         string `json:"node_id,omitempty"`
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

// InputParams sends text to a session. When Submit is true an Enter key is sent
// after the text. When Prepare is true the pane is first normalized for input
// (exit tmux copy mode; if Claude vim mode is on, leave it in insert) — set for
// discrete composer sends, not live key streaming.
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

// RespondParams answers a pending interaction (see session.Interaction).
// Preferred path: the node has a parked PermissionRequest hook for the session
// and turns this into the hook's decision JSON (Behavior, Reason, Answers). If no
// hook is parked (older Claude, or an idle prompt) it falls back to pane
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
	// OptionValue echoes a server-built DecisionOption.Value the user picked. The
	// node maps it to behavior/set-mode: "deny" → deny; "allow" → plain allow;
	// any other value → allow + setMode <value>.
	OptionValue string `json:"option_value,omitempty"`

	// Keystroke fallback (no parked hook / idle).
	Kind        string `json:"kind,omitempty"`
	OptionIndex int    `json:"option_index,omitempty"`
	Text        string `json:"text,omitempty"`
}

// HookResult is the node's reply to a hook.event call. Output, when non-empty,
// is the hookSpecificOutput JSON the `argus hook` command prints to stdout for
// Claude Code (used by the blocking PermissionRequest path).
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

// TranscriptSubscribeParams opens a subscription. AgentID selects a subagent
// trace; empty means the session transcript. HaveChunks is how many chunks the
// client already has cached (0 = none), used to send a minimal catch-up.
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
	Chunks    []claudecode.Chunk `json:"chunks"`
}
