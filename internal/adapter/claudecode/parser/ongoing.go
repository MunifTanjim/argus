package parser

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// OngoingStalenessThreshold: max idle time before a session is considered dead.
// Claude Code writes on every API response/tool call, so 2 min of silence means
// the process is gone.
const OngoingStalenessThreshold = 2 * time.Minute

// activityType classifies events for ongoing detection.
type activityType int

const (
	actTextOutput   activityType = iota // text response (ending event)
	actThinking                         // extended thinking (AI activity)
	actToolUse                          // tool invocation (AI activity)
	actToolResult                       // tool result (AI activity)
	actInterruption                     // user interruption (ending event)
	actExitPlanMode                     // ExitPlanMode tool call (ending event)
)

// activity tracks an event type and its position in the activity stream.
type activity struct {
	typ   activityType
	index int
}

// isEndingEvent returns true if this activity type terminates an ongoing session.
func (a activity) isEndingEvent() bool {
	return a.typ == actTextOutput || a.typ == actInterruption || a.typ == actExitPlanMode
}

// isAIActivity returns true if this activity type represents AI work in progress.
func (a activity) isAIActivity() bool {
	return a.typ == actThinking || a.typ == actToolUse || a.typ == actToolResult
}

// approvePattern matches approve: true in SendMessage shutdown_response input.
var approvePattern = regexp.MustCompile(`"approve"\s*:\s*true`)

// isShutdownApproval reports whether a tool_use is a SendMessage
// shutdown_response with approve: true.
func isShutdownApproval(toolName string, toolInput json.RawMessage) bool {
	if toolName != "SendMessage" {
		return false
	}
	var fields struct {
		Type    string `json:"type"`
		Approve *bool  `json:"approve"`
	}
	if err := json.Unmarshal(toolInput, &fields); err != nil {
		return approvePattern.Match(toolInput) // fallback for malformed JSON
	}
	return fields.Type == "shutdown_response" && fields.Approve != nil && *fields.Approve
}

// IsOngoing reports whether the session is still in progress. Ongoing if either
// (1) there's AI activity after the last ending event (text output, interruption,
// ExitPlanMode, shutdown approval), or (2) any agent/task call is still pending.
//
// Condition 2 catches team sessions where the parent emits text after partial
// agent results, which would otherwise mask still-running agents whose tool_use
// appeared earlier. With no ending event, ongoing if there's any AI activity.
// For old-style chunks lacking items, falls back to the last AI chunk's stop_reason.
func IsOngoing(chunks []Chunk) bool {
	if len(chunks) == 0 {
		return false
	}

	// Trailing user prompt: Claude is processing. Callers apply staleness
	// thresholds to catch dead sessions where Claude never responded.
	if chunks[len(chunks)-1].Type == UserChunk {
		return true
	}

	var activities []activity
	actIdx := 0
	hasItems := false

	// Shutdown-approval tool IDs, so their tool_results also count as ending events.
	shutdownToolIDs := make(map[string]bool)

	for _, chunk := range chunks {
		if chunk.Type != AIChunk {
			continue
		}

		if len(chunk.Items) == 0 {
			continue
		}
		hasItems = true

		for _, item := range chunk.Items {
			switch item.Type {
			case ItemThinking:
				activities = append(activities, activity{typ: actThinking, index: actIdx})
				actIdx++

			case ItemOutput:
				if strings.TrimSpace(item.Text) != "" {
					activities = append(activities, activity{typ: actTextOutput, index: actIdx})
					actIdx++
				}

			case ItemToolCall:
				if item.ToolName == "ExitPlanMode" {
					activities = append(activities, activity{typ: actExitPlanMode, index: actIdx})
					actIdx++
				} else if isShutdownApproval(item.ToolName, item.ToolInput) {
					shutdownToolIDs[item.ToolID] = true
					activities = append(activities, activity{typ: actInterruption, index: actIdx})
					actIdx++
				} else {
					activities = append(activities, activity{typ: actToolUse, index: actIdx})
					actIdx++
				}

				if item.ToolResult != "" {
					if shutdownToolIDs[item.ToolID] {
						activities = append(activities, activity{typ: actInterruption, index: actIdx})
					} else {
						activities = append(activities, activity{typ: actToolResult, index: actIdx})
					}
					actIdx++
				}

			case ItemSubagent:
				// Subagent spawns are AI activity (like tool_use).
				activities = append(activities, activity{typ: actToolUse, index: actIdx})
				actIdx++
				if item.ToolResult != "" {
					activities = append(activities, activity{typ: actToolResult, index: actIdx})
					actIdx++
				}
			}
		}
	}

	if hasItems {
		if isOngoingFromActivities(activities) {
			return true
		}
		// Activities look complete, but a pending agent/task call still means
		// ongoing (see condition 2 above). Only agent/task calls count — regular
		// tools can lack results after interruptions/compaction.
		return hasPendingAgents(chunks)
	}

	// Old-style fallback: ongoing if the last AI chunk has no end_turn stop reason.
	for i := len(chunks) - 1; i >= 0; i-- {
		if chunks[i].Type == AIChunk {
			return chunks[i].StopReason != "end_turn"
		}
	}

	return false
}

