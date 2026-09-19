package opencode

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func foldMessages(msgs []ocMessage) transcript.TranscriptView {
	view := transcript.TranscriptView{}
	for _, m := range msgs {
		switch m.Type {
		case "user":
			view.Chunks = append(view.Chunks, userChunk(m))
		case "assistant":
			view.Chunks = append(view.Chunks, assistantChunk(m))
		}
	}
	return view
}

func userChunk(m ocMessage) transcript.Chunk {
	return transcript.Chunk{
		ID:        m.ID,
		Kind:      transcript.ChunkUser,
		Timestamp: tsMillis(m.Time.Created),
		Text:      m.Text,
	}
}

func assistantChunk(m ocMessage) transcript.Chunk {
	c := transcript.Chunk{
		ID:        m.ID,
		Kind:      transcript.ChunkAI,
		Timestamp: tsMillis(m.Time.Created),
		ModelName: m.Model.ID,
	}
	for _, p := range m.Content {
		switch p.Type {
		case "reasoning":
			c.Items = append(c.Items, transcript.Item{ID: p.ID, Kind: transcript.ItemThinking, Text: p.Text})
			c.Thinking++
		case "text":
			c.Items = append(c.Items, transcript.Item{ID: p.ID, Kind: transcript.ItemText, Text: p.Text})
		case "tool":
			it := transcript.Item{ID: p.ID, Kind: transcript.ItemTool, ToolName: p.Name, ToolID: p.ID}
			if p.State != nil {
				it.ToolInput = string(p.State.Input)
				it.InputPreview = toolPreview(p.Name, p.State.Input)
				it.Result = toolContentText(p.State.Content)
				it.ResultIsError = p.State.Status == "error"
				if it.ResultIsError && p.State.Error != nil && p.State.Error.Message != "" {
					it.Result = p.State.Error.Message
				}
				if sub, ok := subagentRef(p); ok {
					it.Kind = transcript.ItemSubagent
					it.Subagents = []transcript.Subagent{sub}
				}
			}
			c.Items = append(c.Items, it)
			c.ToolCount++
		}
	}
	return c
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
