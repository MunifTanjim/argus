package opencode

import (
	"strings"

	"github.com/MunifTanjim/argus/internal/session"
)

// Mirror history lists (title, model, tokens, last activity) so live
// cards carry the same info; nil when empty.
func summaryFor(s ocSession) *session.Summary {
	task := strings.TrimSpace(firstLine(s.Title))
	tokens := s.Tokens.Input + s.Tokens.Output + s.Tokens.Reasoning
	last := tsMillis(s.Time.Updated)
	if s.Model.ID == "" && task == "" && tokens == 0 && last == "" {
		return nil
	}
	return &session.Summary{
		ModelName:    s.Model.ID,
		Tokens:       tokens,
		Task:         task,
		LastActivity: last,
	}
}

func summariesEqual(a, b *session.Summary) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
