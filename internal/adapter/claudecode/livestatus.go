package claudecode

import (
	"os"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/session"
)

// liveWorkingTimeout bounds how stale a transcript may be for an "ongoing"
// verdict to still count as working. Claude Code writes on every API response
// and tool call, so an ongoing verdict on a transcript untouched longer than this
// (with nothing pending) means an interrupted/aborted turn, not active work — the
// "[Request interrupted by user]" marker is filtered as noise so IsOngoing can't
// see it. A pending tool/agent call overrides this (long tools produce no writes).
const liveWorkingTimeout = 120 * time.Second

// liveStatusFromChunks classifies a session's live status from its transcript
// chunks: ongoing AI activity means working, a completed turn means idle, an
// empty transcript yields no claim.
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
// "" when the path is empty or unreadable (caller keeps the discovered status).
// Staleness is not death here: the pane exists, so an old transcript means
// long-idle. A freshness guard corrects an IsOngoing blind spot — an interrupted
// turn looks ongoing, so an ongoing verdict on a long-stale transcript with
// nothing pending is reclassified idle.
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
// or subagent call awaiting its result in the LAST AI chunk (e.g. a long Bash
// command that produces no writes while running). Such a session is working no
// matter how stale the transcript looks.
//
// Only the trailing AI chunk is inspected — a long-running tool is necessarily
// the most recent activity. An empty ToolResult on an EARLIER chunk is a folding
// artifact (result landed outside its merge buffer, see chunk_merge.go, or was
// dropped by compaction), not active work, and must not keep a long-stale
// interrupted session classified as working.
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
