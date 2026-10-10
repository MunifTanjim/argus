package codex

import "strings"

// multiAgentV2Namespace holds Codex's multi-agent v2 tools, which address agents
// by path instead of thread id.
const multiAgentV2Namespace = "collaboration"

// codexToolName is the name argus records for a Codex tool call. Default-
// namespace tools keep their bare name, as do multi-agent v1 tools (older
// rollouts record them bare), and MCP namespaces join the way Codex flattens
// them ("mcp__server__tool"). Any other namespace stays as a "namespace."
// prefix, so its tools cannot collide with another agent's tool of the same name.
func codexToolName(namespace, name string) string {
	switch {
	case namespace == "" || namespace == "functions" || namespace == "multi_agent_v1":
		return name
	case strings.HasPrefix(namespace, "mcp__"):
		if !strings.HasSuffix(namespace, "__") {
			namespace += "__"
		}
		return namespace + name
	}
	return namespace + "." + name
}

// baseToolName strips codexToolName's "namespace." prefix.
func baseToolName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}
