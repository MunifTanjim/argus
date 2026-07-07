package parser

import (
	"encoding/json"
	"strings"
)

// --- Hard noise detection ---

// noiseEntryTypes are entry types that never produce visible messages.
// "summary" (CompactMsg), "attachment", and "system" (each a Classify branch with
// a surfaced subtype) are handled elsewhere.
var noiseEntryTypes = map[string]bool{
	"file-history-snapshot": true,
	"queue-operation":       true,
	"progress":              true,
	// Session metadata sidecars with no visible content; without dropping them
	// they render as an empty "(no output)" chunk.
	"last-prompt": true,
	"mode":        true,
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

// hasUserContent reports whether raw content has real user text or images.
func hasUserContent(raw json.RawMessage, strContent string) bool {
	// JSON string content is already system-tag-checked, so non-empty means real.
	if len(raw) > 0 && raw[0] == '"' {
		return strings.TrimSpace(strContent) != ""
	}

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

// isUserNoise reports whether a user-type entry is droppable noise:
// hard-noise-tag wrapping, empty command output, or interruption messages.
func isUserNoise(raw json.RawMessage, contentStr string) bool {
	trimmed := strings.TrimSpace(contentStr)

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

// extractToolSearchMatches returns the loaded tool names from a ToolSearch
// toolUseResult, or nil if absent.
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

// isArrayInterruption reports whether content is a single "[Request interrupted by user" text block.
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
