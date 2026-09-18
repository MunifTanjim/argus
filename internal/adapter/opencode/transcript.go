package opencode

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func serviceClient() (*client, bool) {
	info, ok := readServiceInfo()
	if !ok {
		return nil, false
	}
	return newClient(info), true
}

func readTranscriptView(sessionID string) (transcript.TranscriptView, error) {
	c, ok := serviceClient()
	if !ok {
		return transcript.TranscriptView{}, nil
	}
	items, err := c.readMessages(context.Background(), sessionID)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	return foldMessages(items), nil
}

func findToolDetail(sessionID, _, toolID string) (transcript.ToolDetail, bool, error) {
	view, err := readTranscriptView(sessionID)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	for _, ch := range view.Chunks {
		for _, it := range ch.Items {
			if it.Kind == transcript.ItemTool && it.ToolID == toolID {
				return transcript.ToolDetail{ToolInput: it.ToolInput, Result: it.Result, ResultIsError: it.ResultIsError}, true, nil
			}
		}
	}
	return transcript.ToolDetail{}, false, nil
}

func subagentFilePath(_, agentID string) (string, bool) { return agentID, agentID != "" }

func readSubagentView(_, agentID string) (transcript.TranscriptView, bool, error) {
	view, err := readTranscriptView(agentID)
	if err != nil {
		return transcript.TranscriptView{}, false, err
	}
	return view, len(view.Chunks) > 0, nil
}

type streamingTranscript struct {
	sessionID string
	chunks    []transcript.Chunk
}

func newStreamingTranscript(path, _ string, _ bool) adapter.StreamingTranscript {
	return &streamingTranscript{sessionID: path}
}

func (s *streamingTranscript) Refresh() ([]transcript.Chunk, error) {
	view, err := readTranscriptView(s.sessionID)
	if err != nil {
		return s.chunks, nil // transient; keep last good
	}
	if len(view.Chunks) == len(s.chunks) {
		return s.chunks, nil
	}
	s.chunks = view.Chunks
	return s.chunks, nil
}