// hasPendingAgents reports whether any agent/task call still awaits a result.
// Only Task/Agent calls count — regular tools return within seconds, so their
// missing result signals interruption/incomplete JSONL, not ongoing work.
func hasPendingAgents(chunks []Chunk) bool {
	for _, chunk := range chunks {
		if chunk.Type != AIChunk {
			continue
		}
		for _, item := range chunk.Items {
			switch item.Type {
			case ItemSubagent:
				if item.ToolResult == "" {
					return true
				}
			case ItemToolCall:
				if (item.ToolName == "Task" || item.ToolName == "Agent") && item.ToolResult == "" {
					return true
				}
			}
		}
	}
	return false
}

// isOngoingFromActivities determines ongoing state from collected activities.
func isOngoingFromActivities(activities []activity) bool {
	if len(activities) == 0 {
		return false
	}

	lastEndingIdx := -1
	for i := len(activities) - 1; i >= 0; i-- {
		if activities[i].isEndingEvent() {
			lastEndingIdx = activities[i].index
			break
		}
	}

	// No ending event: ongoing if there's any AI activity at all.
	if lastEndingIdx == -1 {
		for _, a := range activities {
			if a.isAIActivity() {
				return true
			}
		}
		return false
	}

	// Check for AI activity AFTER the last ending event.
	for _, a := range activities {
		if a.index > lastEndingIdx && a.isAIActivity() {
			return true
		}
	}

	return false
}

// scanOngoingAssistant processes an assistant entry for ongoing detection.
func scanOngoingAssistant(e *metadataScanEntry, activityIndex *int,
	lastEndingIndex *int, hasAny, hasAfter *bool, shutdownIDs, pendingToolIDs map[string]bool) {

	var blocks []ongoingBlock
	if err := json.Unmarshal(e.Message.Content, &blocks); err != nil {
		return
	}

	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				*hasAny = true
				if *lastEndingIndex >= 0 {
					*hasAfter = true
				}
				*activityIndex++
			}
		case "tool_use":
			if b.ID == "" {
				continue
			}
			if b.Name == "ExitPlanMode" {
				*lastEndingIndex = *activityIndex
				*hasAfter = false
				*activityIndex++
			} else if isShutdownApproval(b.Name, b.Input) {
				shutdownIDs[b.ID] = true
				*lastEndingIndex = *activityIndex
				*hasAfter = false
				*activityIndex++
			} else {
				pendingToolIDs[b.ID] = true
				*hasAny = true
				if *lastEndingIndex >= 0 {
					*hasAfter = true
				}
				*activityIndex++
			}
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				*lastEndingIndex = *activityIndex
				*hasAfter = false
				*activityIndex++
			}
		}
	}
}

// scanOngoingUser processes a user entry for ongoing detection.
func scanOngoingUser(e *metadataScanEntry, activityIndex *int,
	lastEndingIndex *int, hasAny, hasAfter *bool, shutdownIDs, pendingToolIDs map[string]bool) {

	isRejection := isToolUseRejection(e.ToolResult)

	// String-content user entries (e.g. "[Request interrupted...]") fail array
	// unmarshal, so check them before block parsing.
	var text string
	if err := json.Unmarshal(e.Message.Content, &text); err == nil {
		if strings.HasPrefix(text, "[Request interrupted by user") {
			// Interruption clears all pending tool calls — the process was killed.
			for id := range pendingToolIDs {
				delete(pendingToolIDs, id)
			}
			*lastEndingIndex = *activityIndex
			*hasAfter = false
			*activityIndex++
		}
		return
	}

	var blocks []ongoingUserBlock
	if err := json.Unmarshal(e.Message.Content, &blocks); err != nil {
		return
	}

	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			if b.ToolUseID == "" {
				continue
			}
			delete(pendingToolIDs, b.ToolUseID)
			if shutdownIDs[b.ToolUseID] || isRejection {
				// Ending event.
				*lastEndingIndex = *activityIndex
				*hasAfter = false
				*activityIndex++
			} else {
				// Ongoing activity.
				*hasAny = true
				if *lastEndingIndex >= 0 {
					*hasAfter = true
				}
				*activityIndex++
			}
		case "text":
			if strings.HasPrefix(b.Text, "[Request interrupted by user") {
				// Interruption clears all pending tool calls.
				for id := range pendingToolIDs {
					delete(pendingToolIDs, id)
				}
				*lastEndingIndex = *activityIndex
				*hasAfter = false
				*activityIndex++
			}
		}
	}
}

// ongoingBlock is the minimal struct for parsing assistant content blocks
// during ongoing detection. Only captures fields needed for activity classification.
type ongoingBlock struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Input    json.RawMessage `json:"input"`
}

// ongoingUserBlock is the minimal struct for parsing user content blocks
// during ongoing detection.
type ongoingUserBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Text      string `json:"text"`
}

// toolUseRejectedMsg is the exact string Claude Code writes to toolUseResult
// when a user rejects a tool invocation.
const toolUseRejectedMsg = "User rejected tool use"

// isToolUseRejection checks if a raw toolUseResult value equals the rejection string.
func isToolUseRejection(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return s == toolUseRejectedMsg
}
