package tui

import (
	"context"
	"encoding/json"
	"os"
	"slices"
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

func (m model) Init() tea.Cmd {
	if t, ok := m.baseComp().(transcriptComp); ok && m.viewer {
		return tea.Batch(m.fetchHistTranscript(t.history.addr()), m.kittyCheckCmd())
	}
	return tea.Batch(m.refreshCmd(), m.fetchProjects(), m.kittyCheckCmd())
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
	res, cmd := m.update(msg)
	next, ok := res.(model)
	if !ok {
		return res, cmd
	}
	next, mem := next.syncMemory()
	next = next.repairFocus()
	next = next.leaveTree(m.focused)
	next = next.syncDock()
	next, sync := next.syncSidebar()
	spin := next.maybeSpin()
	return next, tea.Batch(cmd, mem, sync, spin)
}

func (m model) syncSidebar() (model, tea.Cmd) {
	ws := m.currentWorkspace()
	var drop tea.Cmd
	if f, ok := m.openFile(); ok && ws != m.right.fileTree.ws && f.ws != ws {
		c := &ctx{m: &m}
		c.closeFile()
		drop = m.apply(c)
	}
	c := &ctx{m: &m}
	var cmd tea.Cmd
	m.right, cmd = m.right.show(c, ws)
	cmd = tea.Batch(drop, cmd, m.apply(c))
	return m, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Markdown wrap width changed; drop cached renderers/output.
		m.render.mdRenderers = make(map[int]*glamour.TermRenderer)
		m.render.mdCache = make(map[string]string)
		if m.idleComposerActive() {
			m.sizeIdleReply()
		}
		return m.updateScreen(m.topScreen(), msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case keyTimeoutMsg:
		return m.keyTimeout(msg)
	case tea.KeyboardEnhancementsMsg:
		m.kittyKeys = msg.SupportsKeyDisambiguation()
		return m, nil
	case kittyCheckMsg:
		return m.kittyCheck(), nil
	case wheelMsg:
		return m.mouseWheel(msg)
	case tea.MouseClickMsg:
		return m.mouseClick(msg.Mouse())
	case tea.MouseMotionMsg:
		return m.mouseMotion(msg.Mouse())
	case tea.MouseReleaseMsg:
		return m.mouseRelease(msg.Mouse())
	case tea.PasteMsg:
		switch {
		case msg.Content == "":
		case len(m.popups) > 0:
			return m.updatePopup(msg)
		case m.redactTyping():
			return m.updateTranscript(msg, isHistory)
		case m.topScreen() >= 0:
			return m.updateScreen(m.topScreen(), msg)
		case m.topSpawn() >= 0:
			return m.updateSpawn(m.topSpawn(), msg)
		case m.dockFocused():
			return m.pasteDock(msg)
		}
	case notificationMsg:
		cmd := m.applyEvent(api.Notification(msg))
		return m, cmd
	case connStateMsg:
		if msg.connected {
			// Reconnected: resync authoritatively; live events resume on their own.
			m.reconnecting = false
			cmd := tea.Batch(m.resyncCmd(), m.loadProjects())
			if t, ok := m.baseComp().(transcriptComp); ok && t.live && t.activeSub.subID != "" {
				ref := t.activeSub
				have := len(m.transcriptCache[ref.key()].chunks)
				cmd = tea.Batch(cmd, m.subscribeCmd(ref, have))
			}
			return m, cmd
		}
		m.reconnecting = true // keep the last-known list visible meanwhile
		return m.updateScreens(msg)
	case sessionsReplacedMsg:
		m.sessions = make(map[string]session.Session, len(msg))
		for _, s := range msg {
			m.sessions[s.ID] = s
		}
		m.dock.pruneReplyDrafts(m.sessions)
		m.reorder()
		return m, nil
	case transcriptMsg:
		return m.updateTranscript(msg, func(t transcriptComp) bool { return t.live && t.sessionID == msg.id })
	case histProjectsMsg, histSessionsMsg:
		return m.updateHistory(msg)
	case projectsTreeMsg:
		res, cmd := m.updateTree(msg)
		res, file := res.(model).updateFile(msg)
		return res, tea.Batch(cmd, file)
	case projectsActionMsg:
		if msg.reloadChanges && msg.err == nil {
			m.right.changes.reload()
		}
		return m.updateTree(msg)
	case branchesMsg, prsMsg, issuesMsg:
		return m.updatePopup(msg)
	case createDoneMsg:
		return m.createDone(msg)
	case changedFilesMsg, commitsMsg, commitFilesMsg, listDirMsg:
		c := &ctx{m: &m}
		var cmd tea.Cmd
		m.right, cmd = m.right.update(c, msg)
		cmd = tea.Batch(cmd, m.apply(c))
		return m, cmd
	case wsDiffMsg, readFileMsg, setupLogMsg, setupLogTickMsg:
		return m.updateFile(msg)
	case histTranscriptMsg:
		return m.updateTranscript(msg, reads(msg.addr))
	case histSubagentMsg:
		return m.updateTranscript(msg, reads(msg.addr))
	case toolDetailMsg:
		return m.updateTranscript(msg, func(t transcriptComp) bool {
			return t.owner() == msg.owner && t.toolBodies[msg.toolID].loading
		})
	case transcriptDeltaMsg:
		return m.updateDelta(msg)
	case spawnNodesMsg:
		nodes := msg.nodes
		if msg.err != nil { // no gateway node list (plain local node); nodeID stays empty
			nodes = nil
		}
		return m.beginSpawn(nodes, msg.projects, msg.cwd)
	case spawnAgentsMsg:
		return m.updateSpawn(m.spawnAt(), msg)
	case spawnResultMsg:
		if msg.err != nil {
			m.flash = "spawn failed: " + msg.err.Error()
		}
		return m, nil
	case killResultMsg:
		if msg.err != nil {
			m.flash = "kill failed: " + msg.err.Error()
		}
		return m, nil
	case resumeResultMsg:
		if msg.err != nil {
			m.flash = "resume failed: " + msg.err.Error()
			return m, nil
		}
		return m.enterSession(msg.sessionID)
	case exportDoneMsg:
		if msg.err != nil {
			m.flash = "export failed: " + msg.err.Error()
		} else {
			m.flash = "exported: " + msg.path
		}
		return m, nil
	case redactPreparedMsg, redactDoneMsg:
		return m.updateTranscript(msg, isHistory)
	case termOpenedMsg:
		return m.updateScreen(m.screenAt(msg.termID), msg)
	case logTickMsg:
		// Returning re-renders, which re-reads the log buffer.
		return m, nil
	case jumpResultMsg:
		// On success the client has already switched away; only surface failures.
		if msg.err != nil {
			m.flash = "jump failed: " + msg.err.Error()
		}
	case spinTickMsg:
		m.spinning = false
		m.spin++
		return m, nil
	}
	return m, nil
}

func (m model) idleComposerActive() bool {
	if !m.dockFocused() {
		return false
	}
	s, ok := m.sessions[m.liveSessionID()]
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

// maybeSpin re-arms the spinner tick, which stops itself (see spinTickMsg).
func (m *model) maybeSpin() tea.Cmd {
	if m.spinning || !m.spinShown() {
		return nil
	}
	m.spinning = true
	return spinTickCmd()
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

func (m model) newSessionCmd() tea.Cmd {
	cwd, _ := os.Getwd()
	return m.fetchSpawnNodes(cwd)
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
		// On success, registry events remove the session.
		return killResultMsg{err: client.Call(api.MethodSessionKill, api.SessionRef{SessionID: id}, nil)}
	}
}

func (m *model) applyEvent(n api.Notification) tea.Cmd {
	if n.Method == api.MethodProjectChanged {
		return m.loadProjects()
	}
	if n.Method == api.MethodTerminalOutput {
		var o api.TerminalOutput
		if json.Unmarshal(n.Params, &o) != nil {
			return nil
		}
		var cmd tea.Cmd
		*m, cmd = m.updateScreen(m.screenAt(o.TermID), o)
		return cmd
	}
	if n.Method == api.MethodTerminalExited {
		var o api.TerminalExited
		if json.Unmarshal(n.Params, &o) != nil {
			return nil
		}
		var cmd tea.Cmd
		*m, cmd = m.updateScreen(m.screenAt(o.TermID), o)
		return cmd
	}
	if n.Method == api.MethodTranscriptDelta {
		var d api.TranscriptDelta
		if json.Unmarshal(n.Params, &d) != nil {
			return nil
		}
		i := m.transcriptAt(streams(d.SubID)) // only an open live transcript's stream
		if i < 0 {
			return nil
		}
		ref := m.main[i].(transcriptComp).activeSub
		return func() tea.Msg { return transcriptDeltaMsg{ref: ref, delta: d} }
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
		// A session in a workspace the tree lacks means a new repo or worktree.
		if ws := ev.Session.WorkspaceID; ws != "" {
			cmd = tea.Batch(cmd, m.left.tree.loadMissing(m.client, ws))
		}
		// An agent that stops working has likely changed files in its workspace.
		if existed && prev.Status == session.StatusWorking && ev.Session.Status != session.StatusWorking &&
			ev.Session.WorkspaceID != "" && ev.Session.WorkspaceID == m.right.changes.ws && m.right.changes.files != nil {
			var refresh tea.Cmd
			m.right.changes, refresh = m.right.changes.refresh(&ctx{m: m})
			cmd = tea.Batch(cmd, refresh)
		}
		// /clear swaps the open session's transcript in place; re-subscribe so the
		// stale (pre-clear) stream is dropped and the new file streams from the start.
		if c := m.resubscribeOnClear(prev, existed, ev.Session); c != nil {
			cmd = tea.Batch(cmd, c)
		}
	case registry.EventRemoved:
		delete(m.sessions, ev.Session.ID)
		delete(m.dock.drafts, ev.Session.ID)
	}
	m.syncPromptDraft() // reset a stale draft if the open session's prompt changed
	m.reorder()
	return cmd
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
	for id, s := range m.sessions {
		if m.shows(s) {
			m.order = append(m.order, id)
		}
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
	clamp := func(c int) int { return max(0, min(c, len(m.order)-1)) }
	present := func(id string) string {
		if _, ok := m.sessions[id]; ok {
			return id
		}
		return ""
	}
	m.memory.home.home.cursor = clamp(m.memory.home.home.cursor)
	switch p := m.rootComp().(type) {
	case homeComp:
		p.cursor, p.killID = clamp(p.cursor), present(p.killID)
		m.main = m.main.replaceAt(0, p)
	case workspaceComp:
		p.cursor, p.killID = min(p.cursor, cursorBottom(len(m.wsSessions(p.ws)))), present(p.killID)
		m.main = m.main.replaceAt(0, p)
	}
}

func activeStatus(s session.Session) bool {
	switch s.Status {
	case session.StatusDiscovered, session.StatusStarting, session.StatusWorking, session.StatusAwaitingInput:
		return true
	}
	return false
}

func (m model) shows(s session.Session) bool {
	return (!m.activeOnly || activeStatus(s)) && sessionMatches(s, m.sessionFilter)
}

func (m *model) toggleActiveOnly() {
	sel := m.rootSessionID()
	m.activeOnly = !m.activeOnly
	m.refilter(sel)
	m.flash = "showing all sessions"
	if m.activeOnly {
		m.flash = "showing active sessions · " + m.showsAllHint()
	}
}

func (m *model) refilter(sel string) {
	m.reorder()
	if i := slices.Index(m.rootIDs(), sel); i >= 0 {
		switch p := m.rootComp().(type) {
		case homeComp:
			p.cursor = i
			m.main = m.main.replaceAt(0, p)
		case workspaceComp:
			p.cursor = i
			m.main = m.main.replaceAt(0, p)
		}
	}
}

func (m model) showsAllHint() string {
	return m.keyText(listKeys.ActiveOnly) + " shows all"
}

func (m model) rootIDs() []string {
	switch p := m.rootComp().(type) {
	case homeComp:
		return m.order
	case workspaceComp:
		ss := m.wsSessions(p.ws)
		ids := make([]string, len(ss))
		for i, s := range ss {
			ids[i] = s.ID
		}
		return ids
	}
	return nil
}

func (m model) rootSessionID() string {
	ids := m.rootIDs()
	var cursor int
	switch p := m.rootComp().(type) {
	case homeComp:
		cursor = p.cursor
	case workspaceComp:
		cursor = p.cursor
	}
	if cursor < len(ids) {
		return ids[cursor]
	}
	return ""
}

// enterSession opens a session's transcript view. It subscribes by session id, so
// a just-resumed session works before discovery adds it to the local list.
// A session already open is replaced, and its streams close.
func (m model) enterSession(id string) (model, tea.Cmd) {
	t := newLiveTranscript(id)
	var cmd tea.Cmd
	if m.inSession() {
		c := &ctx{m: &m}
		c.replaceBase(t)
		cmd = m.apply(c)
	} else {
		m.enterMain(t)
	}
	m.focused = mainPane
	m.dock = m.dock.follow(&ctx{m: &m}, id)
	ref := subRef{subID: newSubID(), sessionID: id, cacheKey: m.cacheKeyFor(id)}
	bind := m.editTranscript(m.baseTop()-1, func(v tview) tea.Cmd { return v.bindStream(ref) })
	return m, tea.Batch(cmd, bind)
}

// planJump is the pure decision behind jump.
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

// killRefusal is "" when kill can remove s: kill its pane, or dismiss a
// paneless presence card.
func killRefusal(s session.Session) string {
	if !s.Controllable() && !dismissable(s) {
		return string(s.Frontend) + " session: terminal control unavailable"
	}
	return ""
}

func killPrompt(s session.Session) string {
	return killVerb(s) + " session " + sessionRef(s) + "? y/n"
}

func sessionRef(s session.Session) string {
	name := s.Name
	if name == "" {
		name = s.Tmux.SessionName
	}
	if name == "" {
		name = paneTag(s)
	}
	if s.Repo == "" {
		return name
	}
	return s.Repo + " · " + name
}

func killVerb(s session.Session) string {
	if s.Controllable() {
		return "kill"
	}
	return "dismiss"
}

// dismissable reports whether a paneless OpenCode session can be removed. Kill is
// unconditional, so status and interaction do not gate it.
func dismissable(s session.Session) bool {
	return s.Agent == "opencode" && !s.Controllable()
}

// quit's batch gives no order, so the program may exit before a close command
// finishes.
func (m model) quit() (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	cmds := make([]tea.Cmd, 0, len(m.main)+2)
	for _, comp := range m.main {
		cmds = append(cmds, comp.close(c))
	}
	cmds = append(cmds, m.apply(c), tea.Quit)
	return m, tea.Batch(cmds...)
}
