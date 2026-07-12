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
)

type ItemKind string

const (
	ItemThinking ItemKind = "thinking"
	ItemText     ItemKind = "text"
	ItemTool     ItemKind = "tool"
	ItemSubagent ItemKind = "subagent" // a subagent op: spawn (drillable), or wait/close on one
	ItemSkill    ItemKind = "skill"    // a skill file loaded into context via the Skill tool
	ItemPrompt   ItemKind = "prompt"   // synthetic: injected TUI-side, never parser-emitted
)

// Subagent is one referenced agent. A teammate is also modeled here with
// IsTeammate set; its message body (if any) is on the item's Text.
type Subagent struct {
	ID         string  `json:"id"`
	Name       string  `json:"name,omitempty"`   // codex per-spawn nickname (e.g. "Volta"); teammate id when IsTeammate
	Type       string  `json:"type,omitempty"`   // agent subtype (e.g. Explore, Plan)
	Desc       string  `json:"desc,omitempty"`   // task/spawn message
	Status     string  `json:"status,omitempty"` // running/closed
	Color      string  `json:"color,omitempty"`  // team color; teammate only
	IsTeammate bool    `json:"isTeammate,omitempty"`
	Idle       bool    `json:"idle,omitempty"` // teammate went idle / finished (IsTeammate only)
	HasTrace   bool    `json:"hasTrace,omitempty"`
	Trace      []Chunk `json:"trace,omitempty"` // inline execution trace (history only)
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

	// Subagents (ItemSubagent only).
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

	// User chunk. Also shell chunk: the command run.
	Text string `json:"text,omitempty"`

	// AI chunk.
	ModelName  string `json:"modelName,omitempty"`
	ModelColor string `json:"modelColor,omitempty"` // hex like "#d3869b"; "" = uncolored
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

	// System / compact / shell chunk.
	Summary string `json:"summary,omitempty"` // compact: the compression title
	Label   string `json:"label,omitempty"`   // system: preview after the timestamp (e.g. "Recap")
	Detail  string `json:"detail,omitempty"`  // system/shell: detail text (shell: raw result scaffolding)
	IsError bool   `json:"isError,omitempty"` // system: error flag; shell: nonzero exit code
}

type TranscriptView struct {
	Chunks []Chunk `json:"chunks"`
}

// IsTeammate reports whether this item is a teammate message rather than a spawn.
func (it Item) IsTeammate() bool {
	return it.Kind == ItemSubagent && len(it.Subagents) == 1 && it.Subagents[0].IsTeammate
}

// LastOutput returns the trailing item of an AI chunk for a collapsed preview:
// the last text output, else the last tool call/result. Teammate items are
// skipped — they're peer chatter, not this session's own output.
func (c Chunk) LastOutput() (Item, bool) {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if c.Items[i].Kind == ItemText && strings.TrimSpace(c.Items[i].Text) != "" {
			return c.Items[i], true
		}
	}
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.IsTeammate() {
			continue
		}
		if it.Kind == ItemTool || it.Kind == ItemSubagent || it.Kind == ItemSkill {
			return it, true
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
