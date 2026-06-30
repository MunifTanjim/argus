package parser

import (
	"bytes"
	"io"
	"os"
	"time"
)

// SessionInfo holds metadata about a discovered session file for the picker.
type SessionInfo struct {
	Path           string
	SessionID      string
	ModTime        time.Time
	Title          string // custom or AI-generated session title (custom wins)
	FirstMessage   string // first user message text, truncated
	LastPrompt     string // most recent user input (from type=last-prompt)
	TurnCount      int    // conversation turns (user messages + their first AI responses)
	IsOngoing      bool   // AI activity after last ending event
	ContextTokens  int    // last assistant message's context window usage
	DurationMs     int64  // last timestamp - first timestamp
	Model          string // model from first real assistant entry
	Cwd            string // working directory from session entries
	GitBranch      string // git branch from session entries
	PermissionMode string // last permission mode: "default", "acceptEdits", "bypassPermissions", "plan"
}

// SessionMeta holds session-level metadata for the info bar (vs. SessionInfo,
// which carries picker-specific data).
type SessionMeta struct {
	Cwd            string
	GitBranch      string
	PermissionMode string
}

// ExtractSessionMeta returns session-level metadata from a JSONL file. Reads
// the full file since permissionMode can change mid-session and we want the last.
func ExtractSessionMeta(path string) SessionMeta {
	m := scanSessionMetadata(path)
	return SessionMeta{
		Cwd:            m.cwd,
		GitBranch:      m.gitBranch,
		PermissionMode: m.permissionMode,
	}
}

// ReadSession reads a JSONL session file and returns the fully processed chunk list.
func ReadSession(path string) ([]Chunk, error) {
	msgs, _, _, err := ReadSessionIncremental(path, 0, false)
	if err != nil {
		return nil, err
	}
	return BuildChunks(msgs), nil
}

// ReadSubagentSession reads one subagent JSONL file into chunks. Subagent files
// mark every entry isSidechain=true, so the flag must be cleared or Classify
// drops them all.
func ReadSubagentSession(path string) ([]Chunk, error) {
	msgs, _, _, err := ReadSessionIncremental(path, 0, true)
	if err != nil {
		return nil, err
	}
	return BuildChunks(msgs), nil
}

// ReadSessionIncremental reads complete lines appended after offset and returns
// the new msgs, subagent links (agentID -> toolUseID), and the advanced offset.
// A trailing partial line (no \n yet) is left for the next call. The building
// block for live tailing.
//
// clearSidechain forces isSidechain off before classifying, needed when tailing
// a subagent file (see ReadSubagentSession) or Classify drops every entry.
func ReadSessionIncremental(path string, offset int64, clearSidechain bool) ([]ClassifiedMsg, map[string]string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, offset, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, nil, offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, offset, err
	}

	// Process only through the last newline; defer any partial final line so a
	// half-written entry is re-read once complete.
	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL < 0 {
		return nil, nil, offset, nil // no complete line appended
	}
	newOffset := offset + int64(lastNL) + 1

	var msgs []ClassifiedMsg
	links := map[string]string{}
	for _, line := range bytes.Split(data[:lastNL+1], []byte{'\n'}) {
		if len(line) == 0 || len(line) > maxLineSize {
			continue
		}
		entry, ok := ParseEntry(line)
		if !ok {
			continue
		}
		if clearSidechain {
			entry.IsSidechain = false
		}
		if msg, ok := Classify(entry); ok {
			msgs = append(msgs, msg)
		}
		if agentID, toolUseID, ok := agentLinkFromEntry(entry); ok {
			links[agentID] = toolUseID
		}
	}
	return msgs, links, newOffset, nil
}
