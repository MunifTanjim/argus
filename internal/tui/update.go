package tui

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func (m model) Init() tea.Cmd { return tea.Batch(m.refreshCmd(), spinResumeCmd()) }

// spinResumeCmd fires on a timer to re-arm the list spinner; registry events also
// re-arm it, but this guarantees it resumes even when no event arrives.
func spinResumeCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return spinResumeMsg{} })
}

// refreshCmd asks the node to rescan; results stream back as registry events.
func (m model) refreshCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodSessionsRefresh, nil, nil)
		return nil
	}
}

// resyncCmd fetches the authoritative session list after a reconnect and replaces the
// model's map with it (so sessions removed while disconnected are dropped).
func (m model) resyncCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var sessions []session.Session
		if err := client.Call(api.MethodSessionsList, nil, &sessions); err != nil {
			return nil
		}
		return sessionsReplacedMsg(sessions)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Markdown wrap width changed; drop cached renderers/output.
		m.transcript.mdRenderers = make(map[int]*glamour.TermRenderer)
		m.transcript.mdCache = make(map[string]string)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		switch {
		case msg.Content == "":
		case m.mode == modeScreen:
			// Screen view: paste goes straight to the pane as literal text.
			select {
			case m.keyCh <- paneKey{id: m.selectedID, literal: msg.Content}:
			default:
			}
		case m.idleComposerActive():
			// Idle reply composer: append the paste (newlines and all); the
			// composer renders multi-line and submits it via sendInputCmd.
			m.prompt.reasonText += msg.Content
		}
	case notificationMsg:
		return m, m.applyEvent(api.Notification(msg))
	case connStateMsg:
		if msg.connected {
			// Reconnected: resync authoritatively (drops sessions removed while away);
			// live events resume via the stable Events() stream.
			m.reconnecting = false
			if m.mode == modeSession && m.activeSub.subID != "" {
				ref := m.activeSub
				have := len(m.transcriptCache[ref.key()].chunks)
				return m, tea.Batch(m.resyncCmd(), m.subscribeCmd(ref, have))
			}
			return m, m.resyncCmd()
		}
		m.reconnecting = true // keep the last-known list visible meanwhile
	case sessionsReplacedMsg:
		m.sessions = make(map[string]session.Session, len(msg))
		for _, s := range msg {
			m.sessions[s.ID] = s
		}
		m.reorder()
		return m, m.maybeSpin()
	case transcriptMsg:
		if msg.id == m.selectedID {
			prevID := m.currentChunkID()
			// Tail-follow only if the view was already pinned to the bottom.
			atBottom := m.transcript.scroll >= m.maxScroll()
			m.transcript.chunks, m.transcript.err = msg.chunks, msg.err
			m.restoreChunkCursor(prevID, atBottom)
		}
	case captureMsg:
		if msg.id == m.selectedID {
			m.screen, m.screenErr = msg.screen, msg.err
		}
	case histProjectsMsg:
		m.history.projects, m.history.err = groupProjectsByNode(msg.projects), msg.err
		if m.history.projCursor >= len(m.history.projects) {
			m.history.projCursor = max(0, len(m.history.projects)-1)
		}
	case histSessionsMsg:
		m.history.loading = false
		if msg.err != nil {
			m.history.err = msg.err
			break
		}
		m.history.err = nil
		if msg.offset == 0 {
			m.history.sessions = msg.page.Items
		} else {
			m.history.sessions = append(m.history.sessions, msg.page.Items...)
		}
		m.history.hasMore = msg.page.HasMore
		if m.history.sessCursor >= len(m.history.sessions) {
			m.history.sessCursor = max(0, len(m.history.sessions)-1)
		}
	case histTranscriptMsg:
		m.transcript.chunks, m.transcript.err = msg.chunks, msg.err
		m.transcript.cursor, m.transcript.scroll = 0, 0
	case histSubagentMsg:
		// Fill the deepest pending history-drill frame for this agent. The user
		// may have drilled into a leaf above it, so match by agentID rather than
		// assuming topFrame().
		if msg.err == nil {
			for i := len(m.transcript.detailStack) - 1; i >= 0; i-- {
				if m.transcript.detailStack[i].agentID == msg.agentID {
					m.transcript.detailStack[i].items = flattenTrace(msg.chunks)
					m.transcript.detailStack[i].expandOutputs()
					break
				}
			}
		}
	case toolDetailMsg:
		// File the fetched body (or, on error, a done entry with an empty body so
		// the loading placeholder clears and we don't retry on every render).
		e := toolBodyEntry{done: true}
		if msg.err == nil {
			e.toolInput, e.result, e.resultIsError = msg.detail.ToolInput, msg.detail.Result, msg.detail.ResultIsError
		}
		m.toolBodies[msg.toolID] = e
	case transcriptDeltaMsg:
		if msg.ref.subID != m.activeSub.subID {
			break // stale subscription (view changed)
		}
		if msg.ref.agentID != "" {
			// Subagent delta: update the cache and fold into the frame that owns
			// this subscription. The user may have drilled into a leaf above the
			// subagent frame, so we must not assume topFrame() is the right one.
			m.transcriptCache[msg.ref.key()] = cachedTranscript{chunks: applyDelta(m.transcriptCache[msg.ref.key()].chunks, msg.delta)}
			for i := range m.transcript.detailStack {
				if m.transcript.detailStack[i].subID == msg.delta.SubID {
					m.transcript.detailStack[i].items = flattenTrace(m.transcriptCache[msg.ref.key()].chunks)
					m.transcript.detailStack[i].expandOutputs()
					break
				}
			}
			break
		}
		prevID := m.currentChunkID()
		atBottom := m.transcript.scroll >= m.maxScroll()
		m.transcript.chunks = applyDelta(m.transcript.chunks, msg.delta)
		m.transcriptCache[msg.ref.key()] = cachedTranscript{chunks: m.transcript.chunks}
		m.restoreChunkCursor(prevID, atBottom)
	case spawnNodesMsg:
		nodes := msg.nodes
		if msg.err != nil { // no gateway node list (plain local node); nodeID stays empty
			nodes = nil
		}
		m.beginSpawn(nodes, msg.projects, msg.cwd)
		return m, nil
	case screenTickMsg:
		if m.mode == modeScreen {
			return m, tea.Batch(m.fetchCapture(m.selectedID), screenTickCmd())
		}
	case logTickMsg:
		// New log lines arrived; returning re-renders, which re-reads the buffer.
		return m, nil
	case jumpResultMsg:
		// On success the client has already switched away; only surface failures.
		if msg.err != nil {
			m.flash = "jump failed: " + msg.err.Error()
		}
	case spinResumeMsg:
		return m, tea.Batch(spinResumeCmd(), m.maybeSpin())
	case spinTickMsg:
		m.spinning = false
		m.spin++
		return m, m.maybeSpin()
	}
	return m, nil
}

