package parser

import (
	"encoding/json"
	"strings"
	"time"
)

// DisplayItemType discriminates the display item categories.
type DisplayItemType int

const (
	ItemThinking DisplayItemType = iota
	ItemOutput
	ItemToolCall
	ItemSubagent        // Task tool spawned subagent
	ItemTeammateMessage // message from a teammate agent
	ItemMemoryLoad      // nested memory file loaded into context ("Loaded X")
)

// DisplayItem is a structured element within an AI chunk's detail view.
type DisplayItem struct {
	Type        DisplayItemType
	Text        string
	ToolName    string
	ToolID      string
	ToolInput   json.RawMessage
	ToolSummary string // "main.go" for Read, "go test" for Bash
	ToolResult  string
	ToolError   bool
	DurationMs  int64 // tool_use -> tool_result timestamp delta
	TokenCount  int   // estimated tokens: len(text)/4

	// Tool categorization
	ToolCategory ToolCategory // broad functional group (Read, Edit, Bash, etc.)

	// Subagent fields (ItemSubagent only)
	SubagentType   string // "Explore", "Plan", "general-purpose", etc.
	SubagentDesc   string // Task description
	TeamMemberName string // team member name from Task input (e.g. "file-counter")

	// Teammate fields (ItemTeammateMessage only)
	TeammateID    string
	TeammateColor string // team color name (e.g. "blue", "green")
}

// ChunkType discriminates the chunk categories.
type ChunkType int

const (
	UserChunk ChunkType = iota
	AIChunk
	SystemChunk
	CompactChunk // context compression boundary
)

// InferenceCycle is one LLM call plus the tool calls it dispatched. Tool
// results arrive as meta entries within the cycle's item range; the next
// non-meta assistant entry starts the next cycle.
//
// Cycles index into Chunk.Items via StartItem (inclusive) and EndItem
// (exclusive). The items themselves keep their existing flat ordering --
// this is a derived view, not a replacement structure.
type InferenceCycle struct {
	Index       int    // 0-based, per chunk
	StartItem   int    // inclusive index into Chunk.Items
	EndItem     int    // exclusive
	Model       string // model that produced this response
	Usage       Usage  // context-window snapshot for this call
	StopReason  string
	HasThinking bool
	ToolCount   int   // ItemToolCall + ItemSubagent in range
	DurationMs  int64 // wall time from this assistant entry to the next, or to chunk end
}

// Chunk is the output of the pipeline. Each chunk represents one visible unit
// in the conversation timeline.
type Chunk struct {
	Type      ChunkType
	Timestamp time.Time

	// User chunk fields.
	UserText       string
	ExpandedPrompt string // expanded skill/command prompt (from isMeta=true entry after /command)

	// AI chunk fields.
	Model         string
	Text          string
	ThinkingCount int
	ToolCalls     []ToolCall
	Items         []DisplayItem    // structured detail, nil until populated
	Cycles        []InferenceCycle // one per non-meta assistant entry; nil for non-AI chunks
	Usage         Usage
	StopReason    string
	DurationMs    int64 // first to last message timestamp in chunk

	// System chunk fields.
	Output  string
	IsError bool // bash stderr present or task killed
}

// BuildChunks folds classified messages into display chunks.
// The algorithm buffers consecutive AI messages and flushes them into a single
// AI chunk whenever a User or System message appears (or at end of input).
// TeammateMsg entries fold into the current AI buffer rather than starting new chunks.
func BuildChunks(msgs []ClassifiedMsg) []Chunk {
	var chunks []Chunk
	var aiBuf []AIMsg

	flush := func() {
		if len(aiBuf) == 0 {
			return
		}
		chunks = append(chunks, mergeAIBuffer(aiBuf))
		aiBuf = aiBuf[:0]
	}

	for i := 0; i < len(msgs); i++ {
		switch m := msgs[i].(type) {
		case UserMsg:
			flush()
			c := Chunk{
				Type:      UserChunk,
				Timestamp: m.Timestamp,
				UserText:  m.Text,
			}
			// Slash commands: the next entry may be the expanded skill prompt
			// (isMeta=true with text content, no tool_result blocks). Attach
			// it to this user chunk instead of letting it fall into the AI buffer.
			if strings.HasPrefix(m.Text, "/") && i+1 < len(msgs) {
				if expanded := extractExpandedPrompt(msgs[i+1]); expanded != "" {
					c.ExpandedPrompt = expanded
					i++ // consume the expanded prompt entry
				}
			}
			chunks = append(chunks, c)
		case SystemMsg:
			flush()
			chunks = append(chunks, Chunk{
				Type:      SystemChunk,
				Timestamp: m.Timestamp,
				Output:    m.Output,
				IsError:   m.IsError,
			})
		case AIMsg:
			aiBuf = append(aiBuf, m)
		case TeammateMsg:
			// Fold teammate messages into the AI buffer as synthetic AIMsg
			// with a "teammate" content block. This keeps them within the
			// AI turn rather than splitting it.
			aiBuf = append(aiBuf, AIMsg{
				Timestamp: m.Timestamp,
				IsMeta:    true,
				Blocks: []ContentBlock{{
					Type:          "teammate",
					Text:          m.Text,
					TeammateID:    m.TeammateID,
					TeammateColor: m.Color,
				}},
			})
		case MemoryLoadMsg:
			// Same fold pattern as TeammateMsg. Memory loads happen mid-turn
			// (after the user submits, before the assistant replies) and
			// belong with the surrounding AI turn, not as a standalone chunk.
			aiBuf = append(aiBuf, AIMsg{
				Timestamp: m.Timestamp,
				IsMeta:    true,
				Blocks: []ContentBlock{{
					Type:        "memory_load",
					DisplayPath: m.DisplayPath,
				}},
			})
		case CompactMsg:
			flush()
			chunks = append(chunks, Chunk{
				Type:      CompactChunk,
				Timestamp: m.Timestamp,
				Output:    m.Text,
			})
		}
	}
	flush()

	return chunks
}
