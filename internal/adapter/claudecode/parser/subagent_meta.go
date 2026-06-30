package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MaxSubagentDepth caps how deep subagent drilling is offered: a child is
// drillable only when its depth is <= this value.
const MaxSubagentDepth = 5

// SpawnDepth reads spawnDepth from a subagent's agent-<id>.meta.json. Returns 0
// when the meta file is missing/unreadable, treating it as shallow so drilling stays visible.
func SpawnDepth(rootPath, agentID string) int {
	metaPath := filepath.Join(subagentsDir(rootPath), "agent-"+agentID+".meta.json")
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return 0
	}
	var m struct {
		SpawnDepth int `json:"spawnDepth"`
	}
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	return m.SpawnDepth
}
