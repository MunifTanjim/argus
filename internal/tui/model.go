package tui

import (
	"encoding/json"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/vt"

	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type viewMode int

const (
	modeList    viewMode = iota
	modeSession          // composite: history region + conditional prompt dock
	modeScreen
	modeHistoryProjects   // read-only: list of past projects
	modeHistorySessions   // read-only: a project's past sessions
	modeHistoryTranscript // read-only: a past session's transcript (reuses the transcript region)
	modeLogs              // embedded-node log tail (only when the TUI spawned the node)
)

type focusArea int

const (
	focusHistory focusArea = iota
	focusDock
)

type historyKind int

const (
	histTranscript historyKind = iota
	histDetail
)

type cachedTranscript struct {
	chunks []transcript.Chunk
}

// toolBodyEntry caches one tool's on-demand-fetched body (see sessions.toolDetail).
// Transcript chunks ship without ToolInput/Result; fetched per tool when visible.
// done marks a completed fetch so an empty body isn't retried or shown as loading.
type toolBodyEntry struct {
	toolInput     string
	result        string
	resultIsError bool
	loading       bool
	done          bool
}

// subRef identifies an active subscription and what it streams.
type subRef struct {
	subID     string
	sessionID string
	agentID   string // empty = session transcript
	// cacheKey is the AgentSessionID, which changes on /clear (new transcript file)
	// so pre-clear chunks aren't reused. Falls back to sessionID before a hook sets it.
	cacheKey string
}

func (r subRef) key() string {
	base := r.cacheKey
	if base == "" {
		base = r.sessionID
	}
	if r.agentID != "" {
		return base + "/" + r.agentID
	}
	return base
}

type model struct {
	client       Client
	sessions     map[string]session.Session
	order        []string // session IDs, sorted for stable display
	cursor       int
	width        int
	height       int
	reconnecting bool // connection dropped; the client is retrying
	hasDark      bool // terminal background; drives glamour/highlight styling

	mode         viewMode
	screenReturn viewMode // mode to restore when leaving the live screen (ctrl+])
	selectedID   string

	focus       focusArea   // session screen: which pane has focus
	historyView historyKind // session screen: transcript or detail

	transcript      transcriptState             // transcript viewer state (shared by live + history)
	transcriptCache map[string]cachedTranscript // cacheKey -> last-known chunks (per TUI run)
	activeSub       subRef                      // the subscription backing the open transcript view
	sessionSub      subRef                      // stashed session subRef while drilled into a subagent
	toolBodies      map[string]toolBodyEntry    // tool_use id -> on-demand-fetched body (cleared per open)

	term      *vt.Emulator  // live terminal attach (nil when not attached)
	termID    string        // active attach id (unique per attach; gateway/node key on it)
	termStop  chan struct{} // closed to stop the emulator drain goroutine on detach
	termErr   error         // terminal.open failure, shown in the box
	termKeyCh chan termKey  // ordered keystroke queue drained by sendTermKeyLoop

	prompt promptState // compose-then-submit draft for the prompt dock

	pendingKill bool   // awaiting kill confirmation in list view
	flash       string // transient list-view status (e.g. why a jump was refused)

	spawn spawnState // staged "new session" flow (node → dir → name → command)

	spin     int  // animation frame for the list's working-session spinner
	spinning bool // whether a spin tick is currently scheduled (avoids double-arming)

	history historyState // read-only browsing of past sessions on disk

	// Logs tab: present only with an embedded node (logs != nil). logsScroll is the
	// absolute top-line offset when paused; logsFollow pins to the newest line and
	// ignores logsScroll. Gotcha: at ring capacity a paused offset addresses
	// shifting content as old lines evict — acceptable drift for a tail view.
	logs       *logbuf.Buffer
	logsScroll int
	logsFollow bool
}

// transcriptState is the transcript viewer: parsed chunks plus scroll/cursor/fold/
// drill-down state and render caches. Reused by the live and history views.
type transcriptState struct {
	chunks      []transcript.Chunk
	err         error
	cursor      int                           // selected chunk index
	scroll      int                           // top line offset into the rendered transcript
	detailStack []detailFrame                 // detail drill-down frame stack (deepest = active)
	expanded    map[string]bool               // chunk id -> expanded (override default)
	mdRenderers map[int]*glamour.TermRenderer // markdown renderers, keyed by wrap width
	mdCache     map[string]string             // markdown cache, keyed by width+content
	jsonHL      *jsonHighlighter              // JSON syntax highlighter for tool bodies
}

// historyState holds the read-only History view: the project list, a project's
// paginated session list, and the open transcript's title.
type historyState struct {
	projects   []session.HistoryProject
	projCursor int
	err        error
	project    session.HistoryProject // the project being drilled into
	sessions   []session.HistorySession
	sessCursor int
	hasMore    bool
	loading    bool
	title      string // header for the open historical transcript
	// openNodeID/openPath/openAgent route per-tool detail fetches to the right adapter.
	openNodeID string
	openPath   string
	openAgent  string // owning agent of the open transcript, for read routing
}

func newModel(client Client, hasDark bool, logs *logbuf.Buffer) model {
	return model{
		client:          client,
		hasDark:         hasDark,
		logs:            logs,
		logsFollow:      true,
		termKeyCh:       make(chan termKey, termKeyBuf),
		sessions:        make(map[string]session.Session),
		transcriptCache: make(map[string]cachedTranscript),
		toolBodies:      make(map[string]toolBodyEntry),
		transcript: transcriptState{
			expanded:    make(map[string]bool),
			mdRenderers: make(map[int]*glamour.TermRenderer),
			mdCache:     make(map[string]string),
			jsonHL:      newJSONHighlighter(hasDark),
		},
	}
}

func (m model) sessionInteraction() *session.Interaction {
	return m.sessions[m.selectedID].Interaction
}

// interactionKey is a stable identity for a pending interaction: changes for a
// different prompt, stays equal across re-publishes of the same one.
func interactionKey(ix *session.Interaction) string {
	if ix == nil {
		return ""
	}
	b, _ := json.Marshal(ix) // content hash: same prompt → same key
	return string(b)
}

// syncPromptDraft resets the compose draft when the pending interaction changes
// identity (new or cleared) so a stale draft never carries over. A re-publish of
// the same interaction keeps the in-progress draft.
func (m *model) syncPromptDraft() {
	k := interactionKey(m.sessions[m.selectedID].Interaction)
	if k != m.prompt.key {
		m.resetPromptState()
		m.prompt.key = k
	}
}
