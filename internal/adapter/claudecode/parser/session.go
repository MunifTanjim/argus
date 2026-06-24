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

// SessionMeta holds session-level metadata extracted from a JSONL file.
// Unlike SessionInfo (which is for the picker), SessionMeta is designed for
// the info bar -- just the metadata fields, no picker-specific data.
type SessionMeta struct {
	Cwd            string
	GitBranch      string
	PermissionMode string
}

// ExtractSessionMeta returns session-level metadata from a JSONL file.
// Reads the full file to capture the last permissionMode (mode can change mid-session).
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

// ReadSessionIncremental reads complete lines appended after offset and returns
// the newly classified msgs, the subagent links (agentID -> toolUseID) found in
// those lines, and the offset advanced to the end of the last NEWLINE-terminated
// line. A trailing partial line (no \n yet) is not consumed; it is read on the
// next call once complete. Oversized lines are skipped but still advance offset.
// This is the building block for live tailing.
//
// clearSidechain forces every entry's isSidechain flag off before classifying.
// Subagent files mark all entries isSidechain=true (they run off the main
// thread) but represent the subagent's own conversation, so streaming a
// subagent file must clear the flag or Classify drops every entry. Mirrors
// readSubagentSession's behavior for the live-tailing path.
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

	// Only process up to and including the last newline; defer any trailing
	// partial line so a half-written final entry is re-read once complete.
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
