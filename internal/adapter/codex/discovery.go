// Package codex is the argus adapter for OpenAI Codex CLI. Sessions come from
// Codex's shared app-server daemon, which every Codex TUI attaches to by default.
package codex

import (
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/session"
)

const Agent = "codex"

func normalizeTTY(tty string) string { return strings.TrimPrefix(tty, "/dev/") }

// Returns nil when all metadata fields are empty.
func summaryFor(model, title string, tokens int, modelNames map[string]string) *session.Summary {
	if dn := modelNames[model]; dn != "" {
		model = dn
	}
	if model == "" && title == "" && tokens == 0 {
		return nil
	}
	return &session.Summary{ModelName: model, ModelColor: modelColorFor(model), Tokens: tokens, Task: title}
}

// findRolloutPathIn globs for a thread's rollout under sessionsRoot; empty falls
// back to the live ~/.codex/sessions.
func findRolloutPathIn(sessionsRoot, threadID string) string {
	if sessionsRoot == "" {
		dir, err := codexHome()
		if err != nil {
			return ""
		}
		sessionsRoot = filepath.Join(dir, "sessions")
	}
	matches, err := filepath.Glob(filepath.Join(sessionsRoot, "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// sessionsRootFrom returns the sessions dir for a rollout laid out as
// <root>/sessions/YYYY/MM/DD/rollout-*.jsonl. Empty in → empty out.
func sessionsRootFrom(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(transcriptPath))))
}
