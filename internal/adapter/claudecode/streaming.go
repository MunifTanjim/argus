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
	rootPath   string // session root, for resolving sibling subagent files (flat dir)
	isSubagent bool
	offset     int64
	msgs       []parser.ClassifiedMsg
	links      map[string]string // agentID -> toolUseID (cumulative)
}

// NewStreamingTranscript returns a folder positioned at the start of the file.
// rootPath is the session root used to resolve subagent files (pass path itself
// for a session root). isSubagent clears the sidechain flag while reading a
// subagent file. Nested children are linked for both session and subagent files;
// linking is suppressed past MaxSubagentDepth.
func NewStreamingTranscript(path, rootPath string, isSubagent bool) *StreamingTranscript {
	return &StreamingTranscript{path: path, rootPath: rootPath, isSubagent: isSubagent, links: map[string]string{}}
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
	agentRefs := parser.AgentRefsFromLinks(s.rootPath, s.links)
	if s.isSubagent &&
		parser.SpawnDepth(s.rootPath, parser.AgentIDFromPath(s.path)) >= parser.MaxSubagentDepth {
		agentRefs = nil
	}
	return foldChunks(pchunks, agentRefs, nil), nil
}
