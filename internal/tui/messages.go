package tui

import (
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type notificationMsg api.Notification

// connStateMsg reports a connection-state transition from the reconnecting client.
type connStateMsg struct{ connected bool }

// sessionsReplacedMsg carries an authoritative session list for a post-reconnect resync.
type sessionsReplacedMsg []session.Session
type transcriptMsg struct {
	id     string
	chunks []claudecode.Chunk
	err    error
}
type captureMsg struct {
	id     string
	screen string
	err    error
}
type histProjectsMsg struct {
	projects []session.HistoryProject
	err      error
}
type histSessionsMsg struct {
	projectDir string
	offset     int
	page       session.HistorySessionPage
	err        error
}
type histTranscriptMsg struct {
	chunks []claudecode.Chunk
	err    error
}

// spawnNodesMsg carries the server.info reply for a pending "new session" action.
// cwd is the working dir captured when the user pressed New.
type spawnNodesMsg struct {
	nodes    []api.NodeInfo
	projects []session.HistoryProject
	cwd      string
	err      error
}
type screenTickMsg struct{} // screen refresh
type spinResumeMsg struct{} // periodic kick that re-arms the list spinner
type spinTickMsg struct{}   // list spinner animation frame

// toolDetailMsg carries an on-demand tool-body fetch (sessions.toolDetail),
// keyed by the tool_use id so it can be filed into the toolBodies cache.
type toolDetailMsg struct {
	toolID string
	detail api.ToolDetail
	err    error
}

// transcriptDeltaMsg carries a subscription catch-up (initial) or a live push.
type transcriptDeltaMsg struct {
	ref     subRef
	delta   api.TranscriptDelta
	initial bool
}
