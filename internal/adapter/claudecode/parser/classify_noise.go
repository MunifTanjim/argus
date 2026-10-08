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

// hasUserContent reports whether content has real user text or images.
func hasUserContent(c msgContent, strContent string) bool {
	// JSON string content is already system-tag-checked, so non-empty means real.
	if c.isString {
		return strings.TrimSpace(strContent) != ""
	}
	for _, b := range c.blocks {
		if b.Type == "text" || b.Type == "image" {
			return true
		}
	}
	return false
}

// isUserNoise reports whether a user-type entry is droppable noise:
// hard-noise-tag wrapping or empty command output.
func isUserNoise(contentStr string) bool {
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

	return false
}

// isInterruptMarker reports whether a user entry is the interruption marker
// Claude Code writes when the user interrupts a turn: string content or an array
// with a single text block. trimmed is the space-trimmed string content.
func isInterruptMarker(c msgContent, trimmed string) bool {
	if strings.HasPrefix(trimmed, interruptedMarkerPrefix) {
		return true
	}
	return len(c.blocks) == 1 && c.blocks[0].Type == "text" && strings.HasPrefix(c.blocks[0].Text, interruptedMarkerPrefix)
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

// interruptedMarkerPrefix is the text Claude Code writes when the user interrupts
// a turn. It appears as string content or a single text block.
const interruptedMarkerPrefix = "[Request interrupted by user"
