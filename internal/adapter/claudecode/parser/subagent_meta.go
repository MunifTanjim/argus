package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MaxSubagentDepth caps how deep subagent drilling is offered. A child item is
// drillable only when its depth is <= this value; equivalently, a subagent whose
// own spawnDepth >= MaxSubagentDepth has non-drillable children.
const MaxSubagentDepth = 5

// SpawnDepth reads the spawnDepth field from a subagent's agent-<id>.meta.json
// under rootPath's subagents dir. Returns 0 when the meta file is missing or
// unreadable (best-effort: treat as shallow so drilling is not hidden).
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