// idleComposerActive reports whether the idle free-text reply composer is the
// focused input (session dock focused on an idle interaction). Used to route
// pastes into the composer the same way handlePromptKey routes keystrokes.
func (m model) idleComposerActive() bool {
	if m.mode != modeSession || m.focus != focusDock {
		return false
	}
	s, ok := m.sessions[m.selectedID]
	return ok && s.Interaction != nil && s.Interaction.Kind == session.InteractionIdle
}

func screenTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return screenTickMsg{} })
}

func spinTickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return spinTickMsg{} })
}

// anyWorking reports whether any session is actively working (so the list spinner
// has something to animate).
func (m model) anyWorking() bool {
	for _, s := range m.sessions {
		if s.Status == session.StatusWorking {
			return true
		}
	}
	return false
}

// maybeSpin starts the list spinner tick when the list is showing a working session
// and a tick isn't already scheduled. The tick self-stops once those conditions no
// longer hold (see the spinTickMsg handler), and is restarted by the periodic
// resume tick (spinResumeCmd) and by registry events.
func (m *model) maybeSpin() tea.Cmd {
	if m.mode == modeList && !m.spinning && m.anyWorking() {
		m.spinning = true
		return spinTickCmd()
	}
	return nil
}

func (m model) fetchCapture(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.CaptureResult
		err := client.Call(api.MethodSessionCapture, api.SessionRef{SessionID: id}, &r)
		return captureMsg{id: id, screen: r.Screen, err: err}
	}
}

