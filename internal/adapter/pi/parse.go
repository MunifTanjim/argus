package pi

import (
	"cmp"
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
	msgs := make([]*agentMessage, len(branch))
	results := map[string]*agentMessage{} // toolCallId -> result message
	for i, e := range branch {
		if e.Type != "message" {
			continue
		}
		m := &agentMessage{}
		if json.Unmarshal(e.Message, m) != nil {
			continue
		}
		msgs[i] = m
		if m.Role == "toolResult" && m.ToolCallID != "" {
			results[m.ToolCallID] = m
		}
	}
	edits := latestEdits(branch)

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

	for i, e := range branch {
		switch e.Type {
		case "compaction":
			closeTurn()
			out = append(out, compactEntry(e.ID, e.Timestamp, e.Summary))
		case "branch_summary":
			closeTurn()
			out = append(out, branchSummaryEntry(e.ID, e.Timestamp, e.Summary))
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
			out = append(out, customEntry(e.ID, e.Timestamp, e.Content))
		case "message":
			if msgs[i] == nil {
				continue
			}
			m := *msgs[i]
			ts := cmp.Or(e.Timestamp, tsMillis(m.Timestamp))
			// Context edits replace message content for future context; the
			// raw history keeps the original. Fold the replacement.
			if ed, ok := edits[e.ID]; ok && ed.replaces() {
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
					Timestamp: ts,
					Text:      text,
				})
			case "assistant":
				openTurn()
				f := turn.Footer
				f.ID = e.ID + ".end"
				f.ModelName = m.Model
				f.Timestamp = ts
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
					Timestamp: ts,
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
					Timestamp: ts,
					Text:      label,
					Detail:    m.Output,
					IsError:   m.ExitCode != nil && *m.ExitCode != 0 && !m.Cancelled,
				})
			case "branchSummary":
				closeTurn()
				out = append(out, branchSummaryEntry(e.ID, ts, m.Summary))
			case "compactionSummary":
				closeTurn()
				out = append(out, compactEntry(e.ID, ts, m.Summary))
			case "custom":
				if !m.Display {
					continue
				}
				closeTurn()
				out = append(out, customEntry(e.ID, ts, m.Content))
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

// compactEntry, branchSummaryEntry and customEntry render what pi records both
// as a tree entry and as a message role.
func compactEntry(id, ts, summary string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntryCompact, Timestamp: ts, Summary: summary}
}

func branchSummaryEntry(id, ts, summary string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntrySystem, Timestamp: ts, Label: "Branch summary", Detail: summary}
}

func customEntry(id, ts string, content json.RawMessage) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntryText, Timestamp: ts, Text: messageContentText(content)}
}

// assistantEntries expands one assistant message's content blocks. Tool calls
// carry their joined result when the branch still holds it.
func assistantEntries(entryID string, m agentMessage, results map[string]*agentMessage) []transcript.Entry {
	ts := tsMillis(m.Timestamp)
	var blocks []contentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		// String content renders as a single text row.
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

// tsMillis converts a Unix millisecond timestamp to RFC3339 UTC, "" for zero.
func tsMillis(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
