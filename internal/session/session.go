// Package session defines the core model argus tracks for every AI coding tool
// session it discovers or manages. It is transport- and tool-agnostic: the
// registry stores these, adapters populate them, and clients render them.
package session

// Status is the lifecycle/activity state of a session, driven authoritatively by
// tool hooks where available and by a tmux fallback heuristic otherwise.
type Status string

const (
	// StatusDiscovered means the session was found (e.g. a tmux pane running
	// claude) but no authoritative hook event has arrived yet.
	StatusDiscovered Status = "discovered"
	// StatusWorking means the tool is actively processing (between a prompt
	// submission and a stop, or running a tool call).
	StatusWorking Status = "working"
	// StatusAwaitingInput means the tool is blocked on the user (e.g. a
	// permission prompt).
	StatusAwaitingInput Status = "awaiting_input"
	// StatusIdle means the tool finished responding and is waiting for the
	// next prompt.
	StatusIdle Status = "idle"
	// StatusDead means the underlying process/pane is gone.
	StatusDead Status = "dead"
)

// Label is the human-readable status word clients display next to the glyph. The
// server owns this string so wording stays consistent across clients.
func (s Status) Label() string {
	switch s {
	case StatusWorking:
		return "working"
	case StatusAwaitingInput:
		return "awaiting"
	case StatusIdle:
		return "idle"
	case StatusDiscovered:
		return "discovered"
	case StatusDead:
		return "dead"
	default:
		return string(s)
	}
}

// Source records how argus learned about a session.
type Source string

const (
	// SourceDiscovered: found by scanning tmux.
	SourceDiscovered Source = "discovered"
	// SourceSpawned: created by argus on its private tmux socket.
	SourceSpawned Source = "spawned"
	// SourceHooked: first seen via a tool hook event.
	SourceHooked Source = "hooked"
)

// Frontend classifies where a session's UI lives, which determines whether argus
// can drive its terminal. tmux sessions have a pane and are fully controllable;
// vscode/external sessions are paneless (hook-only) and support decisions but not
// terminal control.
type Frontend string

const (
	// FrontendTmux: has a tmux pane → fully controllable.
	FrontendTmux Frontend = "tmux"
	// FrontendVSCode: paneless, started from the VSCode extension (entrypoint "claude-vscode").
	FrontendVSCode Frontend = "vscode"
	// FrontendExternal: paneless, some other non-tmux terminal.
	FrontendExternal Frontend = "external"
)

// TmuxServer identifies which tmux server a pane lives on.
type TmuxServer string

const (
	// TmuxServerDefault is the user's normal tmux server.
	TmuxServerDefault TmuxServer = "default"
	// TmuxServerArgus is the private "tmux -L argus" socket argus spawns into.
	TmuxServerArgus TmuxServer = "argus"
)

// TmuxLocation pins a session to a concrete tmux pane. PaneID (e.g. "%3") is the
// stable, never-reused identifier and is the primary correlation key.
type TmuxLocation struct {
	Server      TmuxServer `json:"server"`
	PaneID      string     `json:"pane_id"`
	SessionName string     `json:"session_name"`
	WindowIndex int        `json:"window_index"`
	CurrentPath string     `json:"current_path"`
}

// InteractionKind classifies what a session is waiting on the user for.
type InteractionKind string

const (
	// InteractionPermission: Claude wants approval to use a tool (1/2/3 prompt).
	InteractionPermission InteractionKind = "permission"
	// InteractionQuestion: an AskUserQuestion multiple-choice prompt.
	InteractionQuestion InteractionKind = "question"
	// InteractionPlan: an ExitPlanMode plan-approval prompt.
	InteractionPlan InteractionKind = "plan"
	// InteractionIdle: Claude finished and is waiting for the next message, or a
	// generic notification needing attention.
	InteractionIdle InteractionKind = "idle"
)

// QuestionSpec is one question within an AskUserQuestion interaction. A single
// AskUserQuestion call may carry several (1-4), each independently answered.
type QuestionSpec struct {
	Header      string `json:"header,omitempty"`       // short category label (chip)
	Question    string `json:"question,omitempty"`     // the question prompt
	MultiSelect bool   `json:"multi_select,omitempty"` // allows multiple selections
	// Options and the positionally-aligned per-option help/preview text.
	Options            []string `json:"options,omitempty"`
	OptionDescriptions []string `json:"option_descriptions,omitempty"`
	OptionPreviews     []string `json:"option_previews,omitempty"`
}

