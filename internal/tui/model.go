package tui

import (
	"encoding/json"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type cachedTranscript struct {
	entries []transcript.Entry
}

// toolBodyEntry caches one tool's on-demand-fetched body (see sessions.toolDetail).
// Transcript entries ship without ToolInput/Result; fetched per tool when visible.
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
	// so pre-clear entries aren't reused. Falls back to sessionID before a hook sets it.
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
	client        Client
	sessions      map[string]session.Session
	order         []string       // session IDs, sorted for stable display
	activeOnly    bool           // the session lists show only active and awaiting-input sessions
	sessionFilter string         // the session lists show only the sessions that match it
	terminals     []api.Terminal // every node's terminals, from the last load
	terminalsErr  error
	terminalsDone bool                 // a terminal load has finished
	nodeInfo      []api.NodeInfo       // nodes and their capabilities, from the last load
	hosts         map[string]hostEntry // node id -> last host.info reply
	hostGen       int
	width         int
	height        int
	reconnecting  bool // connection dropped; the client is retrying
	hasDark       bool // terminal background; drives glamour/highlight styling
	viewer        bool // offline viewer mode: opens directly in transcript view
	brand         int  // the title icon, an index into brandGlyphs

	redactMode   bool   // --redact: interactive redaction affordance in the viewer
	bundlePath   string // source .argus path (for the -redacted output name)
	redactSrcDir string // extracted cache dir to redact from

	main    backStack // the main pane's components; the last one shows
	focused container
	left    leftSidebarState
	right   rightSidebarState

	memory viewMemory

	transcriptCache map[string]cachedTranscript // cacheKey -> last-known entries (per TUI run)
	render          renderCache

	termKeyCh chan termKey // ordered keystroke queue drained by sendTermKeyLoop

	dock dockComp

	flash string // transient list-view status (e.g. why a jump was refused)

	spin     int  // animation frame for the list's working-session spinner
	spinning bool // whether a spin tick is currently scheduled (avoids double-arming)

	showHelp   bool       // the key help covers the screen
	helpScroll int        // the help's top row, when it is taller than the screen
	cmdHistory []string   // the command line's history for this run
	popups     popupStack // the open popups; the last one takes the keys
	hovered    string     // the key of the item the hover shows; "" when closed

	keys      *keymap    // resolved keymaps; nil in models built without withKeymaps
	kittyKeys bool       // the terminal reported the Kitty keyboard protocol
	mouse     bool       // the mouse is captured on every screen
	hits      *hitMap    // what the last frame drew that takes the mouse
	drag      dragTarget // the divider that the mouse drags

	keyBuf    []tea.KeyPressMsg // keys of a pending sequence
	keyMatch  string            // id of the complete match inside keyBuf, "" when none
	keyMatchN int               // keys of keyBuf that keyMatch covers
	keyGen    int               // bumped on every wait; a keyTimeoutMsg from an older wait is stale

	logs *logbuf.Buffer // the embedded node's log lines; nil without one
}

type transcriptState struct {
	entries     []transcript.Entry
	err         error
	cursor      int                 // selected entry index
	scroll      int                 // top line offset into the rendered transcript
	detailStack []detailFrame       // deepest = active
	expanded    map[string]bool     // entry id -> expanded
	rows        map[string]rowEntry // rendered entry lines, keyed by entry id
}

// renderCache is what the transcript and the dock draw markdown and code with.
type renderCache struct {
	mdRenderers map[int]*glamour.TermRenderer // markdown renderers, keyed by wrap width
	mdCache     map[string]string             // markdown cache, keyed by width+content
	jsonHL      *codeHighlighter              // JSON syntax highlighter for tool bodies
	jsHL        *codeHighlighter              // JavaScript highlighter for opencode execute
}

func newRenderCache(hasDark bool) renderCache {
	return renderCache{
		mdRenderers: make(map[int]*glamour.TermRenderer),
		mdCache:     make(map[string]string),
		jsonHL:      newCodeHighlighter(hasDark, "json"),
		jsHL:        newCodeHighlighter(hasDark, "javascript"),
	}
}

// historyState is the open past-session transcript: its project and the
// address its reads go to.
type historyState struct {
	project     session.HistoryProject
	title       string                 // header for the open historical transcript
	openSession session.HistorySession // retained for export metadata
	// openNodeID/openPath/openAgent route per-tool detail fetches to the right adapter.
	openNodeID    string
	openPath      string
	openAgent     string // owning agent of the open transcript, for read routing
	openSessionID string // agent session id of the open transcript, for resume
	openResumable bool   // whether the open session can be resumed
}

func newModel(client Client, hasDark bool, logs *logbuf.Buffer) model {
	return model{
		main:            backStack{homeComp{}},
		hits:            &hitMap{},
		client:          client,
		hasDark:         hasDark,
		logs:            logs,
		termKeyCh:       make(chan termKey, termKeyBuf),
		sessions:        make(map[string]session.Session),
		hosts:           make(map[string]hostEntry),
		transcriptCache: make(map[string]cachedTranscript),
		render:          newRenderCache(hasDark),
		dock:            newDock(),
		left:            leftSidebarState{tree: newProjectTree()},
	}
}

func (m model) sessionInteraction() *session.Interaction {
	return m.sessions[m.liveSessionID()].Interaction
}

// liveSessionID is the session of the topmost live transcript on the main pane:
// the one the header, the dock, the right sidebar, and the attach act on. It is
// derived, not stored, so every pop restores it.
func (m model) liveSessionID() string {
	if i := m.transcriptAt(isLive); i >= 0 {
		return m.main[i].(transcriptComp).sessionID
	}
	return ""
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

// redactState holds queued secrets plus input/confirm state for interactive redaction.
type redactState struct {
	literals    []string
	inputActive bool
	input       textinput.Model
	listActive  bool
	listCursor  int
	listReturn  bool // input was opened from the list (D); reopen it when the input closes
	pendingSave bool
	warnConfirm bool // prepare found un-scrubbable content; require an extra ack before saving
	report      *bundle.Report
	tempPath    string // staged bundle awaiting confirmation; renamed to outPath on save
	outPath     string // resolved sibling bundle path
}