func (m model) sendInputCmd(id, text string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodSessionInput, api.InputParams{SessionID: id, Text: text, Submit: true, Prepare: true}, nil)
		// Recapture immediately for snappy feedback.
		var r api.CaptureResult
		err := client.Call(api.MethodSessionCapture, api.SessionRef{SessionID: id}, &r)
		return captureMsg{id: id, screen: r.Screen, err: err}
	}
}

func (m model) spawnCmd(cwd, nodeID, prompt string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodSessionSpawn, api.SpawnParams{
			NodeID: nodeID, Cwd: cwd, Prompt: prompt,
		}, nil)
		return nil // registry events will surface the new session
	}
}

// fetchSpawnNodes asks which nodes can be a spawn target (via server.info) so New
// can route (and gate) the spawn. A gateway returns every connected node; a plain
// local node returns just itself (empty ID) with its tmux/spawn capability. A call
// error yields no nodes, leaving node_id empty for an immediate local spawn.
func (m model) fetchSpawnNodes(cwd string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var info api.ServerInfo
		err := client.Call(api.MethodServerInfo, nil, &info)
		var projects []session.HistoryProject
		_ = client.Call(api.MethodSessionsHistoryProjects, nil, &projects)
		return spawnNodesMsg{nodes: info.Nodes, projects: projects, cwd: cwd, err: err}
	}
}

func (m model) killCmd(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodSessionKill, api.SessionRef{SessionID: id}, nil)
		return nil // registry events will remove it
	}
}

func (m *model) applyEvent(n api.Notification) tea.Cmd {
	if n.Method == api.MethodTranscriptDelta {
		var d api.TranscriptDelta
		if json.Unmarshal(n.Params, &d) != nil {
			return nil
		}
		// Match by sub_id to the active subscription.
		if d.SubID != m.activeSub.subID {
			return nil
		}
		return func() tea.Msg { return transcriptDeltaMsg{ref: m.activeSub, delta: d} }
	}
	if n.Method != api.MethodSessionEvent {
		return nil
	}
	var ev registry.Event
	if err := json.Unmarshal(n.Params, &ev); err != nil {
		return nil
	}
	var cmd tea.Cmd
	switch ev.Type {
	case registry.EventAdded, registry.EventUpdated:
		// Ring the terminal bell once on the edge into awaiting-input.
		prev, existed := m.sessions[ev.Session.ID]
		if ev.Session.Status == session.StatusAwaitingInput &&
			(!existed || prev.Status != session.StatusAwaitingInput) {
			cmd = bellCmd()
		}
		m.sessions[ev.Session.ID] = ev.Session
	case registry.EventRemoved:
		delete(m.sessions, ev.Session.ID)
	}
	m.syncPromptDraft() // reset a stale draft if the open session's prompt changed
	m.reorder()
	return tea.Batch(cmd, m.maybeSpin())
}

// bellCmd rings the terminal bell by writing BEL to stderr, which is outside the
// alt-screen frame so it doesn't disturb the rendered UI.
func bellCmd() tea.Cmd {
	return func() tea.Msg {
		shell.StdErr("\a")
		return nil
	}
}

