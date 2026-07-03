package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MaxSubagentDepth caps how deep subagent drilling is offered: a child is
// drillable only when its depth is <= this value.
const MaxSubagentDepth = 5

// agentMeta is the agent-<id>.meta.json sidecar written at subagent spawn.
type agentMeta struct {
	SpawnDepth int    `json:"spawnDepth"`
	ToolUseID  string `json:"toolUseId"` // parent Task tool_use id
}

// readAgentMeta parses a subagent's agent-<id>.meta.json, returning the zero
// value when the file is missing/unreadable.
func readAgentMeta(path string) agentMeta {
	var m agentMeta
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m) // zero value on malformed JSON is fine
	}
	return m
}

// SpawnDepth reads spawnDepth from a subagent's agent-<id>.meta.json. Returns 0
// when the meta file is missing/unreadable, treating it as shallow so drilling stays visible.
func SpawnDepth(rootPath, agentID string) int {
	return readAgentMeta(filepath.Join(subagentsDir(rootPath), "agent-"+agentID+".meta.json")).SpawnDepth
}
