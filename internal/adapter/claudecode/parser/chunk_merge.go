package parser

import (
	"encoding/json"
	"strings"
	"time"
)

// concurrentTaskDurationThreshold: a non-Task tool exceeding this is assumed
// inflated by concurrent background Task agents. Claude Code delays writing
// tool_result entries until background agents finish, inflating wall-clock
// durations for tools that actually completed in seconds.
const concurrentTaskDurationThreshold int64 = 60_000 // 60 seconds

// extractExpandedPrompt returns the text of an expanded skill/command prompt
// (a meta AI message with only text blocks, no tool_result), else "".
func extractExpandedPrompt(msg ClassifiedMsg) string {
	ai, ok := msg.(AIMsg)
	if !ok || !ai.IsMeta || ai.Text == "" {
		return ""
	}
	for _, b := range ai.Blocks {
		if b.Type == "tool_result" {
			return ""
		}
	}
	return ai.Text
}

const skillBaseDirPrefix = "Base directory for this skill: "

func skillLoad(cmd, expanded string) (name, path, body string, ok bool) {
	if !strings.HasPrefix(expanded, skillBaseDirPrefix) {
		return "", "", "", false
	}
	line, rest, _ := strings.Cut(expanded, "\n")
	// cmd is "/name args"; the skill's identity is just the name — drop any argument.
	name, _, _ = strings.Cut(strings.TrimPrefix(cmd, "/"), " ")
	path = strings.TrimSpace(strings.TrimPrefix(line, skillBaseDirPrefix))
	body = strings.TrimSpace(rest)
	return name, path, body, true
}

func skillToolBody(text string) (string, bool) {
	if !strings.HasPrefix(text, skillBaseDirPrefix) {
		return "", false
	}
	_, rest, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(rest), true
}

type pendingTool struct {
	index     int       // index into the items slice
	timestamp time.Time // tool_use message timestamp
}

