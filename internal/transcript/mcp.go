package transcript

import "strings"

// MCPDisplayName renders an MCP tool name "mcp__<server>__<tool>" as
// "server › tool", splitting at the first "__" after the prefix; ok is false
// unless both parts are non-empty.
func MCPDisplayName(name string) (display string, ok bool) {
	rest, found := strings.CutPrefix(name, "mcp__")
	if !found {
		return "", false
	}
	server, tool, found := strings.Cut(rest, "__")
	if !found || server == "" || tool == "" {
		return "", false
	}
	return server + " › " + tool, true
}
