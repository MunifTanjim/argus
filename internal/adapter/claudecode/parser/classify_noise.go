package parser

import (
	"encoding/json"
	"strings"
)

// --- Hard noise detection ---

// noiseEntryTypes are entry types that never produce visible messages.
// Note: "summary" is handled separately as CompactMsg, not noise.
// "attachment" is handled by the dedicated branch in Classify: nested_memory
// surfaces as MemoryLoadMsg, everything else drops.
var noiseEntryTypes = map[string]bool{
	"system":                true,
	"file-history-snapshot": true,
	"queue-operation":       true,
	"progress":              true,
}

// hardNoiseTags are XML tags whose sole presence means the entire message is noise.
var hardNoiseTags = []string{
	"<local-command-caveat>",
	"<system-reminder>",
}

// systemOutputTags exclude a user message from being a "user chunk" starter.
var systemOutputTags = []string{
	localCommandStderrTag,
	localCommandStdoutTag,
	"<local-command-caveat>",
	"<system-reminder>",
	bashStdoutTag,
	bashStderrTag,
	taskNotificationTag,
}

var emptyStdout = "<local-command-stdout></local-command-stdout>"
var emptyStderr = "<local-command-stderr></local-command-stderr>"

// hasUserContent checks whether the raw content has real user text or images.
// String content is always considered real (already checked for system tags).
// Array content needs at least one text or image block.
func hasUserContent(raw json.RawMessage, strContent string) bool {
	// If ExtractText produced a non-empty string and raw is a JSON string, it's real.
	if len(raw) > 0 && raw[0] == '"' {
		return strings.TrimSpace(strContent) != ""
	}

	// Array content: check for text or image blocks.
	var blocks []textBlockJSON
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" || b.Type == "image" {
			return true
		}
	}
	return false
}

// isUserNoise returns true if a user-type entry is noise that should be dropped.
// Checks: hard noise tag wrapping, empty command output, interruption messages.
func isUserNoise(raw json.RawMessage, contentStr string) bool {
	trimmed := strings.TrimSpace(contentStr)

	// Wrapped entirely in a hard noise tag.
	for _, tag := range hardNoiseTags {
		closeTag := strings.Replace(tag, "<", "</", 1)
		if strings.HasPrefix(trimmed, tag) && strings.HasSuffix(trimmed, closeTag) {
			return true
		}
	}

	// Empty command output.
	if trimmed == emptyStdout || trimmed == emptyStderr {
		return true
	}

	// Interruption messages (string content or array with single text block).
	if strings.HasPrefix(trimmed, "[Request interrupted by user") {
		return true
	}
	return isArrayInterruption(raw)
}

// extractToolSearchMatches parses the toolUseResult field for ToolSearch
// responses, returning the list of loaded tool names. Returns nil if the
// field is absent or doesn't contain a matches array.
func extractToolSearchMatches(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var result struct {
		Matches []string `json:"matches"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil
	}
	return result.Matches
}

// isArrayInterruption checks if content is an array with a single text block
// starting with "[Request interrupted by user".
func isArrayInterruption(raw json.RawMessage) bool {
	var blocks []textBlockJSON
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return false
	}
	if len(blocks) == 1 && blocks[0].Type == "text" && strings.HasPrefix(blocks[0].Text, "[Request interrupted by user") {
		return true
	}
	return false
}
