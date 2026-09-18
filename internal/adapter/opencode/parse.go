package opencode

import (
	"time"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func foldMessages(items []ocMessageItem) transcript.TranscriptView {
	view := transcript.TranscriptView{}
	for _, item := range items {
		switch item.Info.Role {
		case "user":
			view.Chunks = append(view.Chunks, userChunk(item))
		case "assistant":
			view.Chunks = append(view.Chunks, assistantChunk(item))
		}
	}
	return view
}

func userChunk(item ocMessageItem) transcript.Chunk {
	c := transcript.Chunk{ID: item.Info.ID, Kind: transcript.ChunkUser, Timestamp: tsMillis(item.Info.Time.Created)}
	for _, p := range item.Parts {
		if p.Type == "text" {
			c.Text += p.Text
		}
	}
	return c
}

func assistantChunk(item ocMessageItem) transcript.Chunk {
	c := transcript.Chunk{
		ID:        item.Info.ID,
		Kind:      transcript.ChunkAI,
		Timestamp: tsMillis(item.Info.Time.Created),
		ModelName: item.Info.ModelID,
	}
	for _, p := range item.Parts {
		switch p.Type {
		case "reasoning":
			c.Items = append(c.Items, transcript.Item{ID: p.ID, Kind: transcript.ItemThinking, Text: p.Text})
			c.Thinking++
		case "text":
			c.Items = append(c.Items, transcript.Item{ID: p.ID, Kind: transcript.ItemText, Text: p.Text})
		case "tool":
			it := transcript.Item{ID: p.ID, Kind: transcript.ItemTool, ToolName: p.Tool, ToolID: p.CallID}
			if p.State != nil {
				it.ToolInput = string(p.State.Input)
				it.Result = p.State.Output
				it.ResultIsError = p.State.Status == "error"
			}
			c.Items = append(c.Items, it)
			c.ToolCount++
		case "subtask":
			c.Items = append(c.Items, transcript.Item{
				ID: p.ID, Kind: transcript.ItemSubagent, ToolName: p.Agent, Text: p.Description,
			})
		}
	}
	return c
}

// tsMillis converts a Unix millisecond timestamp to RFC3339 UTC, returning ""
// for the zero value.
func tsMillis(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
