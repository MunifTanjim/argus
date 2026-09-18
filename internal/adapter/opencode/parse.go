package opencode

import (
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
				it.Result = toolContentText(p.State.Content)
				it.ResultIsError = p.State.Status == "error"
			}
			c.Items = append(c.Items, it)
			c.ToolCount++
		}
	}
	return c
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
