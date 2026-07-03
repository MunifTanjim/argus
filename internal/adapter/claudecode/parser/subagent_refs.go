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

// existingAgentIDs lists agent ids with a non-empty file in the subagents dir
// (directory listing only, no file parsing).
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

// MetaAgentRefs maps tool_use_id -> agentID from the agent-<id>.meta.json
// sidecars under rootPath. Unlike AgentRefsFromLinks (which needs the parent's
// tool_result), these sidecars exist from spawn time, so still-running subagents
// are linked — the drill target is gated on a non-empty agent-<id>.jsonl so we
// never link to a file with nothing to show yet. cache (may be nil) memoizes the
// immutable toolUseId per agentID across calls; the .jsonl existence gate is
// re-evaluated every call since that file grows.
func MetaAgentRefs(rootPath string, cache map[string]string) map[string]string {
	if cache == nil {
		cache = map[string]string{}
	}
	dir := subagentsDir(rootPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]string{}
	}

	// One pass to find agentIDs with a non-empty .jsonl (drillable), reusing the
	// entries we already read rather than re-scanning via existingAgentIDs.
	hasContent := map[string]bool{}
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if info, err := de.Info(); err != nil || info.Size() == 0 {
			continue
		}
		hasContent[strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")] = true
	}

	out := map[string]string{}
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		agentID := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		if strings.HasPrefix(agentID, "acompact") || !hasContent[agentID] {
			continue
		}
		tid, ok := cache[agentID]
		if !ok {
			tid = readAgentMeta(filepath.Join(dir, name)).ToolUseID
			if tid != "" {
				cache[agentID] = tid // don't memoize a not-yet-written meta
			}
		}
		if tid != "" {
			out[tid] = agentID
		}
	}
	return out
}

// AgentRefsFromLinks inverts agentID->toolUseID links to toolUseID->agentID,
// keeping only agents whose subagent file currently exists under sessionPath.
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

// ChildAgentRefs maps tool_use_id -> child agentID for subagents spawned inside
// scanPath, keeping only children whose file exists under rootPath's flat
// subagents dir. Pass scanPath==rootPath for a session root; pass a subagent
// path with the session root to resolve nested children.
func ChildAgentRefs(scanPath, rootPath string) map[string]string {
	links := scanAgentLinks(scanPath).agentToToolID
	exist := existingAgentIDs(rootPath)
	out := make(map[string]string, len(links))
	for agentID, toolID := range links {
		if exist[agentID] {
			out[toolID] = agentID
		}
	}
	return out
}

// AgentIDFromPath extracts the subagent id from a .../agent-<id>.jsonl path.
func AgentIDFromPath(path string) string {
	name := filepath.Base(path)
	name = strings.TrimPrefix(name, "agent-")
	return strings.TrimSuffix(name, ".jsonl")
}

// AgentIDsByToolID maps a parent Task tool_use_id to its subagent agentID by
// scanning the given session/subagent file and keeping links whose subagent
// file exists under the same root. Equivalent to ChildAgentRefs(p, p).
func AgentIDsByToolID(sessionPath string) map[string]string {
	return ChildAgentRefs(sessionPath, sessionPath)
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
