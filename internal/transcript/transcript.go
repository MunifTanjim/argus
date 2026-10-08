// Package transcript defines argus's tool-agnostic display model for a coding
// session's conversation: a flat list of entries shipped over RPC and rendered
// by clients.
package transcript

import "encoding/json"

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

type EntryKind string

const (
	EntryUser     EntryKind = "user"
	EntryThinking EntryKind = "thinking"
	EntryText     EntryKind = "text"
	EntryTool     EntryKind = "tool"
	EntrySubagent EntryKind = "subagent" // a subagent op (spawn/wait/close), or a teammate message
	EntrySkill    EntryKind = "skill"    // a skill file loaded into context
	EntryTurnEnd  EntryKind = "turn_end" // footer of a finished turn
	EntrySystem   EntryKind = "system"   // runtime event / meta row
	EntryCompact  EntryKind = "compact"  // context-compression boundary
	EntryShell    EntryKind = "shell"    // user ran `!cmd` directly in the CLI
)

// Subagent is one referenced agent. A teammate is also modeled here with
// IsTeammate set; its message body (if any) is on the entry's Text.
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
	Trace      []Entry `json:"trace,omitempty"` // inline execution trace (history only)
}

// Entry is one visible unit of the conversation timeline. Fields are used per
// Kind.
type Entry struct {
	ID        string    `json:"id"` // stable across re-folds, for cursor preservation
	Kind      EntryKind `json:"kind"`
	Timestamp string    `json:"timestamp,omitempty"`

	// user, thinking, text, skill, teammate message; shell: the command run.
	Text      string `json:"text,omitempty"`
	Signature bool   `json:"signature,omitempty"` // thinking carried a signature

	// tool, skill, subagent.
	ToolName      string     `json:"toolName,omitempty"`
	ToolID        string     `json:"toolId,omitempty"` // tool_use id (subagent linking, detail fetch)
	ToolInput     string     `json:"toolInput,omitempty"`
	InputPreview  string     `json:"inputPreview,omitempty"`
	Result        string     `json:"result,omitempty"`
	ResultIsError bool       `json:"resultIsError,omitempty"`
	Subagents     []Subagent `json:"subagents,omitempty"`

	// turn_end.
	ModelName   string `json:"modelName,omitempty"`
	ModelColor  string `json:"modelColor,omitempty"` // hex like "#d3869b"; "" = uncolored
	Usage       Usage  `json:"usage,omitzero"`
	StopReason  string `json:"stopReason,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
	Thinking    int    `json:"thinking,omitempty"`  // thinking entries in the turn
	ToolCount   int    `json:"toolCount,omitempty"` // tool/skill/subagent-op entries in the turn
	Interrupted bool   `json:"interrupted,omitempty"`

	// turn_end: context-window evolution across the turn.
	HasContext         bool    `json:"hasContext,omitempty"`
	ContextPct         float64 `json:"contextPct,omitempty"`         // last cycle, 0..100
	ContextFirstPct    float64 `json:"contextFirstPct,omitempty"`    // first cycle, 0..100
	ContextDeltaTokens int     `json:"contextDeltaTokens,omitempty"` // growth, >= 0

	// system / compact / shell.
	Summary string `json:"summary,omitempty"` // compact: the compression title
	Label   string `json:"label,omitempty"`   // system: preview after the timestamp (e.g. "Recap")
	Detail  string `json:"detail,omitempty"`  // system/shell: detail text (shell: raw result scaffolding)
	IsError bool   `json:"isError,omitempty"` // system: error flag; shell: nonzero exit code
}

// MarshalJSON drops ToolInput/Result from the wire form; clients fetch them on demand.
func (e Entry) MarshalJSON() ([]byte, error) {
	type alias Entry // avoid recursing into MarshalJSON
	a := alias(e)
	a.ToolInput = "" // ,omitempty drops the now-empty fields
	a.Result = ""
	return json.Marshal(a)
}

// IsTeammate reports whether this entry is a teammate message rather than a spawn.
func (e Entry) IsTeammate() bool {
	return e.Kind == EntrySubagent && len(e.Subagents) == 1 && e.Subagents[0].IsTeammate
}

// IsToolCall reports whether the entry is a call the agent made. Teammate
// messages are peer chatter, not calls.
func (e Entry) IsToolCall() bool {
	switch e.Kind {
	case EntryTool, EntrySkill:
		return true
	case EntrySubagent:
		return !e.IsTeammate()
	}
	return false
}

// FindToolDetail returns the input and result of the tool call with toolID.
func FindToolDetail(entries []Entry, toolID string) (ToolDetail, bool) {
	for _, e := range entries {
		if e.IsToolCall() && e.ToolID == toolID {
			return ToolDetail{ToolInput: e.ToolInput, Result: e.Result, ResultIsError: e.ResultIsError}, true
		}
	}
	return ToolDetail{}, false
}

// Turn tracks the footer of the assistant turn being folded. A turn that adds
// no entries gets no footer.
type Turn struct {
	Footer     *Entry // nil when no turn is open
	hasEntries bool
}

// Open starts a new turn with footer f.
func (t *Turn) Open(f Entry) {
	t.Footer, t.hasEntries = &f, false
}

// Count records e as an entry of the open turn.
func (t *Turn) Count(e Entry) {
	switch {
	case e.Kind == EntryThinking:
		t.Footer.Thinking++
	case e.IsToolCall():
		t.Footer.ToolCount++
	}
	t.hasEntries = true
}

// Close ends the turn and returns its footer, if the turn added entries.
func (t *Turn) Close() (Entry, bool) {
	f, ok := t.Footer, t.hasEntries
	t.Footer, t.hasEntries = nil, false
	if f == nil || !ok {
		return Entry{}, false
	}
	return *f, true
}

type TranscriptView struct {
	Entries []Entry `json:"entries"`
}

// ToolDetail is one tool entry's heavy body (input + result), fetched on demand.
type ToolDetail struct {
	ToolInput     string
	Result        string
	ResultIsError bool
}
