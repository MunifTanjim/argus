package codex

import (
	"os"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	return readTranscriptView(path, false)
}

func readTranscriptView(path string, finished bool) (transcript.TranscriptView, error) {
	entries, err := parseRollout(path, finished)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	stampSubagents(entries, sessionsRootFrom(path))
	return transcript.TranscriptView{Entries: entries}, nil
}

func stampSubagents(entries []transcript.Entry, sessionsRoot string) {
	var edges map[string]string
	if p, err := stateDBPath(); err == nil {
		edges = loadSpawnEdges(p)
	}
	for i := range entries {
		e := &entries[i]
		if e.ToolName != "spawn_agent" || len(e.Subagents) == 0 {
			continue
		}
		sub := &e.Subagents[0]
		if sub.ID == "" {
			continue
		}
		sub.Status = edges[sub.ID]
		sub.HasTrace = findRolloutPathIn(sessionsRoot, sub.ID) != ""
	}
}

func ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	sessionsRoot := sessionsRootFrom(rootPath)
	path := findRolloutPathIn(sessionsRoot, agentID)
	if path == "" {
		return transcript.TranscriptView{}, false, nil
	}
	entries, err := parseRollout(path, false)
	if err != nil {
		return transcript.TranscriptView{}, false, err
	}
	stampSubagents(entries, sessionsRoot) // nested spawns link one level deeper
	return transcript.TranscriptView{Entries: entries}, true, nil
}

func FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	if agentID != "" {
		if p := findRolloutPathIn(sessionsRootFrom(path), agentID); p != "" {
			path = p
		} else {
			return transcript.ToolDetail{}, false, nil
		}
	}
	entries, err := parseRollout(path, false)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	d, ok := transcript.FindToolDetail(entries, toolID)
	return d, ok, nil
}

func SubagentFilePath(rootPath, agentID string) (string, bool) {
	if p := findRolloutPathIn(sessionsRootFrom(rootPath), agentID); p != "" {
		return p, true
	}
	return "", false
}

// streamingTranscript tails a growing rollout: a byte cursor reads only newly
// appended lines, which accumulate and re-fold in memory each Refresh.
type streamingTranscript struct {
	path         string
	sessionsRoot string
	offset       int64
	loaded       bool
	lines        []rolloutLine
	models       map[string]string
	entries      []transcript.Entry
}

func (s *streamingTranscript) Refresh() ([]transcript.Entry, error) {
	if fi, err := os.Stat(s.path); err == nil && fi.Size() < s.offset {
		s.offset = 0 // truncation/rotation: rebuild from the start
		s.loaded = false
		s.lines = nil
		s.entries = nil
	}

	newLines, newOffset, err := scanRolloutFrom(s.path, s.offset)
	if err != nil {
		return nil, err
	}
	if len(newLines) == 0 && s.loaded {
		return s.entries, nil // nothing appended; skip re-fold and its DB/glob work
	}
	s.loaded = true
	s.lines = append(s.lines, newLines...)
	s.offset = newOffset

	// Re-load while empty: the cache may be written mid-session.
	if s.models == nil {
		s.models = loadModelNames()
	}
	entries := foldRollout(s.lines, s.models, false)
	stampSubagents(entries, s.sessionsRoot)
	s.entries = entries
	return s.entries, nil
}

func NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	root := sessionsRootFrom(rootPath)
	if root == "" {
		root = sessionsRootFrom(path)
	}
	return &streamingTranscript{path: path, sessionsRoot: root}
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
	return readTranscriptView(clean, true)
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
