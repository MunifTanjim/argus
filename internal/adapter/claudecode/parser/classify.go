package parser

import (
	"strings"
)

// Classify maps a raw Entry to one of the classified message types.
// Returns false for noise entries (filtered out) and sidechain messages.
func Classify(e Entry) (ClassifiedMsg, bool) {
	// Filter sidechain messages - we only care about main thread.
	if e.IsSidechain {
		return nil, false
	}

	ts := parseTimestamp(e.Timestamp)

	// 1. Hard noise: structural metadata types.
	if noiseEntryTypes[e.Type] {
		return nil, false
	}

	// Attachment entries (Claude Code 2.1+). Only nested_memory ("Loaded X")
	// surfaces to users; every other attachment subtype — hook responses,
	// skill listings, permission snapshots, mcp/tool deltas, output-style
	// banners — is infrastructure and drops here. Keeping the enumeration
	// tight preserves the "unknown → drop" invariant without widening
	// ClassifiedMsg once per internal event type.
	if e.Type == "attachment" {
		if e.Attachment.Type == "nested_memory" && e.Attachment.DisplayPath != "" {
			return MemoryLoadMsg{
				Timestamp:   ts,
				DisplayPath: e.Attachment.DisplayPath,
			}, true
		}
		return nil, false
	}

	// Summary entries become CompactMsg (context compression boundary).
	// The title lives in e.Summary, not message.content.
	if e.Type == "summary" {
		return CompactMsg{
			Timestamp: ts,
			Text:      e.Summary,
		}, true
	}

	// Hard noise: synthetic assistant messages.
	if e.Type == "assistant" && e.Message.Model == "<synthetic>" {
		return nil, false
	}

	// Get string content for user-type checks.
	contentStr := ExtractText(e.Message.Content)

	// Filter user-type noise (hard noise tags, empty output, interruptions).
	if e.Type == "user" && isUserNoise(e.Message.Content, contentStr) {
		return nil, false
	}

	// Teammate messages: classify as TeammateMsg.
	if e.Type == "user" {
		trimmed := strings.TrimSpace(contentStr)
		if teammateMessageRe.MatchString(trimmed) {
			innerContent := extractTeammateContent(trimmed)

			// Filter protocol messages (idle notifications, shutdown, task
			// assignments). These are JSON payloads from the team coordination
			// system, not human-readable agent output.
			if teammateProtocolRe.MatchString(innerContent) {
				return nil, false
			}

			teammateID := extractTeammateID(trimmed)
			color := extractTeammateColor(trimmed)
			text := SanitizeContent(innerContent)
			return TeammateMsg{
				Timestamp:  ts,
				Text:       text,
				TeammateID: teammateID,
				Color:      color,
			}, true
		}
	}

	// 2. System message: user entry starting with command output tag.
	if e.Type == "user" {
		trimmed := strings.TrimSpace(contentStr)
		if strings.HasPrefix(trimmed, localCommandStdoutTag) || strings.HasPrefix(trimmed, localCommandStderrTag) {
			return SystemMsg{
				Timestamp: ts,
				Output:    ExtractCommandOutput(contentStr),
			}, true
		}

		// Bash mode output (!bash inline execution).
		if strings.HasPrefix(trimmed, bashStdoutTag) || strings.HasPrefix(trimmed, bashStderrTag) {
			stderrContent := ""
			if m := reBashStderr.FindStringSubmatch(contentStr); m != nil {
				stderrContent = strings.TrimSpace(m[1])
			}
			return SystemMsg{
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

	// ToolSearch results: deferred tool loading responses.
	// These entries have text "Tool loaded." plus a toolUseResult with a
	// matches array listing which tools were loaded. Without this check they
	// appear as UserMsg("Tool loaded.") which starts a spurious user chunk.
	if e.Type == "user" && strings.TrimSpace(contentStr) == "Tool loaded." {
		if names := extractToolSearchMatches(e.ToolUseResult); len(names) > 0 {
			return SystemMsg{
				Timestamp: ts,
				Output:    "Loaded: " + strings.Join(names, ", "),
			}, true
		}
	}

	// 3. User message: type=user, not isMeta, has real content, not system output.
	if e.Type == "user" && !e.IsMeta {
		trimmed := strings.TrimSpace(contentStr)

		// Exclude messages starting with system output tags.
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
				Text:           SanitizeContent(contentStr),
				PermissionMode: e.PermissionMode,
			}, true
		}
	}

	// 4. AI message: everything else (assistant messages, internal user messages with tool results).
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

	// Fallback: remaining user messages -> AI message.
	// Covers both isMeta=true entries (slash commands etc.) and tool_result
	// entries where isMeta is null in the JSONL. extractMetaBlocks handles both:
	// if the content has tool_result blocks it extracts them; otherwise it returns
	// a text fallback that mergeAIBuffer silently ignores.
	blocks := extractMetaBlocks(e.Message.Content, contentStr)
	return AIMsg{
		Timestamp: ts,
		Text:      contentStr,
		IsMeta:    true,
		Blocks:    blocks,
	}, true
}
