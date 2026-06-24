package parser

import (
	"os"
	"path/filepath"
	"strings"
)

// subagentsDir derives {sessionDir}/{sessionBase}/subagents from a session path.
func subagentsDir(sessionPath string) string {
	dir := filepath.Dir(sessionPath)
	base := strings.TrimSuffix(filepath.Base(sessionPath), ".jsonl")
	return filepath.Join(dir, base, "subagents")
}

// existingAgentIDs lists agent ids that have a non-empty file in the subagents
// directory. Directory listing only — no file parsing.
func existingAgentIDs(sessionPath string) map[string]bool {
	ids := map[string]bool{}
	entries, err := os.ReadDir(subagentsDir(sessionPath))
	if err != nil {
		return ids
	}
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if info, err := de.Info(); err != nil || info.Size() == 0 {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		if strings.HasPrefix(id, "acompact") {
			continue
		}
		ids[id] = true
	}
	return ids
}

// AgentRefsFromLinks inverts agentID->toolUseID links to toolUseID->agentID,
// keeping only agents whose subagent file currently exists under sessionPath
// (directory listing only, no file parsing).
func AgentRefsFromLinks(sessionPath string, links map[string]string) map[string]string {
	exist := existingAgentIDs(sessionPath)
	out := make(map[string]string, len(links))
	for agentID, toolID := range links {
		if exist[agentID] {
			out[toolID] = agentID
		}
	}
	return out
}

// AgentIDsByToolID maps a parent Task tool_use_id to its subagent agentID by
// scanning only the parent session file (via scanAgentLinks) and keeping links
// whose subagent file actually exists. No subagent file is parsed. Team agents
// (matched positionally/by description elsewhere) are not resolved here.
func AgentIDsByToolID(sessionPath string) map[string]string {
	return AgentRefsFromLinks(sessionPath, scanAgentLinks(sessionPath).agentToToolID)
}

// SubagentFilePath resolves the JSONL path for a subagent id and reports whether
// it exists.
func SubagentFilePath(sessionPath, agentID string) (string, bool) {
	p := filepath.Join(subagentsDir(sessionPath), "agent-"+agentID+".jsonl")
	if info, err := os.Stat(p); err != nil || info.IsDir() {
		return "", false
	}
	return p, true
}
