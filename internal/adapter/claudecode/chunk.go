package claudecode

import (
	"encoding/json"
	"strings"
)

// The chunk model is argus's stable, display-ready view of a transcript: it is
// what the node ships over RPC and what the TUI renders. The actual JSONL
// parsing is delegated to the vendored `parser` package and mapped into these
// types by transcript.go, so this boundary stays fixed even if the parser changes.

// Usage is a per-call context-window snapshot. The Claude API reports
// input_tokens as the full prompt size per call, so Context() (input + cache) is
// the right per-turn context metric, not a sum across round trips.
type Usage struct {
	Input         int `json:"input,omitempty"`
	Output        int `json:"output,omitempty"`
	CacheRead     int `json:"cacheRead,omitempty"`
	CacheCreation int `json:"cacheCreation,omitempty"`
}

// Context returns the prompt-side token count (the context-window occupancy).
func (u Usage) Context() int { return u.Input + u.CacheRead + u.CacheCreation }

// Total returns all tokens accounted for in this call.
func (u Usage) Total() int { return u.Input + u.Output + u.CacheRead + u.CacheCreation }

// ChunkKind discriminates the chunk categories.
type ChunkKind string

const (
	ChunkUser    ChunkKind = "user"
	ChunkAI      ChunkKind = "ai"
	ChunkSystem  ChunkKind = "system"  // runtime event / meta row
	ChunkCompact ChunkKind = "compact" // context-compression boundary
)

// ItemKind discriminates the structured items inside an AI chunk.
type ItemKind string

const (
	ItemThinking ItemKind = "thinking"
	ItemText     ItemKind = "text" // assistant output text
	ItemTool     ItemKind = "tool"
	ItemSubagent ItemKind = "subagent" // Task/Agent tool that spawned a subagent
)

// Item is one structured element within an AI chunk.
type Item struct {
	ID   string   `json:"id"` // stable within a chunk
	Kind ItemKind `json:"kind"`

	// Text content (thinking / output).
	Text      string `json:"text,omitempty"`
	Signature bool   `json:"signature,omitempty"` // thinking carried a signature

	// Tool fields (ItemTool / ItemSubagent).
	ToolName      string `json:"toolName,omitempty"`
	ToolID        string `json:"toolId,omitempty"` // tool_use id (subagent linking)
	ToolInput     string `json:"toolInput,omitempty"`
	InputPreview  string `json:"inputPreview,omitempty"`
	Result        string `json:"result,omitempty"`
	ResultIsError bool   `json:"resultIsError,omitempty"`

	// Subagent fields (ItemSubagent only).
	SubagentType string  `json:"subagentType,omitempty"` // Explore, Plan, ...
	SubagentDesc string  `json:"subagentDesc,omitempty"`
	AgentID      string  `json:"agentId,omitempty"`  // linked subagent file id
	HasTrace     bool    `json:"hasTrace,omitempty"` // a subagent trace exists (drillable)
	Trace        []Chunk `json:"trace,omitempty"`    // subagent execution trace (inline; history only)
}

// MarshalJSON omits the heavy ToolInput and Result bodies from the wire form:
// transcript chunks ship with only the truncated InputPreview (and ToolID), and
// clients fetch a tool's full input/result on demand via the sessions.toolDetail
// RPC. The in-memory Item keeps both fields (node-side summary/lookup use them);
// only serialization drops them. Applies on every send path because all Item
// serialization — including inlined subagent traces — goes through here.
func (it Item) MarshalJSON() ([]byte, error) {
	type alias Item // avoid recursing into MarshalJSON
	a := alias(it)
	a.ToolInput = "" // ,omitempty drops the now-empty fields
	a.Result = ""
	return json.Marshal(a)
}

// Chunk is one visible unit in the conversation timeline.
type Chunk struct {
	ID        string    `json:"id"` // stable id for cursor preservation
	Kind      ChunkKind `json:"kind"`
	Timestamp string    `json:"timestamp,omitempty"`

	// User chunk.
	Text string `json:"text,omitempty"`

	// AI chunk.
	Model      string `json:"model,omitempty"`
	Items      []Item `json:"items,omitempty"`
	Thinking   int    `json:"thinking,omitempty"`  // thinking block count
	ToolCount  int    `json:"toolCount,omitempty"` // tool_use + subagent count
	Usage      Usage  `json:"usage,omitzero"`
	StopReason string `json:"stopReason,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`

	// Context-window evolution across this AI turn's cycles.
	HasContext         bool    `json:"hasContext,omitempty"`
	ContextPct         float64 `json:"contextPct,omitempty"`         // last cycle, 0..100
	ContextFirstPct    float64 `json:"contextFirstPct,omitempty"`    // first cycle, 0..100
	ContextDeltaTokens int     `json:"contextDeltaTokens,omitempty"` // growth, >= 0

	// System / compact chunk.
	Summary string `json:"summary,omitempty"`
	Detail  string `json:"detail,omitempty"`
	IsError bool   `json:"isError,omitempty"`
}

// TranscriptView is the node's display-ready transcript payload.
type TranscriptView struct {
	Chunks []Chunk `json:"chunks"`
}

// LastOutput returns the most meaningful trailing item of an AI chunk for a
// collapsed preview: the last text output, else the last tool call/result.
func (c Chunk) LastOutput() (Item, bool) {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if c.Items[i].Kind == ItemText && strings.TrimSpace(c.Items[i].Text) != "" {
			return c.Items[i], true
		}
	}
	for i := len(c.Items) - 1; i >= 0; i-- {
		if c.Items[i].Kind == ItemTool || c.Items[i].Kind == ItemSubagent {
			return c.Items[i], true
		}
	}
	return Item{}, false
}

// MarshalJSON stamps previewItemId — the id of the chunk's preview item per
// LastOutput — so clients render the collapsed preview from a server-chosen item
// instead of re-deriving it. Applies to every send path (list, stream, subagent
// traces) because all Chunk serialization goes through here.
func (c Chunk) MarshalJSON() ([]byte, error) {
	type alias Chunk // avoid recursing into MarshalJSON
	out := struct {
		alias
		PreviewItemID string `json:"previewItemId,omitempty"`
	}{alias: alias(c)}
	if it, ok := c.LastOutput(); ok {
		out.PreviewItemID = it.ID
	}
	return json.Marshal(out)
}
