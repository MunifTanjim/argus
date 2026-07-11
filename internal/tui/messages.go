package tui

import (
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type notificationMsg api.Notification

// connStateMsg reports a connection-state transition from the reconnecting client.
type connStateMsg struct{ connected bool }

// sessionsReplacedMsg carries an authoritative session list for a post-reconnect resync.
type sessionsReplacedMsg []session.Session
type transcriptMsg struct {
	id     string
	chunks []transcript.Chunk
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
	chunks []transcript.Chunk
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

// nodeID identifies the probed node so a stale reply (node changed under a slow
// probe) can be discarded.
type spawnAgentsMsg struct {
	nodeID string
	agents []api.AgentInfo
	err    error
}

// Successful spawns surface via registry events; only the error is acted on.
type spawnResultMsg struct{ err error }

type resumeResultMsg struct {
	sessionID string
	err       error
}

// clearPendingResumeMsg drops a still-pending resume selection so a stale id can't
// later match a reused tmux pane id.
type clearPendingResumeMsg struct{ id string }
type logTickMsg struct{}    // embedded-node logs changed; wake the render loop
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
