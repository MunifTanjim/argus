package claudecode

import (
	"os"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

// StreamingTranscript incrementally folds a growing transcript (session or
// subagent) file. It keeps a byte cursor, the accumulated classified msgs, and
// the cumulative subagent links, so each Refresh parses only newly appended
// lines and re-folds in memory. Output matches ReadStreamingView of the same
// file content. Not safe for concurrent use.
type StreamingTranscript struct {
	path       string
	isSubagent bool
	offset     int64
	msgs       []parser.ClassifiedMsg
	links      map[string]string // agentID -> toolUseID (cumulative; unused for subagents)
}

// NewStreamingTranscript returns a folder positioned at the start of the file.
// isSubagent disables subagent linking (subagent files have no nested subagents).
func NewStreamingTranscript(path string, isSubagent bool) *StreamingTranscript {
	return &StreamingTranscript{path: path, isSubagent: isSubagent, links: map[string]string{}}
}

// Refresh reads newly appended lines, updates state, and returns the full folded
// chunk list (subagent traces de-inlined). If the file shrank below the cursor
// (truncation/rotation) it rebuilds from the start.
func (s *StreamingTranscript) Refresh() ([]Chunk, error) {
	if fi, err := os.Stat(s.path); err == nil && fi.Size() < s.offset {
		s.offset = 0
		s.msgs = nil
		s.links = map[string]string{}
	}

	newMsgs, newLinks, newOffset, err := parser.ReadSessionIncremental(s.path, s.offset, s.isSubagent)
	if err != nil {
		return nil, err
	}
	s.msgs = append(s.msgs, newMsgs...)
	for agentID, toolUseID := range newLinks {
		s.links[agentID] = toolUseID
	}
	s.offset = newOffset

	pchunks := parser.BuildChunks(s.msgs)
	var agentRefs map[string]string
	if !s.isSubagent {
		agentRefs = parser.AgentRefsFromLinks(s.path, s.links)
	}
	return foldChunks(pchunks, agentRefs, nil), nil
}
