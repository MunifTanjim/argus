package opencode

import (
	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func readTranscriptView(string) (transcript.TranscriptView, error) {
	return transcript.TranscriptView{}, nil
}

func readSubagentView(string, string) (transcript.TranscriptView, bool, error) {
	return transcript.TranscriptView{}, false, nil
}

func findToolDetail(string, string, string) (transcript.ToolDetail, bool, error) {
	return transcript.ToolDetail{}, false, nil
}

func newStreamingTranscript(string, string, bool) adapter.StreamingTranscript {
	return &streamingTranscript{}
}

func subagentFilePath(string, string) (string, bool) { return "", false }

type streamingTranscript struct{}

func (s *streamingTranscript) Refresh() ([]transcript.Chunk, error) { return nil, nil }
