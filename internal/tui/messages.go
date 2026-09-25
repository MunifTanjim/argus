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

type projectsTreeMsg struct {
	tree []api.ProjectNode
	err  error
}

// projectsActionMsg is a management action's result; verb labels it on the
// flash line.
type projectsActionMsg struct {
	verb     string
	ok       string // flash on success; defaults to "<verb> done"
	selectID string // workspace to select once the tree reloads (create)
	// reloadChanges drops the Changes list so it re-fetches against the new
	// target after the tree reloads (set target).
	reloadChanges bool
	err           error
}

type changedFilesMsg struct {
	ws      string
	against string
	gen     int
	files   []api.ChangedFile
	err     error
}

type wsDiffMsg struct {
	ws, path, diff string
	against, rev   string
	notShown       bool
	err            error
}

type commitsMsg struct {
	ws      string
	gen     int
	commits []api.Commit
	err     error
}

type commitFilesMsg struct {
	ws, sha string
	files   []api.ChangedFile
	err     error
}

type listDirMsg struct {
	ws, dir string
	entries []api.DirEntry
	err     error
}

type readFileMsg struct {
	ws, path, content string
	notShown          bool
	err               error
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

// resumeResultMsg carries the result of a resume; on success the resumed
// session's transcript view is entered.
type resumeResultMsg struct {
	sessionID string
	err       error
}
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

type branchesMsg struct {
	projectID string
	branches  []api.BranchInfo
	err       error
}

type prsMsg struct {
	projectID string
	prs       []api.PRInfo
	err       error
}

type issuesMsg struct {
	projectID string
	issues    []api.IssueInfo
	err       error
}

type createDoneMsg struct {
	res    api.WorkspaceCreateResult
	source string
	seq    int // createState.seq of the picker that sent it
	err    error
}
