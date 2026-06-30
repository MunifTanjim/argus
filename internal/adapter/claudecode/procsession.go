package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Claude Code writes a live status file per process at ~/.claude/sessions/
// <pid>.json — the fastest way to learn a session's id, cwd, and name at
// discovery, before any hook fires.

// procSession is the subset of ~/.claude/sessions/<pid>.json that argus uses.
type procSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Version   string `json:"version"`
	// Entrypoint is how the session launched ("cli" or "claude-vscode"); the
	// authoritative frontend signal.
	Entrypoint string `json:"entrypoint"`
}

// claudeSessionsDirOverride lets tests point the reader at a temp directory; empty
// means use ~/.claude/sessions.
var claudeSessionsDirOverride string

// claudeSessionsDir returns the directory holding the per-process session files.
func claudeSessionsDir() string {
	if claudeSessionsDirOverride != "" {
		return claudeSessionsDirOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

// readProcSession reads and parses ~/.claude/sessions/<pid>.json. ok is false when
// the file is missing, unreadable, malformed, or carries no session id.
func readProcSession(dir string, pid int) (procSession, bool) {
	if dir == "" || pid <= 0 {
		return procSession{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(pid)+".json"))
	if err != nil {
		return procSession{}, false
	}
	var ps procSession
	if err := json.Unmarshal(data, &ps); err != nil || ps.SessionID == "" {
		return procSession{}, false
	}
	return ps, true
}

// listProcSessions reads every ~/.claude/sessions/<pid>.json, skipping dirs,
// non-.json entries, and unparseable/idless files. Returns nil on an empty or
// unreadable dir.
func listProcSessions(dir string) []procSession {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []procSession
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		if ps, ok := readProcSession(dir, pid); ok {
			out = append(out, ps)
		}
	}
	return out
}

// findProcSessionByID scans the sessions dir for the file whose sessionId matches.
// Files are pid-keyed but a hook only carries the session id, so a scan is needed;
// the dir holds few small files so it's cheap. ok is false on empty dir/id, a read
// error, or no match.
func findProcSessionByID(dir, sessionID string) (procSession, bool) {
	if dir == "" || sessionID == "" {
		return procSession{}, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return procSession{}, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ps procSession
		if json.Unmarshal(data, &ps) == nil && ps.SessionID == sessionID {
			return ps, true
		}
	}
	return procSession{}, false
}
