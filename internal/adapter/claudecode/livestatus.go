package claudecode

import (
	"os"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/session"
)

// liveWorkingTimeout bounds how recently a transcript must have been written for
// an "ongoing" classification to be trusted as working. Claude Code writes to
// the transcript on every API response and tool call, so an ongoing verdict on a
// transcript untouched for longer than this — with no tool/agent call still
// pending — means the turn was interrupted or aborted, not active work (e.g. a
// "[Request interrupted by user]" marker, which the parser filters as noise, so
// IsOngoing cannot see it). A pending tool/agent call overrides this, since a
// long-running tool legitimately produces no writes while it runs.
const liveWorkingTimeout = 120 * time.Second

// liveStatusFromChunks classifies a discovered session's live status from its
// parsed transcript chunks: ongoing AI activity (or a pending tool_use, or a
// trailing user prompt) means working; a completed turn (last ending event is
// text output / interruption) means idle. An empty transcript yields no claim.
func liveStatusFromChunks(pchunks []parser.Chunk) session.Status {
	if len(pchunks) == 0 {
		return ""
	}
	if parser.IsOngoing(pchunks) {
		return session.StatusWorking
	}
	return session.StatusIdle
}

// classifyLiveStatus reads a transcript and classifies its live status. Returns
// session.Status("") when the path is empty or the transcript can't be read —
// the caller leaves the session as discovered. Staleness is intentionally NOT
// treated as death here: the pane exists, so an old transcript means long-idle.
//
// A freshness guard corrects a known IsOngoing blind spot: an interrupted turn
// (tool activity followed by a filtered "[Request interrupted by user]" marker)
// looks ongoing. A genuinely working session writes constantly, so an ongoing
// verdict on a long-stale transcript with nothing pending is reclassified idle.
func classifyLiveStatus(path string) session.Status {
	if path == "" {
		return ""
	}
	pchunks, err := parser.ReadSession(path)
	if err != nil {
		return ""
	}
	st := liveStatusFromChunks(pchunks)
	if st == session.StatusWorking && !hasPendingWork(pchunks) {
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > liveWorkingTimeout {
			return session.StatusIdle
		}
	}
	return st
}

// hasPendingWork reports whether the session is genuinely mid-execution: a tool
// or subagent call awaiting its result in the LAST AI chunk — a genuinely
// executing operation that legitimately produces no transcript writes while it
// runs (e.g. a long Bash command). Such a session is working regardless of how
// stale the transcript looks.
//
// Only the trailing AI chunk is inspected. A long-running tool is necessarily
// the most recent activity, so a real pending call lives there. An empty
// ToolResult on an EARLIER chunk is a folding artifact — a tool_result that
// landed outside its tool_use's merge buffer (mergeAIBuffer only pairs results
// within one consecutive-AI-message buffer; see chunk_merge.go), or was dropped
// by context compaction. Such historical gaps are not active work and must not
// keep a long-stale interrupted session classified as working.
func hasPendingWork(pchunks []parser.Chunk) bool {
	for i := len(pchunks) - 1; i >= 0; i-- {
		if pchunks[i].Type != parser.AIChunk {
			continue
		}
		for _, item := range pchunks[i].Items {
			if (item.Type == parser.ItemToolCall || item.Type == parser.ItemSubagent) && item.ToolResult == "" {
				return true
			}
		}
		return false // last AI chunk inspected; earlier gaps are folding artifacts
	}
	return false
}
