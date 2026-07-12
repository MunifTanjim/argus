package parser

import (
	"encoding/json"
	"time"
)

// ClassifiedMsg is a sealed interface representing the message categories
// that survive noise filtering. Noise entries are dropped, not classified.
type ClassifiedMsg interface {
	classifiedMsg()
}

// UserMsg represents genuine user input that starts a new request cycle.
type UserMsg struct {
	Timestamp      time.Time
	UUID           string // raw entry uuid; stable id for a skill item
	Text           string // sanitized display text
	PermissionMode string // "default", "acceptEdits", "bypassPermissions", "plan"; empty if not present
}

func (UserMsg) classifiedMsg() {}

// ContentBlock represents a single content block from an assistant or tool result message.
type ContentBlock struct {
	Type          string          // "thinking", "text", "tool_use", "tool_result", "teammate", "memory_load"
	Text          string          // thinking or text content
	ToolID        string          // tool_use: call ID; tool_result: tool_use_id
	ToolName      string          // tool_use only
	ToolInput     json.RawMessage // tool_use only
	Content       string          // tool_result content (stringified)
	IsError       bool            // tool_result only
	TeammateID    string          // teammate only
	TeammateColor string          // teammate only: team color name
	TeammateIdle  bool            // teammate only: idle_notification ("went idle / done")
	DisplayPath   string          // memory_load only: path shown in the "Loaded X" pill
}

// AIMsg represents assistant responses and internal flow messages (tool results).
type AIMsg struct {
	Timestamp     time.Time
	Model         string
	Text          string // sanitized text content
	ThinkingCount int    // count of thinking blocks
	ToolCalls     []ToolCall
	Blocks        []ContentBlock // ordered content blocks, nil until populated
	Usage         Usage
	StopReason    string
	IsMeta        bool // internal user message (tool results)
}

func (AIMsg) classifiedMsg() {}

// ToolCall is a tool invocation extracted from an assistant message.
type ToolCall struct {
	ID   string
	Name string
}

// Usage holds token counts for a single API response.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
}

// TotalTokens returns the sum of all token fields.
func (u Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

// SystemMsg represents command output (slash command results, bash mode, task notifications).
type SystemMsg struct {
	Timestamp time.Time
	Output    string // extracted from stdout/stderr/notification tags
	IsError   bool   // true when stderr is non-empty or task was killed
	Label     string // preview shown after the timestamp (e.g. "Recap"); empty for none
}

func (SystemMsg) classifiedMsg() {}

type ShellMsg struct {
	Timestamp time.Time
	Command   string
}

func (ShellMsg) classifiedMsg() {}

type ShellOutputMsg struct {
	Timestamp time.Time
	Output    string
	IsError   bool // stderr was non-empty
}

func (ShellOutputMsg) classifiedMsg() {}

// TeammateMsg is folded into the AI turn during chunk building rather than
// starting a new user chunk.
type TeammateMsg struct {
	Timestamp  time.Time
	Text       string // sanitized inner content
	TeammateID string
	Color      string // team color name (e.g. "blue", "green")
	IsIdle     bool   // idle_notification: the teammate went idle / finished ("<id> is done")
}

func (TeammateMsg) classifiedMsg() {}

// CompactMsg represents a context compression boundary (summary entries).
// Displayed as a visual divider in the conversation timeline.
type CompactMsg struct {
	Timestamp time.Time
	Text      string
}

func (CompactMsg) classifiedMsg() {}

// MemoryLoadMsg represents a nested memory file (e.g. a CLAUDE.md) loaded into
// context. Rendered as a "Loaded <path>" pill folded into the AI turn, like TeammateMsg.
type MemoryLoadMsg struct {
	Timestamp   time.Time
	DisplayPath string // relative path shown to the user ("claude-code/CLAUDE.md")
}

func (MemoryLoadMsg) classifiedMsg() {}
