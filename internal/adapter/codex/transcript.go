package codex

import (
	"os"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	chunks, err := parseRollout(path)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	stampSubagents(chunks)
	return transcript.TranscriptView{Chunks: chunks}, nil
}

func stampSubagents(chunks []transcript.Chunk) {
	var edges map[string]string
	if p, err := stateDBPath(); err == nil {
		edges = loadSpawnEdges(p)
	}
	for i := range chunks {
		for j := range chunks[i].Items {
			it := &chunks[i].Items[j]
			if it.ToolName != "spawn_agent" || len(it.Subagents) == 0 {
				continue
			}
			sub := &it.Subagents[0]
			if sub.ID == "" {
				continue
			}
			sub.Status = edges[sub.ID]
			sub.HasTrace = findRolloutPath(sub.ID) != ""
		}
	}
}

func ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	path := findRolloutPath(agentID)
	if path == "" {
		return transcript.TranscriptView{}, false, nil
	}
	chunks, err := parseRollout(path)
	if err != nil {
		return transcript.TranscriptView{}, false, err
	}
	stampSubagents(chunks) // nested spawns link one level deeper
	return transcript.TranscriptView{Chunks: chunks}, true, nil
}

func FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	if agentID != "" {
		if p := findRolloutPath(agentID); p != "" {
			path = p
		} else {
			return transcript.ToolDetail{}, false, nil
		}
	}
	chunks, err := parseRollout(path)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	for _, c := range chunks {
		for _, it := range c.Items {
			if (it.Kind == transcript.ItemTool || it.Kind == transcript.ItemSubagent) && it.ToolID == toolID {
				return transcript.ToolDetail{ToolInput: it.ToolInput, Result: it.Result}, true, nil
			}
		}
	}
	return transcript.ToolDetail{}, false, nil
}

func SubagentFilePath(rootPath, agentID string) (string, bool) {
	if p := findRolloutPath(agentID); p != "" {
		return p, true
	}
	return "", false
}

type streamingTranscript struct {
	path   string
	size   int64
	chunks []transcript.Chunk
}

func (s *streamingTranscript) Refresh() ([]transcript.Chunk, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.chunks, nil // not written yet; return what we have
		}
		return nil, err
	}
	if fi.Size() == s.size && s.chunks != nil {
		return s.chunks, nil
	}
	view, err := ReadTranscriptView(s.path)
	if err != nil {
		return nil, err
	}
	s.size = fi.Size()
	s.chunks = view.Chunks
	return s.chunks, nil
}

func NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return &streamingTranscript{path: path}
}

func ListHistoryProjects() ([]session.HistoryProject, error) { return listHistoryProjects() }

func ListHistorySessions(cwd string, limit, offset int) (session.HistorySessionPage, error) {
	return listHistorySessions(cwd, limit, offset)
}

func ReadHistoryTranscript(path string) (transcript.TranscriptView, error) {
	clean, err := safeSessionsPath(path)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	return ReadTranscriptView(clean)
}

func ReadHistorySubagentView(string, string) (transcript.TranscriptView, bool, error) {
	return transcript.TranscriptView{}, false, nil
}

func FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	clean, err := safeSessionsPath(path)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	return FindToolDetail(clean, agentID, toolID)
}
