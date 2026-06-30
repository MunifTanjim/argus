package parser

import (
	"encoding/json"
	"os"
)

// LinkSubagents connects discovered subagent processes to their parent Task
// tool calls, mutating processes in place and filling Description/SubagentType.
//
// Returns toolUseID->color, a fallback color source for Task items with no
// linked SubagentProcess (e.g. team agents whose JSONL lives outside subagents/).
//
// Matching runs in three phases: (1) result-based via toolUseResult.agentId,
// (2) team-member match of <teammate-message summary> to Task description,
// (3) positional pairing of remaining non-team procs/tasks by time order.
func LinkSubagents(processes []SubagentProcess, parentChunks []Chunk, parentSessionPath string) map[string]string {
	// Scan even with no processes: team agents lack subagent files but their
	// toolUseResult entries still carry color data.
	links := scanAgentLinks(parentSessionPath)

	if len(processes) == 0 {
		return links.toolIDToColor
	}

	// Collect Task tool DisplayItems from parent chunks.
	var taskItems []*DisplayItem
	for i := range parentChunks {
		c := &parentChunks[i]
		if c.Type != AIChunk {
			continue
		}
		for j := range c.Items {
			it := &c.Items[j]
			if it.Type != ItemSubagent {
				continue
			}
			taskItems = append(taskItems, it)
		}
	}

	if len(taskItems) == 0 {
		return links.toolIDToColor
	}

	toolIDToTask := make(map[string]*DisplayItem, len(taskItems))
	for _, it := range taskItems {
		toolIDToTask[it.ToolID] = it
	}

	matchedProcs := make(map[string]bool)
	matchedTools := make(map[string]bool)

	// Phase 1: result-based matching via toolUseResult.agentId.
	for i := range processes {
		toolID, ok := links.agentToToolID[processes[i].ID]
		if !ok {
			continue
		}
		it, ok := toolIDToTask[toolID]
		if !ok {
			continue
		}
		enrichProcess(&processes[i], it)
		matchedProcs[processes[i].ID] = true
		matchedTools[toolID] = true
	}

	// Phase 2: team members match by description -> teammate-message summary.
	// Their agent_id is "name@team_name" (not a file UUID), so Phase 1 misses them.
	teamTaskItems := filterTeamTasks(taskItems, matchedTools)
	if len(teamTaskItems) > 0 {
		for _, it := range teamTaskItems {
			var best *SubagentProcess
			for i := range processes {
				if matchedProcs[processes[i].ID] {
					continue
				}
				if processes[i].TeamSummary == "" || processes[i].TeamSummary != it.SubagentDesc {
					continue
				}
				if best == nil || processes[i].StartTime.Before(best.StartTime) {
					best = &processes[i]
				}
			}
			if best != nil {
				enrichProcess(best, it)
				matchedProcs[best.ID] = true
				matchedTools[it.ToolID] = true
			}
		}
	}

	// Phase 3: positional fallback for non-team tasks (no wrap-around). Team
	// Task calls are excluded — they either matched in Phase 2 or stay unmatched.
	var unmatchedProcs []*SubagentProcess
	for i := range processes {
		if !matchedProcs[processes[i].ID] {
			unmatchedProcs = append(unmatchedProcs, &processes[i])
		}
	}
	var unmatchedTasks []*DisplayItem
	for _, it := range taskItems {
		if !matchedTools[it.ToolID] && !IsTeamTask(it) {
			unmatchedTasks = append(unmatchedTasks, it)
		}
	}

	for i := 0; i < len(unmatchedProcs) && i < len(unmatchedTasks); i++ {
		enrichProcess(unmatchedProcs[i], unmatchedTasks[i])
	}

	// Fill TeamColor from toolUseResult for linked processes lacking one: a team
	// agent's own JSONL doesn't carry its color, but the parent's teammate_spawned
	// result does.
	for i := range processes {
		if processes[i].TeammateColor == "" && processes[i].ParentTaskID != "" {
			if color, ok := links.toolIDToColor[processes[i].ParentTaskID]; ok {
				processes[i].TeammateColor = color
			}
		}
	}

	// Remap team-worker IDs from hex UUID to "name@team" so ReconstructTeams
	// (which filters on the "@" separator) can see them on the task board.
	for i := range processes {
		if processes[i].ParentTaskID == "" {
			continue
		}
		it, ok := toolIDToTask[processes[i].ParentTaskID]
		if !ok || !IsTeamTask(it) {
			continue
		}
		fields := parseInputFields(it.ToolInput)
		teamName := getString(fields, "team_name")
		agentName := getString(fields, "name")
		if teamName != "" && agentName != "" {
			processes[i].ID = agentName + "@" + teamName
		}
	}

	return links.toolIDToColor
}

// agentLinkData holds the result of scanning a parent session for agent links.
type agentLinkData struct {
	agentToToolID map[string]string // agentId -> tool_use_id
	toolIDToColor map[string]string // tool_use_id -> team color name
}

// agentLinkFromEntry returns the link an entry contributes: toolUseResult.agentId
// paired with the spawning Task's tool_use_id (sourceToolUseID, falling back to
// the first tool_result block's id). ok is false when there's no agent link.
func agentLinkFromEntry(e Entry) (agentID, toolUseID string, ok bool) {
	resultMap := e.ToolUseResultMap()
	if resultMap == nil {
		return "", "", false
	}
	agentID = getString(resultMap, "agentId")
	if agentID == "" {
		agentID = getString(resultMap, "agent_id")
	}
	if agentID == "" {
		return "", "", false
	}
	toolUseID = e.SourceToolUseID
	if toolUseID == "" {
		toolUseID = extractFirstToolResultID(e)
	}
	if toolUseID == "" {
		return "", "", false
	}
	return agentID, toolUseID, true
}

// scanAgentLinks reads a parent session JSONL and builds agentId->toolUseID
// (Phase 1 linking) and toolUseID->color (TeamColor) maps. Color comes from
// teammate_spawned toolUseResult entries.
func scanAgentLinks(sessionPath string) agentLinkData {
	data := agentLinkData{
		agentToToolID: make(map[string]string),
		toolIDToColor: make(map[string]string),
	}

	f, err := os.Open(sessionPath)
	if err != nil {
		return data
	}
	defer f.Close()

	lr := newLineReader(f)

	for {
		line, ok := lr.next()
		if !ok {
			break
		}
		entry, ok := ParseEntry([]byte(line))
		if !ok {
			continue
		}
		agentID, toolUseID, ok := agentLinkFromEntry(entry)
		if !ok {
			continue
		}
		data.agentToToolID[agentID] = toolUseID
		if color := getString(entry.ToolUseResultMap(), "color"); color != "" {
			data.toolIDToColor[toolUseID] = color
		}
	}

	return data
}

// extractFirstToolResultID returns the tool_use_id from the first tool_result
// content block in the entry's message, or "" if none found.
func extractFirstToolResultID(entry Entry) string {
	var blocks []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
	}
	if err := json.Unmarshal(entry.Message.Content, &blocks); err != nil {
		return "" // content is a string, not an array — skip
	}
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID != "" {
			return b.ToolUseID
		}
	}
	return ""
}

// enrichProcess fills a SubagentProcess with metadata from its parent Task call.
func enrichProcess(proc *SubagentProcess, item *DisplayItem) {
	proc.ParentTaskID = item.ToolID
	proc.Description = item.SubagentDesc
	proc.SubagentType = item.SubagentType
}