// mergeAIBuffer collapses consecutive AI messages into one AI chunk,
// populating both flat fields (backward compat) and structured Items.
func mergeAIBuffer(buf []AIMsg) Chunk {
	var (
		texts     []string
		thinking  int
		toolCalls []ToolCall
		model     string
		stop      string
	)

	// Structured items built from ContentBlocks.
	var items []DisplayItem
	pending := make(map[string]pendingTool) // ToolID -> pending info
	hasBlocks := false

	// Per-message item-start positions, recorded before appending blocks.
	// Used to derive InferenceCycle ranges below.
	itemStarts := make([]int, len(buf))

	for i, m := range buf {
		itemStarts[i] = len(items)
		// --- Flat field accumulation ---
		if m.Text != "" {
			texts = append(texts, m.Text)
		}
		thinking += m.ThinkingCount
		toolCalls = append(toolCalls, m.ToolCalls...)

		if model == "" && !m.IsMeta && m.Model != "" {
			model = m.Model
		}
		if !m.IsMeta && m.StopReason != "" {
			stop = m.StopReason
		}

		// --- Structured item building ---
		if len(m.Blocks) == 0 {
			continue
		}
		hasBlocks = true

		if !m.IsMeta {
			for _, b := range m.Blocks {
				switch b.Type {
				case "thinking":
					items = append(items, DisplayItem{
						Type:       ItemThinking,
						Text:       b.Text,
						TokenCount: len(b.Text) / 4,
					})
				case "text":
					items = append(items, DisplayItem{
						Type:       ItemOutput,
						Text:       b.Text,
						TokenCount: len(b.Text) / 4,
					})
				case "tool_use":
					inputLen := len(b.ToolInput)
					if b.ToolName == "Task" || b.ToolName == "Agent" {
						info := extractSubagentInfo(b.ToolInput)
						items = append(items, DisplayItem{
							Type:           ItemSubagent,
							ToolName:       b.ToolName,
							ToolID:         b.ToolID,
							ToolInput:      b.ToolInput,
							ToolSummary:    ToolSummary(b.ToolName, b.ToolInput),
							ToolCategory:   CategorizeToolName(b.ToolName),
							SubagentType:   info.Type,
							SubagentDesc:   info.Description,
							TeamMemberName: info.MemberName,
							TokenCount:     inputLen / 4,
						})
					} else {
						items = append(items, DisplayItem{
							Type:         ItemToolCall,
							ToolName:     b.ToolName,
							ToolID:       b.ToolID,
							ToolInput:    b.ToolInput,
							ToolSummary:  ToolSummary(b.ToolName, b.ToolInput),
							ToolCategory: CategorizeToolName(b.ToolName),
							TokenCount:   inputLen / 4,
						})
					}
					pending[b.ToolID] = pendingTool{
						index:     len(items) - 1,
						timestamp: m.Timestamp,
					}
				}
			}
		} else {
			// Meta messages carry tool_result/teammate/memory_load blocks.
			for _, b := range m.Blocks {
				switch b.Type {
				case "tool_result":
					if p, ok := pending[b.ToolID]; ok {
						items[p.index].ToolResult = b.Content
						items[p.index].ToolError = b.IsError
						if !p.timestamp.IsZero() && !m.Timestamp.IsZero() {
							items[p.index].DurationMs = m.Timestamp.Sub(p.timestamp).Milliseconds()
						}
						items[p.index].TokenCount += len(b.Content) / 4
						delete(pending, b.ToolID)
					} else {
						// Unmatched tool_result -> output item.
						items = append(items, DisplayItem{
							Type:       ItemOutput,
							Text:       b.Content,
							TokenCount: len(b.Content) / 4,
						})
					}
				case "teammate":
					items = append(items, DisplayItem{
						Type:          ItemTeammateMessage,
						Text:          b.Text,
						TeammateID:    b.TeammateID,
						TeammateColor: b.TeammateColor,
						TeammateIdle:  b.TeammateIdle,
						TokenCount:    len(b.Text) / 4,
					})
				case "memory_load":
					items = append(items, DisplayItem{
						Type: ItemMemoryLoad,
						Text: b.DisplayPath,
					})
				case "text":
					// Attach the skill file body to the most recent Skill item's result.
					if body, ok := skillToolBody(b.Text); ok {
						for j := len(items) - 1; j >= 0; j-- {
							if items[j].Type == ItemToolCall && items[j].ToolName == "Skill" {
								items[j].ToolResult = body
								items[j].TokenCount += len(body) / 4
								break
							}
						}
					}
				}
			}
		}
	}

	first := buf[0].Timestamp
	last := buf[len(buf)-1].Timestamp

	var dur int64
	if !first.IsZero() && !last.IsZero() {
		dur = last.Sub(first).Milliseconds()
	}

	ts := first
	if ts.IsZero() {
		ts = last
	}

	// Leave Items nil unless there were blocks to process.
	var finalItems []DisplayItem
	if hasBlocks {
		suppressInflatedDurations(items)
		finalItems = items
	}

	cycles := buildCycles(buf, itemStarts, items)

	// Usage snapshot: last non-meta message's usage. The Claude API reports
	// input_tokens as the full context window per call, so the last call is
	// the correct per-turn metric (not the sum across round trips).
	var usage Usage
	for i := len(buf) - 1; i >= 0; i-- {
		if !buf[i].IsMeta && buf[i].Usage.TotalTokens() > 0 {
			usage = buf[i].Usage
			break
		}
	}

	return Chunk{
		Type:          AIChunk,
		Timestamp:     ts,
		Model:         model,
		Text:          strings.Join(texts, "\n"),
		ThinkingCount: thinking,
		ToolCalls:     toolCalls,
		Items:         finalItems,
		Cycles:        cycles,
		Usage:         usage,
		StopReason:    stop,
		DurationMs:    dur,
	}
}

