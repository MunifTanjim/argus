// Package transcript defines argus's tool-agnostic display model for a coding
// session's conversation: the chunk/item types shipped over RPC and rendered by
// clients.
package transcript

import (
	"encoding/json"
	"strings"
)

// Usage is a per-call context-window snapshot. The API reports input_tokens as
// the full prompt size per call, so Context() (input + cache) is the per-turn
// context metric, not a sum across round trips.
type Usage struct {
	Input         int `json:"input,omitempty"`
	Output        int `json:"output,omitempty"`
	CacheRead     int `json:"cacheRead,omitempty"`
	CacheCreation int `json:"cacheCreation,omitempty"`
}

func (u Usage) Context() int { return u.Input + u.CacheRead + u.CacheCreation }

func (u Usage) Total() int { return u.Input + u.Output + u.CacheRead + u.CacheCreation }

type ChunkKind string

const (
	ChunkUser    ChunkKind = "user"
	ChunkAI      ChunkKind = "ai"
	ChunkSystem  ChunkKind = "system"  // runtime event / meta row
	ChunkCompact ChunkKind = "compact" // context-compression boundary
	ChunkShell   ChunkKind = "shell"   // user ran `!cmd` directly in the CLI (codex)
	ChunkSkill   ChunkKind = "skill"   // a skill file was loaded into context (codex)
)

type ItemKind string

const (
	ItemThinking ItemKind = "thinking"
	ItemText     ItemKind = "text"
	ItemTool     ItemKind = "tool"
	ItemSubagent ItemKind = "subagent" // a subagent op: spawn (drillable), or wait/close on one
	ItemPrompt   ItemKind = "prompt"   // synthetic: injected TUI-side, never parser-emitted
)

// Subagent is one subagent an ItemSubagent references.
type Subagent struct {
	ID       string  `json:"id"`
	Name     string  `json:"name,omitempty"`   // codex per-spawn nickname (e.g. "Volta")
	Type     string  `json:"type,omitempty"`   // Explore, Plan, ... (claude); agent_type (codex)
	Desc     string  `json:"desc,omitempty"`   // task/spawn message
	Status   string  `json:"status,omitempty"` // codex: thread_spawn_edges status (running/closed)
	HasTrace bool    `json:"hasTrace,omitempty"`
	Trace    []Chunk `json:"trace,omitempty"` // inline execution trace (history only)
}

// Item is one structured element within an AI chunk.
type Item struct {
	ID   string   `json:"id"` // stable within a chunk
	Kind ItemKind `json:"kind"`

	Text      string `json:"text,omitempty"`
	Signature bool   `json:"signature,omitempty"` // thinking carried a signature

	ToolName      string `json:"toolName,omitempty"`
	ToolID        string `json:"toolId,omitempty"` // tool_use id (subagent linking)
	ToolInput     string `json:"toolInput,omitempty"`
	InputPreview  string `json:"inputPreview,omitempty"`
	Result        string `json:"result,omitempty"`
	ResultIsError bool   `json:"resultIsError,omitempty"`

	// Subagents lists the subagents this item references (ItemSubagent only).
	Subagents []Subagent `json:"subagents,omitempty"`
}

// MarshalJSON drops ToolInput/Result from the wire form; clients fetch them on demand.
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

	// User chunk. Also shell chunk: the command run. Also skill chunk: the skill
	// identifier (e.g. "superpowers:brainstorming").
	Text string `json:"text,omitempty"`

	// AI chunk.
	Model      string `json:"model,omitempty"`
	Items      []Item `json:"items,omitempty"`
	Thinking   int    `json:"thinking,omitempty"`
	ToolCount  int    `json:"toolCount,omitempty"` // tool_use + subagent count
	Usage      Usage  `json:"usage,omitzero"`
	StopReason string `json:"stopReason,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`

	// Context-window evolution across this AI turn's cycles.
	HasContext         bool    `json:"hasContext,omitempty"`
	ContextPct         float64 `json:"contextPct,omitempty"`         // last cycle, 0..100
	ContextFirstPct    float64 `json:"contextFirstPct,omitempty"`    // first cycle, 0..100
	ContextDeltaTokens int     `json:"contextDeltaTokens,omitempty"` // growth, >= 0

	// System / compact / shell / skill chunk.
	Summary string `json:"summary,omitempty"` // compact: the compression title
	Label   string `json:"label,omitempty"`   // system: preview after the timestamp (e.g. "Recap"); skill: source file path
	Detail  string `json:"detail,omitempty"`  // system/shell: detail text (shell: raw result scaffolding); skill: file body (markdown)
	IsError bool   `json:"isError,omitempty"` // system: error flag; shell: nonzero exit code
}

type TranscriptView struct {
	Chunks []Chunk `json:"chunks"`
}

// LastOutput returns the trailing item of an AI chunk for a collapsed preview:
// the last text output, else the last tool call/result.
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

// MarshalJSON stamps previewItemId so clients render the collapsed preview from a
// server-chosen item.
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

// ToolDetail is one tool item's heavy body (input + result), fetched on demand.
type ToolDetail struct {
	ToolInput     string
	Result        string
	ResultIsError bool
}