func (m *model) reorder() {
	m.order = m.order[:0]
	for id := range m.sessions {
		m.order = append(m.order, id)
	}
	sort.Slice(m.order, func(i, j int) bool {
		a, b := m.sessions[m.order[i]], m.sessions[m.order[j]]
		// Awaiting-input sessions surface first in one cross-host "Needs you" group
		// (rendered by listView), matching the mobile app. Then sessions group by
		// host (label asc), id ascending within a host. Local sessions share the
		// empty label.
		ai := a.Status == session.StatusAwaitingInput
		bi := b.Status == session.StatusAwaitingInput
		if ai != bi {
			return ai
		}
		if a.NodeLabel != b.NodeLabel {
			return a.NodeLabel < b.NodeLabel
		}
		return a.ID < b.ID
	})
	if m.cursor >= len(m.order) {
		m.cursor = max(0, len(m.order)-1)
	}
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The screen view is a live terminal passthrough: every key goes to the pane
	// (incl. esc → Claude interrupt, ctrl+c → Claude SIGINT); ctrl+] leaves. Route
	// it before the global quit so ctrl+c reaches Claude.
	if m.mode == modeScreen {
		return m.handleScreenKey(msg)
	}
	// Ctrl+C quits from any other mode.
	if msg.String() == "ctrl+c" {
		if m.activeSub.subID != "" {
			subID := m.activeSub.subID
			m.activeSub = subRef{}
			return m, tea.Batch(m.unsubscribeCmd(subID), tea.Quit)
		}
		return m, tea.Quit
	}
	// Kill confirmation gate (list view).
	if m.pendingKill {
		m.pendingKill = false
		if msg.String() == "y" && m.cursor < len(m.order) {
			return m, m.killCmd(m.order[m.cursor])
		}
		return m, nil
	}
	// Spawn flow gate (list view).
	if m.spawn.active() {
		return m.handleSpawnKey(msg)
	}

	// The composite session screen owns its own navigation/fold/compose keys.
	if m.mode == modeSession {
		return m.handleSessionKey(msg)
	}
	// History modes (read-only browsing) own their own keys.
	switch m.mode {
	case modeHistoryProjects:
		return m.handleHistoryProjectsKey(msg)
	case modeHistorySessions:
		return m.handleHistorySessionsKey(msg)
	case modeHistoryTranscript:
		return m.handleHistoryTranscriptKey(msg)
	case modeLogs:
		return m.handleLogsKey(msg)
	}

	// modeList: any key dismisses a transient flash before dispatching. The jump
	// action sets it afterwards, so its message survives until the next keypress.
	m.flash = ""
	if mm, cmd, ok := m.dispatch(msg, listTable); ok {
		return mm, cmd
	}
	return m, nil
}

// listTable maps the session-list bindings to their actions (see keys.go).
var listTable = []keyTableEntry{
	{listKeys.Up, model.actListUp},
	{listKeys.Down, model.actListDown},
	{listKeys.Top, model.actListTop},
	{listKeys.Bottom, model.actListBottom},
	{listKeys.HalfUp, model.actListHalfUp},
	{listKeys.HalfDown, model.actListHalfDown},
	{listKeys.Open, model.actListOpen},
	{listKeys.Screen, model.actListScreen},
	{listKeys.Jump, model.actListJump},
	{listKeys.TabNext, model.actListHistory}, // right/l → History tab
	{listKeys.TabPrev, model.actOpenLogs},    // left/h → Logs tab (when spawned)
	{listKeys.New, model.actListNew},
	{listKeys.Kill, model.actListKill},
	{listKeys.Refresh, model.actListRefresh},
	{listKeys.Quit, model.actListQuit},
}

func (m model) actListUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.cursor = cursorUp(m.cursor)
	return m, nil
}

func (m model) actListTop(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.cursor = 0
	return m, nil
}

func (m model) actListBottom(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.cursor = cursorBottom(len(m.order))
	return m, nil
}

func (m model) actListHalfUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.cursor = max(0, m.cursor-m.cardListPageStep())
	return m, nil
}

func (m model) actListHalfDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.cursor = min(cursorBottom(len(m.order)), m.cursor+m.cardListPageStep())
	return m, nil
}

