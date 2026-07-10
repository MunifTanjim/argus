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

// streamingTranscript tails a growing rollout: a byte cursor reads only newly
// appended lines, which accumulate and re-fold in memory each Refresh.
type streamingTranscript struct {
	path   string
	offset int64
	loaded bool
	lines  []rolloutLine
	models map[string]string
	chunks []transcript.Chunk
}

func (s *streamingTranscript) Refresh() ([]transcript.Chunk, error) {
	if fi, err := os.Stat(s.path); err == nil && fi.Size() < s.offset {
		s.offset = 0 // truncation/rotation: rebuild from the start
		s.loaded = false
		s.lines = nil
		s.chunks = nil
	}

	newLines, newOffset, err := scanRolloutFrom(s.path, s.offset)
	if err != nil {
		return nil, err
	}
	if len(newLines) == 0 && s.loaded {
		return s.chunks, nil // nothing appended; skip re-fold and its DB/glob work
	}
	s.loaded = true
	s.lines = append(s.lines, newLines...)
	s.offset = newOffset

	// Re-load while empty: the cache may be written mid-session.
	if s.models == nil {
		s.models = loadModelNames()
	}
	chunks := foldRollout(s.lines, s.models)
	stampSubagents(chunks)
	s.chunks = chunks
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
