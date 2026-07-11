package tui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// resumeSelectTimeout bounds how long the list waits for a just-resumed session to
// appear before dropping the pending auto-select (see clearPendingResumeMsg).
const resumeSelectTimeout = 30 * time.Second

func (m model) Init() tea.Cmd {
	if m.viewer {
		return m.fetchHistTranscript(m.history.openNodeID, m.history.openPath, m.history.openAgent)
	}
	return tea.Batch(m.refreshCmd(), spinResumeCmd())
}

// spinResumeCmd re-arms the list spinner on a timer, so it resumes even when no
// registry event arrives.
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

// resyncCmd fetches the authoritative session list after a reconnect (so sessions
// removed while disconnected are dropped).
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
		if m.mode == modeScreen && m.term != nil {
			cols, rows := m.termDims()
			m.term.Resize(cols, rows)
			return m, m.termResizeCmd(m.termID, cols, rows)
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		switch {
		case msg.Content == "":
		case m.redact.inputActive:
			m.redact.input += msg.Content
			return m, nil
		case m.mode == modeScreen:
			m.sendTermKey(m.termID, []byte(msg.Content))
			return m, nil
		case m.idleComposerActive():
			// Idle reply composer: append the paste verbatim (newlines and all).
			m.prompt.reasonText += msg.Content
		}
	case notificationMsg:
		return m, m.applyEvent(api.Notification(msg))
	case connStateMsg:
		if msg.connected {
			// Reconnected: resync authoritatively; live events resume on their own.
			m.reconnecting = false
			if m.mode == modeSession && m.activeSub.subID != "" {
				ref := m.activeSub
				have := len(m.transcriptCache[ref.key()].chunks)
				return m, tea.Batch(m.resyncCmd(), m.subscribeCmd(ref, have))
			}
			return m, m.resyncCmd()
		}
		m.reconnecting = true // keep the last-known list visible meanwhile
		if m.mode == modeScreen {
			m = m.detachScreen()
			m.flash = "terminal detached"
		}
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
		// Match by agentID, not topFrame(): the user may have drilled into a leaf above this frame.
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
		// On error, file a done entry with empty body so the placeholder clears and we don't retry.
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
			// Subagent delta: update the cache and fold into the owning frame. Match
			// by subID, not topFrame() (the user may have drilled into a leaf above it).
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
		return m, m.beginSpawn(nodes, msg.projects, msg.cwd)
	case spawnAgentsMsg:
		m.applySpawnAgents(msg)
		return m, nil
	case spawnResultMsg:
		if msg.err != nil {
			m.flash = "spawn failed: " + msg.err.Error()
		}
		return m, nil
	case resumeResultMsg:
		if msg.err != nil {
			m.flash = "resume failed: " + msg.err.Error()
			return m, nil
		}
		m.mode = modeList
		m.pendingResumeID = msg.sessionID
		m.selectPendingResume()
		if m.pendingResumeID == "" {
			return m, nil
		}
		// Not in the list yet; time out the pending selection (see clearPendingResumeMsg).
		id := m.pendingResumeID
		return m, tea.Tick(resumeSelectTimeout, func(time.Time) tea.Msg {
			return clearPendingResumeMsg{id: id}
		})
	case clearPendingResumeMsg:
		if m.pendingResumeID == msg.id {
			m.pendingResumeID = ""
		}
		return m, nil
	case exportDoneMsg:
		if msg.err != nil {
			m.flash = "export failed: " + msg.err.Error()
		} else {
			m.flash = "exported: " + msg.path
		}
		return m, nil
	case redactPreparedMsg:
		if msg.err != nil {
			m.flash = "redact failed: " + msg.err.Error()
			return m, nil
		}
		r := msg.report
		m.redact.report = &r
		m.redact.tempPath = msg.tempPath
		m.redact.outPath = msg.outPath
		m.redact.pendingSave = true
		m.redact.warnConfirm = len(r.Warnings) > 0 // extra ack when content can't be scrubbed
		return m, nil
	case redactDoneMsg:
		switch {
		case msg.err != nil:
			m.flash = "redact failed: " + msg.err.Error()
		case msg.sidecar != "":
			m.flash = fmt.Sprintf("redacted with %d warning(s) (secrets remain); see %s",
				len(msg.warnings), filepath.Base(msg.sidecar))
		case len(msg.warnings) > 0:
			m.flash = fmt.Sprintf("redacted with %d warning(s) (secrets remain): %s", len(msg.warnings), msg.path)
		default:
			m.flash = "redacted: " + msg.path
		}
		m.redact.report = nil
		m.redact.warnConfirm = false
		m.redact.tempPath = ""
		return m, nil
	case termOpenedMsg:
		if msg.termID == m.termID && msg.err != nil {
			m.termErr = msg.err
		}
	case logTickMsg:
		// Returning re-renders, which re-reads the log buffer.
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

// idleComposerActive reports whether the idle free-text composer is focused, so
// pastes route into it the same way handlePromptKey routes keystrokes.
func (m model) idleComposerActive() bool {
	if m.mode != modeSession || m.focus != focusDock {
		return false
	}
	s, ok := m.sessions[m.selectedID]
	return ok && s.Interaction != nil && s.Interaction.Kind == session.InteractionIdle
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

// maybeSpin starts the list spinner tick when the list shows a working session and
// none is scheduled. The tick self-stops (see spinTickMsg) and is re-armed by
// spinResumeCmd and registry events.
func (m *model) maybeSpin() tea.Cmd {
	if m.mode == modeList && !m.spinning && m.anyWorking() {
		m.spinning = true
		return spinTickCmd()
	}
	return nil
}

func (m model) sendInputCmd(id, text string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodSessionInput, api.InputParams{SessionID: id, Text: text, Submit: true, Prepare: true}, nil)
		return nil
	}
}

