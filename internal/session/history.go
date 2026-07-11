package session

// HistoryProject is one Claude Code project directory discovered on disk, for the
// read-only History view. NodeID/NodeLabel are empty node-side and stamped by
// the gateway when aggregating across machines.
type HistoryProject struct {
	ProjectDir   string `json:"project_dir"`          // routing key for its sessions (node-local path)
	Cwd          string `json:"cwd"`                  // working directory of the project
	Repo         string `json:"repo,omitempty"`       // git repo basename, when inside a repo
	Label        string `json:"label"`                // display name (repo, else cwd basename)
	SessionCount int    `json:"session_count"`        // number of transcripts in the project
	LastActivity string `json:"last_activity"`        // RFC3339 (UTC) newest transcript mod time
	NodeID       string `json:"node_id,omitempty"`    // origin machine (gateway-stamped)
	NodeLabel    string `json:"node_label,omitempty"` // origin machine label (gateway-stamped)
}

// HistorySession is one past session within a project, for the History session list.
// It carries no live/tmux state; TranscriptPath + NodeID address its transcript.
type HistorySession struct {
	SessionID      string `json:"session_id"`
	Agent          string `json:"agent,omitempty"`         // owning agent, stamped by the node
	Resumable      bool   `json:"resumable,omitempty"`     // agent can resume this session by id
	Title          string `json:"title,omitempty"`         // custom/AI title, when present
	FirstMessage   string `json:"first_message,omitempty"` // first user message (title fallback)
	TranscriptPath string `json:"transcript_path"`         // node-local path; routing key for the view
	ModelName      string `json:"model_name,omitempty"`
	ModelColor     string `json:"model_color,omitempty"`
	LastActivity   string `json:"last_activity"` // RFC3339 (UTC) transcript mod time
	Tokens         int    `json:"tokens,omitempty"`
	TurnCount      int    `json:"turn_count,omitempty"`
	DurationMs     int64  `json:"duration_ms,omitempty"`
	NodeID         string `json:"node_id,omitempty"`
	NodeLabel      string `json:"node_label,omitempty"`
}

// HistorySessionPage is a window of a project's sessions, newest-first, with a flag
// indicating whether older sessions remain beyond this page.
type HistorySessionPage struct {
	Items   []HistorySession `json:"items"`
	HasMore bool             `json:"has_more"`
}
