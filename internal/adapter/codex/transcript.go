package codex

import (
	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// Transcript and history reads are stubbed in this first cut: Codex stores
// sessions as rollout-*.jsonl (a version-drifting RolloutLine/RolloutItem
// format) whose parser is deferred. Until then the live pane screen is the read
// path, and these return empty so the node/TUI degrade gracefully rather than
// error.

// ReadTranscriptView returns an empty transcript (parser not yet implemented).
func ReadTranscriptView(string) (transcript.TranscriptView, error) {
	return transcript.TranscriptView{}, nil
}

// ReadSubagentView reports no subagent trace.
func ReadSubagentView(string, string) (transcript.TranscriptView, bool, error) {
	return transcript.TranscriptView{}, false, nil
}

// FindToolDetail reports no tool detail.
func FindToolDetail(string, string, string) (transcript.ToolDetail, bool, error) {
	return transcript.ToolDetail{}, false, nil
}

// SubagentFilePath reports no subagent file.
func SubagentFilePath(string, string) (string, bool) { return "", false }

// streamingTranscript is a no-op live folder: Refresh always yields no chunks.
type streamingTranscript struct{}

func (streamingTranscript) Refresh() ([]transcript.Chunk, error) { return nil, nil }

// NewStreamingTranscript returns a no-op streaming transcript.
func NewStreamingTranscript(string, string, bool) adapter.StreamingTranscript {
	return streamingTranscript{}
}

// ListHistoryProjects reports no on-disk history.
func ListHistoryProjects() ([]session.HistoryProject, error) { return nil, nil }

// ListHistorySessions reports an empty page.
func ListHistorySessions(string, int, int) (session.HistorySessionPage, error) {
	return session.HistorySessionPage{}, nil
}

// ReadHistoryTranscript returns an empty transcript.
func ReadHistoryTranscript(string) (transcript.TranscriptView, error) {
	return transcript.TranscriptView{}, nil
}

// ReadHistorySubagentView reports no subagent trace.
func ReadHistorySubagentView(string, string) (transcript.TranscriptView, bool, error) {
	return transcript.TranscriptView{}, false, nil
}

// FindHistoryToolDetail reports no tool detail.
func FindHistoryToolDetail(string, string, string) (transcript.ToolDetail, bool, error) {
	return transcript.ToolDetail{}, false, nil
}