// buildCycles derives one InferenceCycle per non-meta AIMsg, each spanning
// from its message's item-start to the next non-meta message's (or len(items)).
// Returns nil for meta-only buffers (rare).
func buildCycles(buf []AIMsg, itemStarts []int, items []DisplayItem) []InferenceCycle {
	nonMeta := make([]int, 0, len(buf))
	for i, m := range buf {
		if !m.IsMeta {
			nonMeta = append(nonMeta, i)
		}
	}
	if len(nonMeta) == 0 {
		return nil
	}

	cycles := make([]InferenceCycle, len(nonMeta))
	lastTS := buf[len(buf)-1].Timestamp

	for i, msgIdx := range nonMeta {
		msg := buf[msgIdx]
		startItem := itemStarts[msgIdx]

		var endItem int
		var endTS time.Time
		if i+1 < len(nonMeta) {
			next := nonMeta[i+1]
			endItem = itemStarts[next]
			endTS = buf[next].Timestamp
		} else {
			endItem = len(items)
			endTS = lastTS
		}

		var dur int64
		if !msg.Timestamp.IsZero() && !endTS.IsZero() {
			dur = endTS.Sub(msg.Timestamp).Milliseconds()
		}

		toolCount := 0
		for j := startItem; j < endItem; j++ {
			if items[j].Type == ItemToolCall || items[j].Type == ItemSubagent {
				toolCount++
			}
		}

		cycles[i] = InferenceCycle{
			Index:       i,
			StartItem:   startItem,
			EndItem:     endItem,
			Model:       msg.Model,
			Usage:       msg.Usage,
			StopReason:  msg.StopReason,
			HasThinking: msg.ThinkingCount > 0,
			ToolCount:   toolCount,
			DurationMs:  dur,
		}
	}
	return cycles
}

// suppressInflatedDurations zeroes non-Task tool durations inflated by
// concurrent background Task agents (see concurrentTaskDurationThreshold).
// A 3-second git push can otherwise show as 11 minutes.
func suppressInflatedDurations(items []DisplayItem) {
	var maxTaskDur int64
	for i := range items {
		if items[i].Type == ItemSubagent && items[i].DurationMs > maxTaskDur {
			maxTaskDur = items[i].DurationMs
		}
	}
	if maxTaskDur < concurrentTaskDurationThreshold {
		return
	}

	// Zero non-subagent tools over the threshold; they likely waited on the
	// same background work.
	for i := range items {
		if items[i].Type == ItemSubagent || items[i].Type == ItemTeammateMessage {
			continue
		}
		if items[i].DurationMs > concurrentTaskDurationThreshold {
			items[i].DurationMs = 0
		}
	}
}

// subagentInfo holds metadata extracted from a Task tool_use input.
type subagentInfo struct {
	Type        string // "Explore", "Plan", "general-purpose", etc.
	Description string // Task description or truncated prompt
	MemberName  string // team member name (only for team Task calls)
}

// extractSubagentInfo extracts metadata from Task tool input JSON.
func extractSubagentInfo(input json.RawMessage) subagentInfo {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return subagentInfo{}
	}

	var info subagentInfo

	// Inner unmarshal errors are ignored: these are optional string fields and
	// "" is the right default when absent or non-string.
	if raw, ok := fields["subagent_type"]; ok {
		json.Unmarshal(raw, &info.Type)
	}
	// "description" first, then "prompt" as fallback.
	if raw, ok := fields["description"]; ok {
		json.Unmarshal(raw, &info.Description)
	}
	if info.Description == "" {
		if raw, ok := fields["prompt"]; ok {
			var prompt string
			json.Unmarshal(raw, &prompt)
			info.Description = Truncate(prompt, 80)
		}
	}
	// Skill tool uses "skill" for type and "args" for description.
	if info.Type == "" {
		if raw, ok := fields["skill"]; ok {
			json.Unmarshal(raw, &info.Type)
		}
	}
	if info.Description == "" {
		if raw, ok := fields["args"]; ok {
			var args string
			json.Unmarshal(raw, &args)
			info.Description = Truncate(args, 80)
		}
	}

	// Team member name (present when team_name + name are both set).
	if raw, ok := fields["name"]; ok {
		json.Unmarshal(raw, &info.MemberName)
	}
	return info
}
