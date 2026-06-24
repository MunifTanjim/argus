package claudecode

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/session"
)

// summarize reads a transcript and distills the list-view summary, or nil when the
// transcript can't be read or yields nothing.
func summarize(path string) *session.Summary {
	v, err := ReadTranscriptView(path)
	if err != nil || len(v.Chunks) == 0 {
		return nil
	}
	return summarizeChunks(v.Chunks)
}

// summarizeChunks distills the list-view summary from chunks (in chronological
// order): the latest model/context/tokens (last AI chunk), the latest task (last
// user chunk's first line), and the last-activity timestamp. Returns nil when no
// field could be filled.
func summarizeChunks(chunks []Chunk) *session.Summary {
	s := &session.Summary{}
	for i := len(chunks) - 1; i >= 0; i-- {
		c := chunks[i]
		if s.LastActivity == "" && c.Timestamp != "" {
			s.LastActivity = c.Timestamp
		}
		if s.Model == "" && c.Kind == ChunkAI && c.Model != "" {
			s.Model = c.Model
			if c.HasContext {
				s.HasContext, s.ContextPct = true, c.ContextPct
			}
			s.Tokens = c.Usage.Context()
		}
		if s.Task == "" && c.Kind == ChunkUser && strings.TrimSpace(c.Text) != "" {
			s.Task = firstLineOf(c.Text)
		}
		if s.Model != "" && s.Task != "" && s.LastActivity != "" {
			break
		}
	}
	if *s == (session.Summary{}) {
		return nil
	}
	return s
}

// refreshesSummary reports whether a hook event is worth re-parsing the transcript
// for. The high-frequency PreToolUse/PostToolUse are excluded; the curated set
// covers task starts, turn ends, and attention moments.
func refreshesSummary(event string) bool {
	switch event {
	case "SessionStart", "UserPromptSubmit", "Stop", "Notification", "PermissionRequest":
		return true
	}
	return false
}

// repoName returns a display name for dir: the basename of the nearest ancestor
// that contains a ".git" entry (a directory for a normal repo, a file for a
// worktree/submodule), or the basename of dir itself when it is not inside a
// repository. Returns "" only when dir is empty.
func repoName(dir string) string {
	for d := dir; d != ""; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return filepath.Base(d)
		}
		parent := filepath.Dir(d)
		if parent == d { // reached the filesystem root
			break
		}
		d = parent
	}
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}