// DecisionOption is a server-built choice for a permission/plan decision. The
// client renders Label and, on selection, echoes Value back in
// RespondParams.OptionValue; the node maps Value to the hook decision. Reject
// marks the choice that denies (and surfaces a free-text reason field).
type DecisionOption struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Reject bool   `json:"reject,omitempty"`
	// Placeholder is the prompt shown in the free-text field a reject option
	// opens (e.g. "Tell Claude what to change"). Only meaningful when Reject.
	Placeholder string `json:"placeholder,omitempty"`
}

// Interaction is a pending request for the user, surfaced so a client can render
// it natively and submit a response. It is set while a session is
// StatusAwaitingInput and cleared when the session moves on.
type Interaction struct {
	Kind      InteractionKind `json:"kind"`
	Message   string          `json:"message,omitempty"`    // hook notification text
	ToolName  string          `json:"tool_name,omitempty"`  // tool awaiting permission, when known
	ToolInput string          `json:"tool_input,omitempty"` // pending tool input (permission detail)
	// Questions holds the AskUserQuestion question(s) (>=1 when Kind is question).
	Questions []QuestionSpec `json:"questions,omitempty"`
	Plan      string         `json:"plan,omitempty"` // ExitPlanMode plan text
	// Options, when set, are the server-built decision choices the client renders
	// verbatim (e.g. ExitPlanMode approve variants). The client sends the chosen
	// Value back unchanged; the node maps it to allow/deny + permission mode.
	Options []DecisionOption `json:"options,omitempty"`
}

// Summary is a cached transcript digest for list views. Computed node-side on
// hook events so clients never parse transcripts. All fields are comparable
// (tested with == against zero).
type Summary struct {
	Model        string  `json:"model,omitempty"`         // raw, e.g. "claude-opus-4-8"
	HasContext   bool    `json:"has_context,omitempty"`   // whether ContextPct is meaningful
	ContextPct   float64 `json:"context_pct,omitempty"`   // 0..100, latest turn
	Tokens       int     `json:"tokens,omitempty"`        // latest turn prompt-side token count
	Task         string  `json:"task,omitempty"`          // latest user prompt, first line
	LastActivity string  `json:"last_activity,omitempty"` // RFC3339 of the last chunk
}

// Session is argus's record for a single AI coding tool session.
type Session struct {
	// ID is argus's internal identifier (stable for the session's lifetime).
	ID string `json:"id"`
	// Tool is the adapter that owns this session (e.g. "claude-code").
	Tool string `json:"tool"`
	// ClaudeSessionID is the tool's own session id; empty until a hook fires.
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
	// Name is the tool's own session name (e.g. Claude's session name), when known.
	Name string `json:"name,omitempty"`

	Tmux TmuxLocation `json:"tmux"`

	// Cwd and TranscriptPath are populated from hook payloads.
	Cwd            string `json:"cwd,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`

	Status Status `json:"status"`
	// StatusLabel is the server-rendered display word for Status (set on emit).
	StatusLabel string `json:"status_label,omitempty"`
	Source      Source `json:"source"`

	// Frontend classifies the session's UI host (tmux/vscode/external).
	Frontend Frontend `json:"frontend,omitempty"`

	// Repo is the basename of the session directory's git repository, when known
	// (path-derived, not from the transcript).
	Repo string `json:"repo,omitempty"`

	// Summary is the cached transcript digest for list views (nil until computed).
	Summary *Summary `json:"summary,omitempty"`

	// Interaction is the pending user request (non-nil only while awaiting input).
	Interaction *Interaction `json:"interaction,omitempty"`

	// Origin fields — populated only when surfaced through a gateway.
	// NodeID is the originating node (also the composite ID prefix);
	// NodeLabel is its human-friendly name (e.g. hostname).
	NodeID    string `json:"node_id,omitempty"`
	NodeLabel string `json:"node_label,omitempty"`
	// Offline marks a session whose originating node is currently disconnected
	// from the gateway: kept visible (greyed) for a grace period before removal.
	Offline bool `json:"offline,omitempty"`
}

// Controllable reports whether argus can drive the session's terminal (send
// text/keys, capture, kill, focus). Only tmux-pane sessions are controllable;
// vscode/external sessions support decisions but not terminal control.
func (s Session) Controllable() bool { return s.Tmux.PaneID != "" }