// cardListPageStep is how many cards a half-page jump (ctrl-u/ctrl-d) moves in a
// card list, estimated from the viewport height and a card's nominal line count
// (box edges + two body lines + a blank separator). Shared by the session list
// and the history project/session lists.
func (m model) cardListPageStep() int {
	const cardLines = 5
	return max(1, max(1, m.height-4)/cardLines/2)
}

func (m model) actListDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.cursor = cursorDown(m.cursor, len(m.order))
	return m, nil
}

func (m model) actListOpen(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.order) {
		return m, nil
	}
	m.selectedID = m.order[m.cursor]
	m.mode = modeSession
	m.focus, m.historyView = focusHistory, histTranscript
	m.transcript.err = nil
	m.transcript.detailStack = nil
	m.transcript.expanded = make(map[string]bool)
	m.toolBodies = make(map[string]toolBodyEntry) // per-session tool-body cache
	m.resetPromptState()
	m.prompt.key = interactionKey(m.sessions[m.selectedID].Interaction)
	ref := subRef{subID: newSubID(), sessionID: m.selectedID}
	m.activeSub = ref
	have := len(m.transcriptCache[ref.key()].chunks)
	m.transcript.chunks = m.transcriptCache[ref.key()].chunks // show cached immediately
	// Open pinned to the bottom (newest content); the catch-up delta is then
	// detected at-bottom and keeps tailing. See restoreChunkCursor's follow path.
	m.transcript.cursor = max(0, len(m.transcript.chunks)-1)
	m.transcript.scroll = m.maxScroll()
	return m, m.subscribeCmd(ref, have)
}

func (m model) actListScreen(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.order) {
		return m, nil
	}
	s := m.sessions[m.order[m.cursor]]
	if !s.Controllable() {
		m.flash = string(s.Frontend) + " session: terminal control unavailable"
		return m, nil
	}
	m.selectedID = m.order[m.cursor]
	m.mode = modeScreen
	m.screen, m.screenErr = "", nil
	return m, tea.Batch(m.fetchCapture(m.selectedID), screenTickCmd())
}

// actListJump jumps the user's tmux client to the selected session's window,
// when that session runs on this machine's default tmux server. When the jump
// can't be made (not inside tmux, wrong server, another machine), it sets a
// transient flash explaining why and does nothing else.
func (m model) actListJump(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.order) {
		return m, nil
	}
	s := m.sessions[m.order[m.cursor]]
	host, _ := os.Hostname()
	paneID, reason := planJump(s, host, os.Getenv("TMUX"))
	if reason != "" {
		m.flash = reason
		return m, nil
	}
	return m, jumpCmd(paneID)
}

// planJump is the pure decision behind actListJump: given the selected session,
// this machine's hostname, and the $TMUX value, it returns the pane to reveal,
// or an empty pane and a human-readable reason the jump was refused.
func planJump(s session.Session, hostname, tmuxEnv string) (paneID, reason string) {
	switch {
	case tmuxEnv == "":
		return "", "run argus inside tmux to jump"
	case session.TmuxServer(tmux.SocketBaseFromEnv(tmuxEnv)) != session.TmuxServerDefault:
		return "", "jump only works from the default tmux server"
	case s.Tmux.Server != session.TmuxServerDefault:
		return "", "can't jump: session is on argus's private socket"
	case !sameMachine(s, hostname):
		return "", "can't jump: session is on " + machineLabel(s)
	case !s.Controllable():
		return "", "can't jump: " + string(s.Frontend) + " session has no tmux pane"
	default:
		return s.Tmux.PaneID, ""
	}
}

// sameMachine reports whether a session's tmux pane is on this machine. An empty
// NodeID means a local/embedded node (no gateway), which is always this machine;
// on a gateway, the node's hostname-derived identity must match.
func sameMachine(s session.Session, hostname string) bool {
	return s.NodeID == "" || s.NodeLabel == hostname || s.NodeID == hostname
}

// nodeName picks the human-friendly name for a node: its label, else its id.
func nodeName(label, id string) string {
	if label != "" {
		return label
	}
	return id
}

