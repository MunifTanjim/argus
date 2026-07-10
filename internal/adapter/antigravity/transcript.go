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
	stampChunkModelWith(chunks, name, color)
}

func stampChunkModelWith(chunks []transcript.Chunk, name, color string) {
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

// streamingTranscript tails a growing transcript: a byte cursor reads only newly
// appended lines, which accumulate and re-fold in memory each Refresh.
type streamingTranscript struct {
	path       string
	convID     string
	offset     int64
	loaded     bool
	lines      []line
	modelName  string
	modelColor string
	chunks     []transcript.Chunk
}

func (s *streamingTranscript) Refresh() ([]transcript.Chunk, error) {
	if fi, err := os.Stat(s.path); err == nil && fi.Size() < s.offset {
		s.offset = 0 // truncation/rotation: rebuild from the start
		s.loaded = false
		s.lines = nil
		s.chunks = nil
	}

	newLines, newOffset, err := scanTranscriptFrom(s.path, s.offset)
	if err != nil {
		return nil, err
	}
	if len(newLines) == 0 && s.loaded {
		return s.chunks, nil // nothing appended; skip re-fold and its DB work
	}
	s.loaded = true
	s.lines = append(s.lines, newLines...)
	s.offset = newOffset

	// Re-query while empty: the model may land in the DB mid-session.
	if s.modelName == "" {
		s.modelName, s.modelColor = conversationModel(s.convID)
	}
	chunks := foldTranscript(s.lines)
	stampChunkModelWith(chunks, s.modelName, s.modelColor)
	s.chunks = chunks
	return s.chunks, nil
}

func NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return &streamingTranscript{path: path, convID: convIDFromPath(path)}
}

func ListHistoryProjects() ([]session.HistoryProject, error) { return listHistoryProjects() }

func ListHistorySessions(cwd string, limit, offset int) (session.HistorySessionPage, error) {
	return listHistorySessions(cwd, limit, offset)
}

func ReadHistoryTranscript(path string) (transcript.TranscriptView, error) {
	clean, err := safeBrainPath(path)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	return ReadTranscriptView(clean)
}

func ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error) {
	return ReadSubagentView(path, agentID)
}

func FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	clean, err := safeBrainPath(path)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	return FindToolDetail(clean, agentID, toolID)
}
