package antigravity

import (
	"strings"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// summarizeEntries omits tokens: agy's transcript carries none. Returns nil
// when empty.
func summarizeEntries(entries []transcript.Entry) *session.Summary {
	s := &session.Summary{}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if s.LastActivity == "" && e.Timestamp != "" {
			s.LastActivity = e.Timestamp
		}
		if s.Task == "" && e.Kind == transcript.EntryUser && strings.TrimSpace(e.Text) != "" {
			s.Task = firstLine(e.Text)
		}
		if s.Task != "" && s.LastActivity != "" {
			break
		}
	}
	if *s == (session.Summary{}) {
		return nil
	}
	return s
}

// buildSummary assembles a session card. hookModel is a fallback when the
// conversation db has no model.
func buildSummary(convID, transcriptPath, hookModel string) *session.Summary {
	var s *session.Summary
	if transcriptPath != "" {
		if entries, err := parseTranscript(transcriptPath, false); err == nil {
			s = summarizeEntries(entries)
		}
	}
	name, color := conversationModel(convID)
	if name == "" && hookModel != "" {
		name, color = modelNameColor(hookModel)
	}
	if name != "" {
		if s == nil {
			s = &session.Summary{}
		}
		s.ModelName, s.ModelColor = name, color
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// refreshesSummary reports whether a hook event should re-fold the transcript. Only
// PreInvocation: Stop is too slow (agy tears the hook down before delivery reaches argusd).
func refreshesSummary(event string) bool {
	return event == "PreInvocation"
}
