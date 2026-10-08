package opencode

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// foldMessages treats a run of consecutive assistant messages as one turn. Its
// footer closes on an "idle" message (the service saw the run go idle), on a
// user message, or at the end when finished (history reads).
func foldMessages(msgs []ocMessage, finished bool) []transcript.Entry {
	var out []transcript.Entry
	var turn *transcript.Entry
	turnHasEntries := false
	for _, m := range msgs {
		switch m.Type {
		case "user":
			if turn != nil && turnHasEntries {
				out = append(out, *turn)
			}
			turn, turnHasEntries = nil, false
			out = append(out, transcript.Entry{
				ID:        m.ID,
				Kind:      transcript.EntryUser,
				Timestamp: tsMillis(m.Time.Created),
				Text:      m.Text,
			})
		case "idle":
			if turn != nil && turnHasEntries {
				if m.Time.Created != 0 {
					turn.Timestamp = tsMillis(m.Time.Created)
				}
				out = append(out, *turn)
			}
			turn, turnHasEntries = nil, false
		case "assistant":
			if turn == nil {
				turn = &transcript.Entry{Kind: transcript.EntryTurnEnd}
			}
			turn.ID = m.ID + ".end"
			turn.ModelName = m.Model.ID
			turn.Timestamp = tsMillis(m.Time.Created)
			es := assistantEntries(m, turn)
			turnHasEntries = turnHasEntries || len(es) > 0
			out = append(out, es...)
			if m.Error != nil {
				out = append(out, errorEntry(m))
			}
		}
	}
	if finished && turn != nil && turnHasEntries {
		out = append(out, *turn)
	}
	return out
}

func assistantEntries(m ocMessage, turn *transcript.Entry) []transcript.Entry {
	ts := tsMillis(m.Time.Created)
	var out []transcript.Entry
	for j, p := range m.Content {
		id := partID(m, j, p)
		switch p.Type {
		case "reasoning":
			out = append(out, transcript.Entry{ID: id, Kind: transcript.EntryThinking, Timestamp: ts, Text: p.Text})
			turn.Thinking++
		case "text":
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			out = append(out, transcript.Entry{ID: id, Kind: transcript.EntryText, Timestamp: ts, Text: p.Text})
		case "tool":
			e := transcript.Entry{ID: id, Kind: transcript.EntryTool, Timestamp: ts, ToolName: p.Name, ToolID: p.ID}
			if p.State != nil {
				e.ToolInput = string(p.State.Input)
				e.InputPreview = toolPreview(p.Name, p.State.Input)
				e.Result = toolContentText(p.State.Content)
				e.ResultIsError = p.State.Status == "error"
				if e.ResultIsError && p.State.Error != nil && p.State.Error.Message != "" {
					e.Result = p.State.Error.Message
				}
				if sub, ok := subagentRef(p); ok {
					e.Kind = transcript.EntrySubagent
					e.Subagents = []transcript.Subagent{sub}
				}
			}
			out = append(out, e)
			turn.ToolCount++
		}
	}
	return out
}

// errorEntry surfaces a failed assistant message (quota, rate limit, bad
// provider output). Such a message usually has no parts, so without it the
// failure would leave no trace.
func errorEntry(m ocMessage) transcript.Entry {
	detail := m.Error.Type
	if m.Error.Status != 0 {
		detail += " (" + strconv.Itoa(m.Error.Status) + ")"
	}
	return transcript.Entry{
		ID:        m.ID + ".error",
		Kind:      transcript.EntrySystem,
		Timestamp: tsMillis(m.Time.Created),
		Label:     firstLine(m.Error.Message),
		Detail:    detail + "\n" + m.Error.Message,
		IsError:   true,
	}
}

// partID falls back to the message id plus the part's position for reasoning
// and text parts, which OpenCode sends without an id. The position stays put as
// parts are appended.
func partID(m ocMessage, j int, p ocPart) string {
	if p.ID != "" {
		return p.ID
	}
	return m.ID + "." + strconv.Itoa(j)
}

// subagentRef turns a completed task/subagent tool part into a drillable subagent
// reference. OpenCode carries the child session id in state.metadata.sessionID; the
// child transcript is fetched by that id. ok is false for other tools, or when no
// child session was recorded (e.g. a spawn that errored before starting).
func subagentRef(p ocPart) (transcript.Subagent, bool) {
	if p.Name != "task" && p.Name != "subagent" {
		return transcript.Subagent{}, false
	}
	var meta struct {
		SessionID string `json:"sessionID"`
	}
	json.Unmarshal(p.State.Metadata, &meta)
	if meta.SessionID == "" {
		return transcript.Subagent{}, false
	}
	var in struct {
		Agent        string `json:"agent"`
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
	}
	json.Unmarshal(p.State.Input, &in)
	typ := in.Agent
	if typ == "" {
		typ = in.SubagentType
	}
	return transcript.Subagent{
		ID:       meta.SessionID,
		Type:     typ,
		Desc:     in.Description,
		Status:   p.State.Status,
		HasTrace: true,
	}, true
}

// toolPreview builds the one-line summary shown next to a tool name before it is
// expanded, picking the most identifying input field per tool. Empty for tools
// with no natural one-liner (the row then shows just the name).
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
		return pick("path", "filePath", "file_path")
	case "write":
		return pick("filePath", "path")
	case "bash", "shell":
		return firstLine(pick("command"))
	case "execute":
		return firstLine(pick("code"))
	case "grep", "glob":
		return pick("pattern")
	case "webfetch":
		return pick("url")
	case "websearch":
		return pick("query")
	case "skill":
		return pick("id")
	case "task", "subagent":
		return pick("description", "agent", "subagent_type")
	default:
		return ""
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func toolContentText(parts []ocToolContent) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// tsMillis converts a Unix millisecond timestamp to RFC3339 UTC, returning ""
// for the zero value.
func tsMillis(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
