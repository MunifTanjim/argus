package parser

import (
	"strings"
)

// Classify maps a raw Entry to a classified message type.
// Returns false for noise and sidechain entries.
func Classify(e Entry) (ClassifiedMsg, bool) {
	// Sidechain = off main thread; we only show the main thread.
	if e.IsSidechain {
		return nil, false
	}

	ts := parseTimestamp(e.Timestamp)

	if noiseEntryTypes[e.Type] {
		return nil, false
	}

	// Only nested_memory ("Loaded X") surfaces; every other attachment subtype
	// is infrastructure. Enumerating tightly keeps the "unknown → drop" invariant.
	if e.Type == "attachment" {
		if e.Attachment.Type == "nested_memory" && e.Attachment.DisplayPath != "" {
			return MemoryLoadMsg{
				Timestamp:   ts,
				DisplayPath: e.Attachment.DisplayPath,
			}, true
		}
		return nil, false
	}

	// System side-events: only away_summary (the session recap) surfaces as a system
	// card; every other subtype is infrastructure. Mirrors the attachment handling.
	if e.Type == "system" {
		if e.Subtype == "away_summary" {
			if text := strings.TrimSpace(ExtractText(e.Content)); text != "" {
				return SystemMsg{Timestamp: ts, Output: text, Label: "Recap"}, true
			}
		}
		return nil, false
	}

	// Summary entries are compression boundaries; title is in e.Summary, not content.
	if e.Type == "summary" {
		return CompactMsg{
			Timestamp: ts,
			Text:      e.Summary,
		}, true
	}

	if e.Type == "assistant" && e.Message.Model == "<synthetic>" {
		return nil, false
	}

	contentStr := ExtractText(e.Message.Content)

	if e.Type == "user" && isUserNoise(e.Message.Content, contentStr) {
		return nil, false
	}

	if e.Type == "user" {
		trimmed := strings.TrimSpace(contentStr)
		if teammateMessageRe.MatchString(trimmed) {
			innerContent := extractTeammateContent(trimmed)
			teammateID := extractTeammateID(trimmed)
			color := extractTeammateColor(trimmed)

			// Team-coordination protocol JSON (idle/shutdown/assignments) is not
			// human-readable output. Surface idle_notification as a "went idle / done"
			// marker; drop the rest (shutdown handshakes, task assignments).
			if m := teammateProtocolRe.FindStringSubmatch(innerContent); m != nil {
				if m[1] == "idle_notification" {
					return TeammateMsg{
						Timestamp:  ts,
						TeammateID: teammateID,
						Color:      color,
						IsIdle:     true,
					}, true
				}
				return nil, false
			}

			text := SanitizeContent(innerContent)
			return TeammateMsg{
				Timestamp:  ts,
				Text:       text,
				TeammateID: teammateID,
				Color:      color,
			}, true
		}
	}

	// System message: user entry wrapping command output.
	if e.Type == "user" {
		trimmed := strings.TrimSpace(contentStr)
		if strings.HasPrefix(trimmed, localCommandStdoutTag) || strings.HasPrefix(trimmed, localCommandStderrTag) {
			return SystemMsg{
				Timestamp: ts,
				Output:    ExtractCommandOutput(contentStr),
			}, true
		}

		// Inline !bash mode input.
		if strings.HasPrefix(trimmed, bashInputTag) {
			cmd := ""
			if m := reBashInput.FindStringSubmatch(contentStr); m != nil {
				cmd = strings.TrimSpace(m[1])
			}
			return ShellMsg{Timestamp: ts, Command: cmd}, true
		}

		// Inline !bash mode output.
		if strings.HasPrefix(trimmed, bashStdoutTag) || strings.HasPrefix(trimmed, bashStderrTag) {
			stderrContent := ""
			if m := reBashStderr.FindStringSubmatch(contentStr); m != nil {
				stderrContent = strings.TrimSpace(m[1])
			}
			return ShellOutputMsg{
				Timestamp: ts,
				Output:    extractBashOutput(contentStr),
				IsError:   stderrContent != "",
			}, true
		}

		// Background task notifications.
		if strings.HasPrefix(trimmed, taskNotificationTag) {
			status := ""
			if m := reTaskNotifyStatus.FindStringSubmatch(contentStr); m != nil {
				status = strings.TrimSpace(m[1])
			}
			return SystemMsg{
				Timestamp: ts,
				Output:    extractTaskNotification(contentStr),
				IsError:   status == "killed",
			}, true
		}
	}

	// Deferred tool-load responses: "Tool loaded." + a matches array. Caught
	// here so they don't become a UserMsg that starts a spurious user chunk.
	if e.Type == "user" && strings.TrimSpace(contentStr) == "Tool loaded." {
		if names := extractToolSearchMatches(e.ToolUseResult); len(names) > 0 {
			return SystemMsg{
				Timestamp: ts,
				Output:    "Loaded: " + strings.Join(names, ", "),
			}, true
		}
	}

	// User message: type=user, not isMeta, real content, not system output.
	if e.Type == "user" && !e.IsMeta {
		trimmed := strings.TrimSpace(contentStr)

		excluded := false
		for _, tag := range systemOutputTags {
			if strings.HasPrefix(trimmed, tag) {
				excluded = true
				break
			}
		}

		if !excluded && hasUserContent(e.Message.Content, contentStr) {
			return UserMsg{
				Timestamp:      ts,
				UUID:           e.UUID,
				Text:           SanitizeContent(contentStr),
				PermissionMode: e.PermissionMode,
			}, true
		}
	}

	// AI message: assistant responses.
	if e.Type == "assistant" {
		thinking, toolCalls, blocks := extractAssistantDetails(e.Message.Content)
		stopReason := ""
		if e.Message.StopReason != nil {
			stopReason = *e.Message.StopReason
		}
		return AIMsg{
			Timestamp:     ts,
			Model:         e.Message.Model,
			Text:          SanitizeContent(ExtractText(e.Message.Content)),
			ThinkingCount: thinking,
			ToolCalls:     toolCalls,
			Blocks:        blocks,
			Usage: Usage{
				InputTokens:         e.Message.Usage.InputTokens,
				OutputTokens:        e.Message.Usage.OutputTokens,
				CacheReadTokens:     e.Message.Usage.CacheReadInputTokens,
				CacheCreationTokens: e.Message.Usage.CacheCreationInputTokens,
			},
			StopReason: stopReason,
		}, true
	}

	// Fallback for remaining user entries (isMeta slash commands, and
	// tool_result entries where isMeta is null). extractMetaBlocks returns
	// tool_result blocks if present, else a text fallback mergeAIBuffer ignores.
	blocks := extractMetaBlocks(e.Message.Content, contentStr)
	return AIMsg{
		Timestamp: ts,
		Text:      contentStr,
		IsMeta:    true,
		Blocks:    blocks,
	}, true
}