// machineLabel is a human name for the session's origin node, for flash messages.
func machineLabel(s session.Session) string {
	if n := nodeName(s.NodeLabel, s.NodeID); n != "" {
		return n
	}
	return "another machine"
}

type jumpResultMsg struct{ err error }

// jumpCmd reveals the pane on the local default tmux server. switch-client runs
// against the caller's own client (it inherits $TMUX), so the user's terminal
// follows; the TUI keeps running in its now-background pane.
func jumpCmd(paneID string) tea.Cmd {
	return func() tea.Msg {
		return jumpResultMsg{err: tmux.New("").Reveal(context.Background(), paneID)}
	}
}

func (m model) actListHistory(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.mode = modeHistoryProjects
	m.history.projects, m.history.err, m.history.projCursor = nil, nil, 0
	return m, m.fetchHistProjects()
}

func (m model) actListNew(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cwd, _ := os.Getwd()
	return m, m.fetchSpawnNodes(cwd)
}

func (m model) actListKill(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.order) {
		return m, nil
	}
	s := m.sessions[m.order[m.cursor]]
	if !s.Controllable() {
		m.flash = string(s.Frontend) + " session: terminal control unavailable"
		return m, nil
	}
	m.pendingKill = true
	return m, nil
}

func (m model) actListRefresh(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// While disconnected, "refresh" means "try to reconnect now" rather than an RPC
	// over a dead connection.
	if m.reconnecting {
		m.client.Reconnect()
		return m, nil
	}
	return m, m.refreshCmd()
}

func (m model) actListQuit(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.activeSub.subID != "" {
		subID := m.activeSub.subID
		m.activeSub = subRef{}
		return m, tea.Batch(m.unsubscribeCmd(subID), tea.Quit)
	}
	return m, tea.Quit
}

// handleScreenKey drives the live terminal passthrough: ctrl+] leaves; every other
// key is translated to a tmux key and enqueued (in order) for the sender goroutine.
func (m model) handleScreenKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+]" {
		m.mode = modeList
		return m, nil
	}
	if k, ok := tmuxKeyFor(msg); ok {
		k.id = m.selectedID
		select {
		case m.keyCh <- k: // non-blocking; drop if the buffer is somehow full
		default:
		}
	}
	return m, nil
}

// namedKeys maps Bubble Tea key strings to tmux send-keys key names.
var namedKeys = map[string]string{
	"enter": "Enter", "tab": "Tab", "shift+tab": "BTab", "backspace": "BSpace",
	"esc": "Escape", "escape": "Escape", "delete": "DC", "insert": "IC",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "pgup": "PageUp", "pgdown": "PageDown",
	"space": " ",
}

// tmuxKeyFor translates a key press into a paneKey for tmux send-keys. Printable
// text passes through literally; recognized special/chord keys map to tmux names;
// anything else is ignored (ok=false).
func tmuxKeyFor(msg tea.KeyPressMsg) (paneKey, bool) {
	s := msg.String()
	if n, ok := namedKeys[s]; ok {
		if n == " " {
			return paneKey{literal: " "}, true
		}
		return paneKey{named: n}, true
	}
	// Ctrl / Alt chords: "ctrl+c" → "C-c", "alt+x" → "M-x" (single trailing key).
	if rest, ok := strings.CutPrefix(s, "ctrl+"); ok && len(rest) == 1 {
		return paneKey{named: "C-" + rest}, true
	}
	if rest, ok := strings.CutPrefix(s, "alt+"); ok && len(rest) == 1 {
		return paneKey{named: "M-" + rest}, true
	}
	// Function keys f1..f12 → F1..F12.
	if len(s) >= 2 && s[0] == 'f' {
		if _, err := strconv.Atoi(s[1:]); err == nil {
			return paneKey{named: "F" + s[1:]}, true
		}
	}
	// Plain printable input (letters, digits, punctuation, unicode).
	if msg.Text != "" {
		return paneKey{literal: msg.Text}, true
	}
	return paneKey{}, false
}
