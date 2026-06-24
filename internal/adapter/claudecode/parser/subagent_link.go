package parser

import (
	"encoding/json"
	"os"
)

// LinkSubagents connects discovered subagent processes to their parent Task
// tool calls in the parent session. Mutates processes in place.
//
// Returns toolIDToColor: a map from tool_use_id to team color name, extracted
// from toolUseResult entries in the parent session. Callers use this as a
// fallback color source for Task items that have no linked SubagentProcess
// (e.g. team agents whose JSONL lives outside the subagents/ directory).
//
// Matching strategy (three phases):
//  1. Result-based: scan parent session entries for toolUseResult containing
//     agentId. Map agentId -> sourceToolUseID -> Task tool call.
//  2. Team member: match the <teammate-message summary="..."> attribute from
//     the subagent's first user message to the Task call's description.
//     Only applies to Task calls with both team_name and name in input.
//  3. Positional fallback: remaining unmatched non-team processes are paired
//     with remaining unmatched non-team Task calls by time order (no wrap-around).
//
// Also populates Description and SubagentType from the parent Task call.
func LinkSubagents(processes []SubagentProcess, parentChunks []Chunk, parentSessionPath string) map[string]string {
	// Always scan for colors, even without processes — team agents don't
	// create subagent files but their toolUseResult entries carry color data.
	links := scanAgentLinks(parentSessionPath)

	if len(processes) == 0 {
		return links.toolIDToColor
	}

	// Collect all Task tool DisplayItems from parent chunks.
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

	// Build tool_use_id -> DisplayItem for enrichment.
	toolIDToTask := make(map[string]*DisplayItem, len(taskItems))
	for _, it := range taskItems {
		toolIDToTask[it.ToolID] = it
	}

	matchedProcs := make(map[string]bool)
	matchedTools := make(map[string]bool)

	// Phase 1: Result-based matching via structured toolUseResult.agentId.
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

	// Phase 2: Team member matching by description -> teammate-message summary.
	// Team Task calls have both team_name and name in input. Their agent_id
	// is "name@team_name" (not a file UUID), so Phase 1 can't match them.
	// Match by comparing the Task call's description to the summary attribute
	// in the subagent's first <teammate-message> tag.
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

	// Phase 3: Positional fallback for non-team tasks (no wrap-around).
	// Explicitly excludes team Task calls — they either matched in Phase 2
	// or remain unmatched.
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

	// Populate TeamColor from toolUseResult data for any linked process
	// that doesn't already have a color. Team agents' own JSONL files
	// don't carry their color (the first entry is from team-lead), but
	// the teammate_spawned toolUseResult in the parent session does.
	for i := range processes {
		if processes[i].TeammateColor == "" && processes[i].ParentTaskID != "" {
			if color, ok := links.toolIDToColor[processes[i].ParentTaskID]; ok {
				processes[i].TeammateColor = color
			}
		}
	}

	// Remap IDs for team workers discovered via DiscoverSubagents (hex UUID)
	// to the "name@team" format that ReconstructTeams expects. Without this,
	// team workers in subagents/ are invisible to the team task board —
	// phases 2-4 of ReconstructTeams filter on splitWorkerID which requires
	// the "@" separator.
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

// agentLinkData holds the results of scanning a parent session for agent links.
type agentLinkData struct {
	agentToToolID map[string]string // agentId -> tool_use_id
	toolIDToColor map[string]string // tool_use_id -> team color name
}

// agentLinkFromEntry returns the subagent link an entry contributes: the
// toolUseResult.agentId (or agent_id) paired with the spawning Task call's
// tool_use_id (sourceToolUseID, falling back to the first tool_result block's
// tool_use_id). ok is false when the entry carries no agent link.
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

// scanAgentLinks reads a parent session JSONL file and builds maps from
// agentId -> toolUseID (for Phase 1 linking) and toolUseID -> color
// (for populating TeamColor after any linking phase).
//
// Matching strategy:
//
//	toolUseResult.agentId (or agent_id) -> sourceToolUseID
//
// Fallback when sourceToolUseID is missing: extract the first tool_result
// block's tool_use_id from the message content.
//
// Color extraction: teammate_spawned toolUseResult entries carry a color
// field. The tool_use_id links back to the spawning Task call.
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
		// Extract team color from teammate_spawned results.
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
