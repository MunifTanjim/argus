package pi

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// foldBranch converts the active branch of a session file into transcript
// entries. Consecutive assistant messages (with their tool results) form one
// turn; its footer closes on a user message, a compaction/branch boundary, or
// at the end of finished (history) reads.
//
// Tool results join their toolCall by toolCallId in a first pass, so results
// render inline on the call even when the file interleaves other entries.
func foldBranch(branch []*sessionEntry, finished bool) []transcript.Entry {
	results := map[string]*agentMessage{} // toolCallId -> result message
	for _, e := range branch {
		if e.Type != "message" {
			continue
		}
		var m agentMessage
		if json.Unmarshal(e.Message, &m) != nil {
			continue
		}
		if m.Role == "toolResult" && m.ToolCallID != "" {
			cp := m
			results[m.ToolCallID] = &cp
		}
	}

	var out []transcript.Entry
	var turn transcript.Turn
	turnOpen := false
	closeTurn := func() {
		if turnOpen {
			if f, ok := turn.Close(); ok {
				out = append(out, f)
			}
			turnOpen = false
		}
	}
	openTurn := func() {
		if !turnOpen {
			turn.Open(transcript.Entry{Kind: transcript.EntryTurnEnd})
			turnOpen = true
		}
	}

	for _, e := range branch {
		switch e.Type {
		case "compaction":
			closeTurn()
			out = append(out, transcript.Entry{
				ID:        e.ID,
				Kind:      transcript.EntryCompact,
				Timestamp: e.Timestamp,
				Summary:   e.Summary,
			})
		case "branch_summary":
			closeTurn()
			out = append(out, transcript.Entry{
				ID:        e.ID,
				Kind:      transcript.EntrySystem,
				Timestamp: e.Timestamp,
				Label:     "Branch summary",
				Detail:    e.Summary,
			})
		case "model_change":
			closeTurn()
			label := e.ModelID
			if e.Provider != "" {
				label = e.Provider + "/" + e.ModelID
			}
			out = append(out, transcript.Entry{
				ID:        e.ID,
				Kind:      transcript.EntrySystem,
				Timestamp: e.Timestamp,
				Label:     "Model: " + label,
			})
		case "thinking_level_change":
			closeTurn()
			out = append(out, transcript.Entry{
				ID:        e.ID,
				Kind:      transcript.EntrySystem,
				Timestamp: e.Timestamp,
				Label:     "Thinking: " + e.ThinkingLvl,
			})
		case "custom_message":
			if !e.Display {
				continue
			}
			closeTurn()
			out = append(out, transcript.Entry{
				ID:        e.ID,
				Kind:      transcript.EntryText,
				Timestamp: e.Timestamp,
				Text:      messageContentText(e.Content),
			})
		case "message":
			var m agentMessage
			if json.Unmarshal(e.Message, &m) != nil {
				continue
			}
			// Context edits replace message content for future context; the
			// raw history keeps the original. Fold the replacement.
			if ed := editFor(branch, e.ID); ed != nil && ed.HasRepl && string(ed.Replacement) != "null" {
				m.Content = ed.Replacement
			}
			switch m.Role {
			case "user":
				closeTurn()
				text := messageContentText(m.Content)
				if strings.TrimSpace(text) == "" {
					continue
				}
				out = append(out, transcript.Entry{
					ID:        e.ID,
					Kind:      transcript.EntryUser,
					Timestamp: firstNonEmpty(e.Timestamp, tsMillis(m.Timestamp)),
					Text:      text,
				})
			case "assistant":
				openTurn()
				f := turn.Footer
				f.ID = e.ID + ".end"
				f.ModelName = m.Model
				f.Timestamp = firstNonEmpty(e.Timestamp, tsMillis(m.Timestamp))
				addUsage(f, m.Usage)
				if m.StopReason == "error" || m.StopReason == "aborted" {
					f.Interrupted = m.StopReason == "aborted"
				}
				for _, en := range assistantEntries(e.ID, m, results) {
					turn.Count(en)
					out = append(out, en)
				}
				if m.ErrorMessage != "" {
					out = append(out, transcript.Entry{
						ID:        e.ID + ".error",
						Kind:      transcript.EntrySystem,
						Timestamp: f.Timestamp,
						Label:     firstLine(m.ErrorMessage),
						Detail:    m.ErrorMessage,
						IsError:   true,
					})
				}
			case "toolResult":
				// Joined inline with its toolCall; an orphaned result (call
				// pruned by compaction) still deserves a row.
				if _, ok := results[m.ToolCallID]; ok {
					continue
				}
				openTurn()
				out = append(out, transcript.Entry{
					ID:        e.ID,
					Kind:      transcript.EntryTool,
					Timestamp: firstNonEmpty(e.Timestamp, tsMillis(m.Timestamp)),
					ToolName:  m.ToolName,
					ToolID:    m.ToolCallID,
					Result:    messageContentText(m.Content),
				})
			case "bashExecution":
				closeTurn()
				label := m.Command
				if m.Cancelled {
					label += " (cancelled)"
				}
				out = append(out, transcript.Entry{
					ID:        e.ID,
					Kind:      transcript.EntryShell,
					Timestamp: firstNonEmpty(e.Timestamp, tsMillis(m.Timestamp)),
					Text:      label,
					Detail:    m.Output,
					IsError:   m.ExitCode != nil && *m.ExitCode != 0 && !m.Cancelled,
				})
			case "branchSummary":
				closeTurn()
				out = append(out, transcript.Entry{
					ID:        e.ID,
					Kind:      transcript.EntrySystem,
					Timestamp: firstNonEmpty(e.Timestamp, tsMillis(m.Timestamp)),
					Label:     "Branch summary",
					Detail:    m.Summary,
				})
			case "compactionSummary":
				closeTurn()
				out = append(out, transcript.Entry{
					ID:        e.ID,
					Kind:      transcript.EntryCompact,
					Timestamp: firstNonEmpty(e.Timestamp, tsMillis(m.Timestamp)),
					Summary:   m.Summary,
				})
			case "custom":
				if !m.Display {
					continue
				}
				closeTurn()
				out = append(out, transcript.Entry{
					ID:        e.ID,
					Kind:      transcript.EntryText,
					Timestamp: firstNonEmpty(e.Timestamp, tsMillis(m.Timestamp)),
					Text:      messageContentText(m.Content),
				})
			case "system":
				// Prompt sections / tool loadout: not conversation.
				continue
			}
		}
	}
	if finished {
		closeTurn()
	}
	return out
}

