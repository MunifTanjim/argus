package antigravity

import (
	"strings"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// summarizeChunks distills a list-view card from chunks. Tokens are omitted (agy's
// transcript carries none). Returns nil when empty.
func summarizeChunks(chunks []transcript.Chunk) *session.Summary {
	s := &session.Summary{}
	for i := len(chunks) - 1; i >= 0; i-- {
		c := chunks[i]
		if s.LastActivity == "" && c.Timestamp != "" {
			s.LastActivity = c.Timestamp
		}
		if s.Task == "" && c.Kind == transcript.ChunkUser && strings.TrimSpace(c.Text) != "" {
			s.Task = firstLine(c.Text)
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
		if chunks, err := parseTranscript(transcriptPath); err == nil {
			s = summarizeChunks(chunks)
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
