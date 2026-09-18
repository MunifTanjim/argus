package parser

// LastTurnInterrupted reports whether the folded transcript's final turn ended by
// a user interrupt or a tool-use rejection with nothing after it. An interrupt
// fires no Stop hook and its marker is folded into the AI chunk as a flag (never
// rendered), so live status and the subscription poll use this to read an
// interrupted turn as idle and surface the compose prompt.
func LastTurnInterrupted(chunks []Chunk) bool {
	if len(chunks) == 0 {
		return false
	}
	last := chunks[len(chunks)-1]
	if last.Type != AIChunk {
		return false
	}
	if last.Interrupted {
		return true
	}
	if n := len(last.Items); n > 0 {
		it := last.Items[n-1]
		if it.Type == ItemToolCall || it.Type == ItemSubagent {
			return it.ToolResult == toolUseRejectedMsg
		}
	}
	return false
}
