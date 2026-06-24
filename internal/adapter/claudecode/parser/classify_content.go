package parser

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// firstSubmatch returns the first capture group of re in s, or "" if no match.
func firstSubmatch(s string, re *regexp.Regexp) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// extractTeammateID extracts the teammate_id attribute from a teammate-message XML tag.
func extractTeammateID(s string) string { return firstSubmatch(s, teammateIDRe) }

// extractTeammateColor extracts the color attribute from a teammate-message XML tag.
func extractTeammateColor(s string) string { return firstSubmatch(s, teammateColorRe) }

// extractTeammateContent extracts the inner text content from a teammate-message XML wrapper.
func extractTeammateContent(s string) string {
	m := teammateContentRe.FindStringSubmatch(s)
	if m == nil {
		return s // fallback to full string if no match
	}
	return strings.TrimSpace(m[1])
}

// parseTimestamp parses an ISO 8601 timestamp. Returns zero time on failure.
func parseTimestamp(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// Try the format without timezone that Claude sometimes emits.
	if t, err := time.Parse("2006-01-02T15:04:05.999999999", s); err == nil {
		return t
	}
	return time.Time{}
}

// extractAssistantDetails pulls thinking count, tool calls, and structured
// content blocks from an assistant message's content array.
func extractAssistantDetails(raw json.RawMessage) (int, []ToolCall, []ContentBlock) {
	var blocks []contentBlockJSON
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return 0, nil, nil
	}

	thinking := 0
	var calls []ToolCall
	var contentBlocks []ContentBlock
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			thinking++
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: "thinking",
				Text: b.Thinking,
			})
		case "text":
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: "text",
				Text: b.Text,
			})
		case "tool_use":
			if b.ID != "" && b.Name != "" {
				calls = append(calls, ToolCall{ID: b.ID, Name: b.Name})
			}
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:      "tool_use",
				ToolID:    b.ID,
				ToolName:  b.Name,
				ToolInput: b.Input,
			})
		default:
			// Preserve unknown block types as-is.
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: b.Type,
				Text: b.Text,
			})
		}
	}
	return thinking, calls, contentBlocks
}

// extractMetaBlocks parses isMeta user content (tool results) into ContentBlocks.
// Falls back to a single text block if content isn't a JSON array of tool_result blocks.
func extractMetaBlocks(raw json.RawMessage, textFallback string) []ContentBlock {
	var blocks []contentBlockJSON
	if err := json.Unmarshal(raw, &blocks); err != nil {
		// String content or unparseable -> single text block.
		return []ContentBlock{{Type: "text", Text: textFallback}}
	}

	// Verify we got actual tool_result blocks, not some other array.
	hasToolResult := false
	for _, b := range blocks {
		if b.Type == "tool_result" {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		return []ContentBlock{{Type: "text", Text: textFallback}}
	}

	var contentBlocks []ContentBlock
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		content := stringifyContent(b.Content)
		contentBlocks = append(contentBlocks, ContentBlock{
			Type:    "tool_result",
			ToolID:  b.ToolUseID,
			Content: content,
			IsError: b.IsError,
		})
	}
	return contentBlocks
}

// stringifyContent converts tool_result content (string or array of text blocks) to a string.
func stringifyContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try array of text blocks.
	var blocks []textBlockJSON
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	// Last resort: raw JSON string.
	return string(raw)
}