// assistantEntries expands one assistant message's content blocks. Tool calls
// carry their joined result when the branch still holds it.
func assistantEntries(entryID string, m agentMessage, results map[string]*agentMessage) []transcript.Entry {
	ts := tsMillis(m.Timestamp)
	var blocks []contentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		// String content (shouldn't happen for assistant) → single text row.
		if t := messageContentText(m.Content); strings.TrimSpace(t) != "" {
			return []transcript.Entry{{ID: entryID + ".0", Kind: transcript.EntryText, Timestamp: ts, Text: t}}
		}
		return nil
	}
	var out []transcript.Entry
	for j, b := range blocks {
		id := b.ID
		if id == "" {
			id = entryID + "." + strconv.Itoa(j)
		}
		switch b.Type {
		case "thinking":
			if b.Redacted && b.Thinking == "" {
				continue
			}
			out = append(out, transcript.Entry{ID: id, Kind: transcript.EntryThinking, Timestamp: ts, Text: b.Thinking})
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			out = append(out, transcript.Entry{ID: id, Kind: transcript.EntryText, Timestamp: ts, Text: b.Text})
		case "toolCall":
			e := transcript.Entry{ID: id, Kind: transcript.EntryTool, Timestamp: ts, ToolName: toolDisplayName(b.Namespace, b.Name), ToolID: b.ID}
			if len(b.Arguments) > 0 {
				e.ToolInput = string(b.Arguments)
				e.InputPreview = toolPreview(b.Name, b.Arguments)
			}
			if r, ok := results[b.ID]; ok {
				e.Result = messageContentText(r.Content)
				e.ResultIsError = r.IsError
			}
			out = append(out, e)
		}
	}
	return out
}

// addUsage accumulates one assistant response's usage into the turn footer.
// reasoning is already included in output; cacheWrite maps to CacheCreation.
func addUsage(turn *transcript.Entry, u *piUsage) {
	if u == nil {
		return
	}
	turn.Usage.Input += u.Input
	turn.Usage.Output += u.Output
	turn.Usage.CacheRead += u.CacheRead
	turn.Usage.CacheCreation += u.CacheWrite
}

// toolDisplayName renders a namespaced tool as "namespace.name".
func toolDisplayName(namespace, name string) string {
	if namespace != "" {
		return namespace + "." + name
	}
	return name
}

// toolPreview builds the one-line summary shown next to a tool name.
func toolPreview(name string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	switch name {
	case "read", "edit":
		return pick("path", "filePath")
	case "write":
		return pick("filePath", "path")
	case "bash":
		return firstLine(pick("command"))
	case "grep", "glob":
		return pick("pattern")
	case "find", "ls":
		return pick("path", "pattern")
	default:
		return ""
	}
}

// messageContentText renders string or block-array content as plain text.
// Images contribute nothing (transcript entries are text-only).
func messageContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			b.WriteString(bl.Text)
		case "thinking":
			b.WriteString(bl.Thinking)
		}
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// tsMillis converts a Unix millisecond timestamp to RFC3339 UTC, "" for zero.
func tsMillis(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
