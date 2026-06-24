package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// Claude Code writes a live status file per running process at
// ~/.claude/sessions/<pid>.json. It is the fastest way to learn a session's id,
// cwd, and name at discovery time -- before any hook fires -- so the dashboard can
// show full info immediately.

// procSession is the subset of ~/.claude/sessions/<pid>.json that argus uses.
type procSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Version   string `json:"version"`
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
