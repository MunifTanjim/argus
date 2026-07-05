package antigravity

import (
	"os"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	chunks, err := parseTranscript(path)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	stampChunkModel(chunks, convIDFromPath(path))
	return transcript.TranscriptView{Chunks: chunks}, nil
}

func stampChunkModel(chunks []transcript.Chunk, convID string) {
	name, color := conversationModel(convID)
	if name == "" {
		return
	}
	for i := range chunks {
		if chunks[i].Kind == transcript.ChunkAI && chunks[i].ModelName == "" {
			chunks[i].ModelName = name
			chunks[i].ModelColor = color
		}
	}
}

func SubagentFilePath(rootPath, agentID string) (string, bool) {
	return childTranscriptPath(agentID)
}

func ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	path, ok := childTranscriptPath(agentID)
	if !ok {
		return transcript.TranscriptView{}, false, nil
	}
	chunks, err := parseTranscript(path)
	if err != nil {
		return transcript.TranscriptView{}, false, err
	}
	stampChunkModel(chunks, agentID)
	return transcript.TranscriptView{Chunks: chunks}, true, nil
}

// FindToolDetail returns a tool/subagent item's full input and result.
// Empty agentID searches path itself.
func FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	if agentID != "" {
		p, ok := childTranscriptPath(agentID)
		if !ok {
			return transcript.ToolDetail{}, false, nil
		}
		path = p
	}
	chunks, err := parseTranscript(path)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	for _, c := range chunks {
		for _, it := range c.Items {
			if (it.Kind == transcript.ItemTool || it.Kind == transcript.ItemSubagent) && it.ToolID == toolID {
				return transcript.ToolDetail{ToolInput: it.ToolInput, Result: it.Result, ResultIsError: it.ResultIsError}, true, nil
			}
		}
	}
	return transcript.ToolDetail{}, false, nil
}

// streamingTranscript re-folds on any file size change, returning the full chunk list.
type streamingTranscript struct {
	path   string
	convID string
	size   int64
	chunks []transcript.Chunk
}

func (s *streamingTranscript) Refresh() ([]transcript.Chunk, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.chunks, nil
		}
		return nil, err
	}
	if fi.Size() == s.size && s.chunks != nil {
		return s.chunks, nil
	}
	chunks, err := parseTranscript(s.path)
	if err != nil {
		return nil, err
	}
	stampChunkModel(chunks, s.convID)
	s.size = fi.Size()
	s.chunks = chunks
	return s.chunks, nil
}

func NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return &streamingTranscript{path: path, convID: convIDFromPath(path)}
}

func ListHistoryProjects() ([]session.HistoryProject, error) { return nil, nil }

func ListHistorySessions(string, int, int) (session.HistorySessionPage, error) {
	return session.HistorySessionPage{}, nil
}

func ReadHistoryTranscript(string) (transcript.TranscriptView, error) {
	return transcript.TranscriptView{}, nil
}

func ReadHistorySubagentView(string, string) (transcript.TranscriptView, bool, error) {
	return transcript.TranscriptView{}, false, nil
}

func FindHistoryToolDetail(string, string, string) (transcript.ToolDetail, bool, error) {
	return transcript.ToolDetail{}, false, nil
}