func (m model) spawnCmd(cwd, nodeID, agent, prompt string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodSessionSpawn, api.SpawnParams{
			NodeID: nodeID, Cwd: cwd, Agent: agent, Prompt: prompt,
		}, nil)
		return spawnResultMsg{err: err} // a successful spawn surfaces via registry events
	}
}

func (m model) resumeCmd(nodeID, agent, agentSessionID, cwd string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.ResumeResult
		err := client.Call(api.MethodSessionResume, api.ResumeParams{
			NodeID: nodeID, Agent: agent, AgentSessionID: agentSessionID, Cwd: cwd,
		}, &res)
		return resumeResultMsg{sessionID: res.SessionID, err: err}
	}
}

// fetchSpawnAgents probes the chosen node for launchable agents. Empty nodeID
// (single-node setup) routes to the sole node.
func (m model) fetchSpawnAgents(nodeID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.AgentsListResult
		err := client.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: nodeID}, &res)
		spawnable := make([]api.AgentInfo, 0, len(res.Agents))
		for _, a := range res.Agents {
			if a.Spawnable {
				spawnable = append(spawnable, a)
			}
		}
		return spawnAgentsMsg{nodeID: nodeID, agents: spawnable, err: err}
	}
}

// fetchSpawnNodes asks server.info which nodes can be spawn targets (gateway →
// every node; plain local → just itself). A call error yields no nodes, leaving
// node_id empty for an immediate local spawn.
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
	if n.Method == api.MethodTerminalOutput {
		var o api.TerminalOutput
		if json.Unmarshal(n.Params, &o) != nil {
			return nil
		}
		if o.TermID != m.termID || m.term == nil {
			return nil
		}
		if raw, err := base64.StdEncoding.DecodeString(o.Data); err == nil {
			_, _ = m.term.Write(raw)
		}
		return nil
	}
	if n.Method == api.MethodTerminalExited {
		var o api.TerminalExited
		if json.Unmarshal(n.Params, &o) != nil {
			return nil
		}
		if o.TermID == m.termID && m.mode == modeScreen {
			*m = m.detachScreen() // attach is gone node-side; leave it
			if o.Reason == api.TermExitedEvicted {
				m.flash = "terminal opened elsewhere"
			} else {
				m.flash = "terminal exited"
			}
		}
		return nil
	}
	if n.Method == api.MethodTranscriptDelta {
		var d api.TranscriptDelta
		if json.Unmarshal(n.Params, &d) != nil {
			return nil
		}
		if d.SubID != m.activeSub.subID { // only the active subscription
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
		// /clear swaps the open session's transcript in place; re-subscribe so the
		// stale (pre-clear) stream is dropped and the new file streams from the start.
		if c := m.resubscribeOnClear(prev, existed, ev.Session); c != nil {
			cmd = tea.Batch(cmd, c)
		}
	case registry.EventRemoved:
		delete(m.sessions, ev.Session.ID)
	}
	m.syncPromptDraft() // reset a stale draft if the open session's prompt changed
	m.reorder()
	return tea.Batch(cmd, m.maybeSpin())
}

