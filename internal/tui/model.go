package tui

import (
	"encoding/json"

	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/session"
)

type viewMode int

const (
	modeList    viewMode = iota
	modeSession          // composite: history region + conditional prompt dock
	modeScreen
	modeHistoryProjects   // read-only: list of past projects
	modeHistorySessions   // read-only: a project's past sessions
	modeHistoryTranscript // read-only: a past session's transcript (reuses the transcript region)
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
	chunks []claudecode.Chunk
}

// toolBodyEntry caches one tool item's on-demand-fetched body (see
// sessions.toolDetail). Transcript chunks ship without ToolInput/Result; the
// detail view fetches them per tool when a body becomes visible. done marks a
// completed fetch (so an empty body isn't retried or shown as "loading").
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
}

func (r subRef) key() string {
	if r.agentID != "" {
		return r.sessionID + "/" + r.agentID
	}
	return r.sessionID
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

	mode       viewMode
	selectedID string

	focus       focusArea   // session screen: which pane has focus
	historyView historyKind // session screen: transcript or detail

	transcript      transcriptState             // transcript viewer state (shared by live + history)
	transcriptCache map[string]cachedTranscript // cacheKey -> last-known chunks (per TUI run)
	activeSub       subRef                      // the subscription backing the open transcript view
	sessionSub      subRef                      // stashed session subRef while drilled into a subagent
	toolBodies      map[string]toolBodyEntry    // tool_use id -> on-demand-fetched body (cleared per open)

	screen    string // last captured pane screen
	screenErr error
	keyCh     chan paneKey // ordered keystrokes for the screen-view sender goroutine

	prompt promptState // compose-then-submit draft for the prompt dock

	pendingKill bool   // awaiting kill confirmation in list view
	flash       string // transient list-view status (e.g. why a jump was refused)

	spawn spawnState // staged "new session" flow (node → dir → name → command)

	spin     int  // animation frame for the list's working-session spinner
	spinning bool // whether a spin tick is currently scheduled (avoids double-arming)

	history historyState // read-only browsing of past sessions on disk
}

// transcriptState is the transcript viewer: the parsed chunks plus the scroll/cursor/
// fold/drill-down view state and the markdown/JSON render caches. Reused by the live
// session view and the read-only history transcript.
type transcriptState struct {
	chunks      []claudecode.Chunk
	err         error
	cursor      int                           // selected chunk index
	scroll      int                           // top line offset into the rendered transcript
	detailStack []detailFrame                 // detail drill-down frame stack (deepest = active)
	expanded    map[string]bool               // chunk id -> expanded (override default)
	mdRenderers map[int]*glamour.TermRenderer // markdown renderers, keyed by wrap width
	mdCache     map[string]string             // markdown cache, keyed by width+content
	jsonHL      *jsonHighlighter              // JSON syntax highlighter for tool bodies
}

// historyState holds the read-only History view: the project list, the drilled-into
// project's session list (paginated), and the open historical transcript's title.
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
	// openNodeID/openPath address the open historical transcript for follow-up
	// per-tool detail fetches (sessions.historyToolDetail).
	openNodeID string
	openPath   string
}

func newModel(client Client, hasDark bool, keyCh chan paneKey) model {
	return model{
		client:          client,
		hasDark:         hasDark,
		keyCh:           keyCh,
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

// sessionInteraction returns the open session's pending interaction, or nil.
func (m model) sessionInteraction() *session.Interaction {
	return m.sessions[m.selectedID].Interaction
}

// interactionKey is a stable identity for a pending interaction: it changes when a
// different prompt appears but stays equal across re-publishes of the same one.
func interactionKey(ix *session.Interaction) string {
	if ix == nil {
		return ""
	}
	b, _ := json.Marshal(ix) // content hash: same prompt → same key
	return string(b)
}

// syncPromptDraft resets the compose draft when the open session's pending
// interaction changes identity (a new prompt, or cleared), so a stale draft never
// carries over. A re-publish of the same interaction keeps the in-progress draft.
func (m *model) syncPromptDraft() {
	k := interactionKey(m.sessions[m.selectedID].Interaction)
	if k != m.prompt.key {
		m.resetPromptState()
		m.prompt.key = k
	}
}
