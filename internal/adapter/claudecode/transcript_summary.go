package claudecode

import (
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/session"
)

// summarize reads a transcript and distills the list-view summary (plus the
// tasks-dir session id), or nil when it can't be read or yields nothing. It
// parses directly rather than via ReadTranscriptView so the per-line key walk
// stays off the client transcript paths (subagent linking is irrelevant here).
func summarize(path string) *session.Summary {
	pchunks, err := parser.ReadSession(path)
	if err != nil || len(pchunks) == 0 {
		return nil
	}
	s := summarizeChunks(pchunks)
	if keys := strings.Join(parser.ResolveSessionDirKeys(pchunks).TasksCandidates(), " "); keys != "" {
		if s == nil {
			s = &session.Summary{}
		}
		s.TaskSessionKeys = keys
	}
	return s
}

// summarizeChunks counts a turn still in progress, and takes the task from the
// last user chunk's first line. Returns nil when no field could be filled.
func summarizeChunks(pchunks []parser.Chunk) *session.Summary {
	s := &session.Summary{}
	for i := len(pchunks) - 1; i >= 0; i-- {
		pc := pchunks[i]
		if s.LastActivity == "" && !pc.Timestamp.IsZero() {
			s.LastActivity = formatTS(pc.Timestamp)
		}
		if s.ModelName == "" && pc.Type == parser.AIChunk && pc.Model != "" {
			s.ModelName = modelDisplayName(pc.Model)
			s.ModelColor = modelColorHex(pc.Model)
			if d := parser.ComputeContextDelta(pc.Cycles); d != nil {
				s.HasContext, s.ContextPct = true, d.LastUsagePct
			}
			s.Tokens = transformUsage(pc.Usage).Context()
		}
		if s.Task == "" && pc.Type == parser.UserChunk && strings.TrimSpace(pc.UserText) != "" {
			s.Task = firstLineOf(pc.UserText)
		}
		if s.ModelName != "" && s.Task != "" && s.LastActivity != "" {
			break
		}
	}
	if *s == (session.Summary{}) {
		return nil
	}
	return s
}

// refreshesSummary reports whether a hook event warrants re-parsing the
// transcript. High-frequency PreToolUse/PostToolUse are excluded.
func refreshesSummary(event string) bool {
	switch event {
	case "SessionStart", "UserPromptSubmit", "Stop", "Notification", "PermissionRequest":
		return true
	}
	return false
}
