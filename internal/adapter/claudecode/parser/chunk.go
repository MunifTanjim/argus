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
	ShellChunk   // user ran `!cmd` directly in the CLI
	SkillChunk   // a skill file loaded into context
)

// InferenceCycle is one LLM call plus its dispatched tool calls. A derived
// view over Chunk.Items: [StartItem, EndItem). The next non-meta assistant
// entry starts the next cycle.
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

// Chunk is one visible unit in the conversation timeline.
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
	Output      string
	IsError     bool   // bash stderr present or task killed
	SystemLabel string // preview shown after the timestamp (e.g. "Recap"); empty for none

	// Shell chunk: the !cmd command.
	ShellCommand string
}

// BuildChunks folds classified messages into display chunks. Consecutive AI
// messages buffer into one chunk, flushed when a User/System message appears
// or at EOF. Teammate/memory entries fold into the AI buffer.
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
			// Skill loads emit a separate SkillChunk; other expanded prompts attach.
			if strings.HasPrefix(m.Text, "/") && i+1 < len(msgs) {
				expanded := extractExpandedPrompt(msgs[i+1])
				if name, path, body, ok := skillLoad(m.Text, expanded); ok {
					chunks = append(chunks, c)
					chunks = append(chunks, Chunk{
						Type:        SkillChunk,
						Timestamp:   m.Timestamp,
						UserText:    name,
						SystemLabel: path,
						Output:      body,
					})
					i++ // consume the expanded prompt entry
					continue
				}
				if expanded != "" {
					c.ExpandedPrompt = expanded
					i++ // consume the expanded prompt entry
				}
			}
			chunks = append(chunks, c)
		case SystemMsg:
			flush()
			chunks = append(chunks, Chunk{
				Type:        SystemChunk,
				Timestamp:   m.Timestamp,
				Output:      m.Output,
				IsError:     m.IsError,
				SystemLabel: m.Label,
			})
		case AIMsg:
			aiBuf = append(aiBuf, m)
		case TeammateMsg:
			// Fold into the AI buffer as a synthetic meta AIMsg so the teammate
			// message stays within the AI turn rather than splitting it.
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
			// Same fold pattern as TeammateMsg: memory loads happen mid-turn
			// and belong with the surrounding AI turn, not a standalone chunk.
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
		case ShellMsg:
			flush()
			sc := Chunk{
				Type:         ShellChunk,
				Timestamp:    m.Timestamp,
				ShellCommand: m.Command,
			}
			if i+1 < len(msgs) {
				if out, ok := msgs[i+1].(ShellOutputMsg); ok {
					sc.Output = out.Output
					sc.IsError = out.IsError
					i++
				}
			}
			chunks = append(chunks, sc)
		case ShellOutputMsg:
			// Orphan output (no preceding !cmd): surface rather than drop.
			flush()
			chunks = append(chunks, Chunk{
				Type:      ShellChunk,
				Timestamp: m.Timestamp,
				Output:    m.Output,
				IsError:   m.IsError,
			})
		}
	}
	flush()

	return chunks
}