// bellCmd rings the terminal bell via BEL on stderr (outside the alt-screen frame,
// so it doesn't disturb the UI).
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
		// Awaiting-input first (one cross-host "Needs you" group), then by host
		// (label asc), then id asc. Local sessions share the empty label.
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
	m.selectPendingResume()
}

// selectPendingResume moves the list cursor onto a just-resumed session once it
// is present in the ordered list, clearing the pending id. Freshly spawned
// sessions arrive via registry events, which re-invoke this through reorder.
func (m *model) selectPendingResume() {
	if m.pendingResumeID == "" {
		return
	}
	for i, id := range m.order {
		if id == m.pendingResumeID {
			m.cursor = i
			m.pendingResumeID = ""
			break
		}
	}
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Screen view is a live passthrough (ctrl+c → Claude SIGINT, ctrl+] leaves).
	// Route it before the global quit so ctrl+c reaches Claude.
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

	// modeList: any key dismisses a transient flash before dispatching (the jump
	// action re-sets it afterwards, so it survives to the next keypress).
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

// cardListPageStep is how many cards a half-page jump moves, estimated from the
// viewport height and a card's nominal line count (~5). Shared by all card lists.
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
	ref := subRef{subID: newSubID(), sessionID: m.selectedID, cacheKey: m.cacheKeyFor(m.selectedID)}
	cmd := m.bindStream(ref)
	return m, cmd
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
	return m.enterScreen(m.order[m.cursor])
}

// actListJump jumps the user's tmux client to the selected session's window, or
// sets a flash explaining why the jump was refused (see planJump).
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

// planJump is the pure decision behind actListJump: returns the pane to reveal, or
// an empty pane and a human-readable reason the jump was refused.
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

// sameMachine reports whether a session's tmux pane is on this machine. Empty
// NodeID means a local/embedded node (always this machine).
func sameMachine(s session.Session, hostname string) bool {
	return s.NodeID == "" || s.NodeLabel == hostname || s.NodeID == hostname
}

// clientPaneFor returns this TUI's own tmux pane ($TMUX_PANE) when co-located with
// the session (same tmux server, same machine), else "" — a pane id is meaningless
// on another server, so the guard must not apply.
func clientPaneFor(s session.Session, hostname, tmuxEnv, tmuxPane string) string {
	coLocated := session.TmuxServer(tmux.SocketBaseFromEnv(tmuxEnv)) == s.Tmux.Server && sameMachine(s, hostname)
	if !coLocated {
		return ""
	}
	return tmuxPane
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
// against the caller's own client, so the user's terminal follows; the TUI keeps
// running in its now-background pane.
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
	// While disconnected, "refresh" means "reconnect now" rather than an RPC over a dead connection.
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

func (m model) handleScreenKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isScreenLeave(msg) {
		return m.leaveScreen()
	}
	if b := ptyBytesFor(msg); b != nil {
		m.sendTermKey(m.termID, b)
	}
	return m, nil
}

// isScreenLeave matches ctrl+] independent of how the terminal reports it.
// msg.String() is unreliable (it prioritizes Text), so match on Code+Mod and the
// raw 0x1d control byte instead.
func isScreenLeave(msg tea.KeyPressMsg) bool {
	if msg.Code == ']' && msg.Mod&tea.ModCtrl != 0 {
		return true
	}
	return msg.Code == 0x1d // GS: ctrl+] as a raw control byte
}
