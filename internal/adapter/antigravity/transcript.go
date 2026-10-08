package antigravity

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
	entries, err := parseTranscript(path, finished)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	home := resolveHome(path)
	name, color := conversationModelIn(home, convIDFromPath(path))
	stampTurnModelWith(entries, name, color)
	stampSubagentTrace(entries, home)
	return transcript.TranscriptView{Entries: entries}, nil
}

// stampSubagentTrace recomputes each subagent's HasTrace against home, correcting
// linkSubagent's live-home resolution for a bundle extracted under a temp dir.
func stampSubagentTrace(entries []transcript.Entry, home string) {
	for i := range entries {
		for k := range entries[i].Subagents {
			sub := &entries[i].Subagents[k]
			if sub.ID == "" {
				continue
			}
			_, sub.HasTrace = childTranscriptPathIn(home, sub.ID)
		}
	}
}

// stampTurnModelWith fills the model on footers the transcript left blank.
func stampTurnModelWith(entries []transcript.Entry, name, color string) {
	if name == "" {
		return
	}
	for i := range entries {
		if entries[i].Kind == transcript.EntryTurnEnd && entries[i].ModelName == "" {
			entries[i].ModelName = name
			entries[i].ModelColor = color
		}
	}
}

func SubagentFilePath(rootPath, agentID string) (string, bool) {
	return childTranscriptPathIn(resolveHome(rootPath), agentID)
}

func ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	return readSubagentView(rootPath, agentID, false)
}

func readSubagentView(rootPath, agentID string, finished bool) (transcript.TranscriptView, bool, error) {
	home := resolveHome(rootPath)
	path, ok := childTranscriptPathIn(home, agentID)
	if !ok {
		return transcript.TranscriptView{}, false, nil
	}
	entries, err := parseTranscript(path, finished)
	if err != nil {
		return transcript.TranscriptView{}, false, err
	}
	name, color := conversationModelIn(home, agentID)
	stampTurnModelWith(entries, name, color)
	stampSubagentTrace(entries, home)
	return transcript.TranscriptView{Entries: entries}, true, nil
}

// FindToolDetail returns a tool/subagent entry's full input and result.
// Empty agentID searches path itself.
func FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	if agentID != "" {
		p, ok := childTranscriptPathIn(resolveHome(path), agentID)
		if !ok {
			return transcript.ToolDetail{}, false, nil
		}
		path = p
	}
	entries, err := parseTranscript(path, false)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	d, ok := transcript.FindToolDetail(entries, toolID)
	return d, ok, nil
}

// streamingTranscript tails a growing transcript: a byte cursor reads only newly
// appended lines, which accumulate and re-fold in memory each Refresh.
type streamingTranscript struct {
	path       string
	convID     string
	home       string
	offset     int64
	loaded     bool
	lines      []line
	modelName  string
	modelColor string
	entries    []transcript.Entry
}

func (s *streamingTranscript) Refresh() ([]transcript.Entry, error) {
	if fi, err := os.Stat(s.path); err == nil && fi.Size() < s.offset {
		s.offset = 0 // truncation/rotation: rebuild from the start
		s.loaded = false
		s.lines = nil
		s.entries = nil
	}

	newLines, newOffset, err := scanTranscriptFrom(s.path, s.offset)
	if err != nil {
		return nil, err
	}
	if len(newLines) == 0 && s.loaded {
		return s.entries, nil // nothing appended; skip re-fold and its DB work
	}
	s.loaded = true
	s.lines = append(s.lines, newLines...)
	s.offset = newOffset

	// Re-query while empty: the model may land in the DB mid-session.
	if s.modelName == "" {
		s.modelName, s.modelColor = conversationModelIn(s.home, s.convID)
	}
	entries := foldTranscript(s.lines, false)
	stampTurnModelWith(entries, s.modelName, s.modelColor)
	stampSubagentTrace(entries, s.home)
	s.entries = entries
	return s.entries, nil
}

func NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	home := resolveHome(rootPath)
	if home == "" {
		home = resolveHome(path)
	}
	return &streamingTranscript{path: path, convID: convIDFromPath(path), home: home}
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
	return readTranscriptView(clean, true)
}

func ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error) {
	return readSubagentView(path, agentID, true)
}

func FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	clean, err := safeBrainPath(path)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	return FindToolDetail(clean, agentID, toolID)
}
