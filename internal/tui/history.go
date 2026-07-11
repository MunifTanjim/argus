package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// histPageSize bounds each historySessions fetch (recent-first, "m" loads more).
const histPageSize = 100

// --- commands -----------------------------------------------------------------

func (m model) fetchHistProjects() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var projects []session.HistoryProject
		err := client.Call(api.MethodSessionsHistoryProjects, nil, &projects)
		return histProjectsMsg{projects: projects, err: err}
	}
}

func (m model) fetchHistSessions(nodeID, projectDir string, offset int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var page session.HistorySessionPage
		err := client.Call(api.MethodSessionsHistorySessions, api.HistorySessionsParams{
			NodeID: nodeID, ProjectDir: projectDir, Limit: histPageSize, Offset: offset,
		}, &page)
		return histSessionsMsg{projectDir: projectDir, offset: offset, page: page, err: err}
	}
}

func (m model) fetchHistTranscript(nodeID, path, agent string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var view transcript.TranscriptView
		err := client.Call(api.MethodSessionsHistoryTranscript, api.HistoryTranscriptParams{
			NodeID: nodeID, TranscriptPath: path, Agent: agent,
		}, &view)
		return histTranscriptMsg{chunks: view.Chunks, err: err}
	}
}

// histSubagentMsg carries a fetched nested subagent transcript for history mode.
type histSubagentMsg struct {
	agentID string
	chunks  []transcript.Chunk
	err     error
}

// fetchHistSubagent one-shot fetches a subagent transcript (history has no live
// subscription).
func (m model) fetchHistSubagent(nodeID, path, agent, agentID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var view transcript.TranscriptView
		err := client.Call(api.MethodSessionsHistoryTranscript, api.HistoryTranscriptParams{
			NodeID: nodeID, TranscriptPath: path, Agent: agent, AgentID: agentID,
		}, &view)
		return histSubagentMsg{agentID: agentID, chunks: view.Chunks, err: err}
	}
}

// --- key handling -------------------------------------------------------------

func (m model) handleHistoryProjectsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if mm, cmd, ok := m.dispatch(msg, historyProjectsTable); ok {
		return mm, cmd
	}
	return m, nil
}

var historyProjectsTable = []keyTableEntry{
	{historyProjectsKeys.Up, model.actHistProjUp},
	{historyProjectsKeys.Down, model.actHistProjDown},
	{historyProjectsKeys.Top, model.actHistProjTop},
	{historyProjectsKeys.Bottom, model.actHistProjBottom},
	{historyProjectsKeys.HalfUp, model.actHistProjHalfUp},
	{historyProjectsKeys.HalfDown, model.actHistProjHalfDown},
	{historyProjectsKeys.Open, model.actHistProjOpen},
	{historyProjectsKeys.Refresh, model.actHistProjRefresh},
	{listKeys.TabPrev, model.actHistProjBack}, // left/h → Sessions tab
	{listKeys.TabNext, model.actOpenLogs},     // right/l → Logs tab (when spawned)
	{historyProjectsKeys.Back, model.actHistProjBack},
}

func (m model) actHistProjUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.projCursor = cursorUp(m.history.projCursor)
	return m, nil
}

func (m model) actHistProjDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.projCursor = cursorDown(m.history.projCursor, len(m.history.projects))
	return m, nil
}

func (m model) actHistProjTop(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.projCursor = 0
	return m, nil
}

func (m model) actHistProjBottom(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.projCursor = cursorBottom(len(m.history.projects))
	return m, nil
}

func (m model) actHistProjHalfUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.projCursor = max(0, m.history.projCursor-m.cardListPageStep())
	return m, nil
}

func (m model) actHistProjHalfDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.projCursor = min(cursorBottom(len(m.history.projects)), m.history.projCursor+m.cardListPageStep())
	return m, nil
}

func (m model) actHistProjRefresh(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.projects, m.history.err = nil, nil
	return m, m.fetchHistProjects()
}

func (m model) actHistProjBack(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.mode = modeList
	return m, nil
}

func (m model) actHistProjOpen(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.history.projCursor >= len(m.history.projects) {
		return m, nil
	}
	p := m.history.projects[m.history.projCursor]
	m.history.project = p
	m.history.sessions, m.history.sessCursor, m.history.hasMore = nil, 0, false
	m.history.err, m.history.loading = nil, true
	m.mode = modeHistorySessions
	return m, m.fetchHistSessions(p.NodeID, p.ProjectDir, 0)
}

func (m model) handleHistorySessionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.flash = "" // any key dismisses a transient flash; the action may re-set it
	if mm, cmd, ok := m.dispatch(msg, historySessionsTable); ok {
		return mm, cmd
	}
	return m, nil
}

var historySessionsTable = []keyTableEntry{
	{historySessionsKeys.Up, model.actHistSessUp},
	{historySessionsKeys.Down, model.actHistSessDown},
	{historySessionsKeys.Top, model.actHistSessTop},
	{historySessionsKeys.Bottom, model.actHistSessBottom},
	{historySessionsKeys.HalfUp, model.actHistSessHalfUp},
	{historySessionsKeys.HalfDown, model.actHistSessHalfDown},
	{historySessionsKeys.Open, model.actHistSessOpen},
	{historySessionsKeys.Resume, model.actHistSessResume},
	{historySessionsKeys.More, model.actHistSessMore},
	{historySessionsKeys.Back, model.actHistSessBack},
}

func (m model) actHistSessUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.sessCursor = cursorUp(m.history.sessCursor)
	return m, nil
}

func (m model) actHistSessDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.sessCursor = cursorDown(m.history.sessCursor, len(m.history.sessions))
	return m, nil
}

func (m model) actHistSessTop(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.sessCursor = 0
	return m, nil
}

func (m model) actHistSessBottom(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.sessCursor = cursorBottom(len(m.history.sessions))
	return m, nil
}

func (m model) actHistSessHalfUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.sessCursor = max(0, m.history.sessCursor-m.cardListPageStep())
	return m, nil
}

func (m model) actHistSessHalfDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.history.sessCursor = min(cursorBottom(len(m.history.sessions)), m.history.sessCursor+m.cardListPageStep())
	return m, nil
}

func (m model) actHistSessBack(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.mode = modeHistoryProjects
	return m, nil
}

func (m model) actHistSessMore(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.history.hasMore && !m.history.loading {
		m.history.loading = true
		return m, m.fetchHistSessions(m.history.project.NodeID, m.history.project.ProjectDir, len(m.history.sessions))
	}
	return m, nil
}

func (m model) actHistSessResume(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.history.sessCursor >= len(m.history.sessions) {
		return m, nil
	}
	s := m.history.sessions[m.history.sessCursor]
	return m.startHistoryResume(s.Resumable, m.history.project.NodeID, s.Agent, s.SessionID)
}

// startHistoryResume gates a resume on the session's resumability and a known
// working directory, flashing the reason when it can't proceed.
func (m model) startHistoryResume(resumable bool, nodeID, agent, sessionID string) (tea.Model, tea.Cmd) {
	if !resumable {
		m.flash = "resume not supported for this session"
		return m, nil
	}
	if m.history.project.Cwd == "" {
		m.flash = "resume unavailable: unknown working directory"
		return m, nil
	}
	return m, m.resumeCmd(nodeID, agent, sessionID, m.history.project.Cwd)
}

func (m model) actHistSessOpen(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.history.sessCursor >= len(m.history.sessions) {
		return m, nil
	}
	s := m.history.sessions[m.history.sessCursor]
	m.history.title = historySessionTitle(s)
	// Address for follow-up per-tool detail fetches on this transcript.
	m.history.openNodeID, m.history.openPath, m.history.openAgent = m.history.project.NodeID, s.TranscriptPath, s.Agent
	m.history.openSessionID, m.history.openResumable = s.SessionID, s.Resumable
	m.transcript.chunks, m.transcript.err = nil, nil
	m.transcript.cursor, m.transcript.scroll = 0, 0
	m.transcript.detailStack = nil
	m.historyView = histTranscript
	m.transcript.expanded = make(map[string]bool)
	m.toolBodies = make(map[string]toolBodyEntry) // per-transcript tool-body cache
	m.mode = modeHistoryTranscript
	// Transcript lives on the project's node (session items carry no id).
	return m, m.fetchHistTranscript(m.history.project.NodeID, s.TranscriptPath, s.Agent)
}

func (m model) handleHistoryTranscriptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.flash = "" // any key dismisses a transient flash; the action may re-set it
	if key.Matches(msg, transcriptKeys.Back) {
		if m.historyView == histDetail {
			if m.popDetail() { // root frame → back to transcript
				m.historyView = histTranscript
			}
			return m, nil
		}
		m.mode = modeHistorySessions
		return m, nil
	}
	if key.Matches(msg, transcriptKeys.Resume) && m.historyView == histTranscript {
		return m.startHistoryResume(m.history.openResumable, m.history.openNodeID, m.history.openAgent, m.history.openSessionID)
	}
	if m.historyView == histDetail {
		return m.handleDetailKey(msg)
	}
	return m.handleTranscriptKey(msg)
}

// --- views --------------------------------------------------------------------

func (m model) historyProjectsView() string {
	title := Icon.Claude.Render() + " " + headerStyle.Render("argus") + "    " + m.homeTabs(modeHistoryProjects)
	cardW := historyWidth(m)
	if m.history.err != nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("error: "+m.history.err.Error())+"\n\n"+dimStyle.Render("esc back"), cardW, m.width)
	}
	if m.history.projects == nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("loading projects…"), cardW, m.width)
	}
	if len(m.history.projects) == 0 {
		return centerBlock(title+"\n\n"+dimStyle.Render("no past sessions found")+"\n\n"+dimStyle.Render("esc back"), cardW, m.width)
	}
	cards := make([]string, len(m.history.projects))
	prevNode := ""
	for i, p := range m.history.projects {
		card := historyProjectRow(p, i == m.history.projCursor, cardW)
		if i == 0 || p.NodeID != prevNode {
			card = historyNodeHeader(p) + "\n" + card
		}
		prevNode = p.NodeID
		cards[i] = card
	}
	body := renderCardList(cards, m.history.projCursor, max(1, m.height-4))
	footer := m.footer(listKeys.TabNext, historyProjectsKeys.Up, historyProjectsKeys.Bottom,
		historyProjectsKeys.Open, historyProjectsKeys.Refresh, historyProjectsKeys.Back)
	return pinFooter(centerBlock(title+"\n\n"+body, cardW, m.width), footer, m.width, m.height)
}

func (m model) historySessionsView() string {
	title := headerStyle.Render("argus · history · "+m.history.project.Label) + dimStyle.Render("  "+truncate(m.history.project.Cwd, 50))
	cardW := historyWidth(m)
	if m.history.err != nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("error: "+m.history.err.Error())+"\n\n"+dimStyle.Render("esc back"), cardW, m.width)
	}
	if len(m.history.sessions) == 0 {
		msg := "loading sessions…"
		if !m.history.loading {
			msg = "no sessions in this project"
		}
		return centerBlock(title+"\n\n"+dimStyle.Render(msg)+"\n\n"+dimStyle.Render("esc back"), cardW, m.width)
	}
	showAgent := historyMultiAgent(m.history.sessions)
	cards := make([]string, len(m.history.sessions))
	for i, s := range m.history.sessions {
		cards[i] = historySessionRow(s, i == m.history.sessCursor, cardW, showAgent)
	}
	body := renderCardList(cards, m.history.sessCursor, max(1, m.height-4))
	binds := []key.Binding{historySessionsKeys.Up, historySessionsKeys.Bottom, historySessionsKeys.Open, historySessionsKeys.Resume}
	if m.history.hasMore {
		binds = append(binds, historySessionsKeys.More)
	}
	binds = append(binds, historySessionsKeys.Back)
	footer := m.footer(binds...)
	if m.flash != "" {
		footer = asstStyle.Render(m.flash)
	}
	return pinFooter(centerBlock(title+"\n\n"+body, cardW, m.width), footer, m.width, m.height)
}

// renderCardList lays out blank-line-separated cards, windowed to avail height
// with the cursor card kept fully visible (mirrors listView).
func renderCardList(cards []string, cursor, avail int) string {
	var lines []string
	curStart, curEnd := 0, 0
	for i, c := range cards {
		if i > 0 {
			lines = append(lines, "") // blank separator between cards
		}
		start := len(lines)
		lines = append(lines, strings.Split(c, "\n")...)
		if i == cursor {
			curStart, curEnd = start, len(lines)
		}
	}
	return strings.Join(windowSpan(lines, curStart, curEnd, avail), "\n")
}

func (m model) historyTranscriptView() string {
	parts := []string{"argus", "history"}
	if lbl := m.history.project.Label; lbl != "" {
		parts = append(parts, lbl)
	}
	header := headerStyle.Render(strings.Join(parts, " · "))
	if m.history.title != "" {
		header += dimStyle.Render("  " + truncate(m.history.title, 60))
	}
	header = centerBlock(indentBlock(header, strings.Repeat(" ", contentPadX)), m.containerWidth(), m.width)
	body := m.historyBody() // reuses live transcript/detail renderers (read-only)
	footer := m.footer(transcriptKeys.ScrollUp, transcriptKeys.TurnNext, transcriptKeys.Fold,
		transcriptKeys.Detail, transcriptKeys.Bottom, transcriptKeys.Resume, transcriptKeys.Back)
	if m.flash != "" {
		footer = asstStyle.Render(m.flash)
	}
	return pinFooter(header+"\n\n"+body, footer, m.width, m.height)
}

// --- row rendering ------------------------------------------------------------

func historyWidth(m model) int {
	w := min(m.containerWidth(), 78)
	if w < 30 {
		w = 30
	}
	return w
}

// historyCardChrome returns a history card's border color and glyphs (heavy bright
// border when selected), matching live session cards.
func historyCardChrome(sel bool) (color.Color, cardChrome) {
	if sel {
		return ColorFocus, cardHeavy
	}
	return ColorBorder, cardRounded
}

func historyProjectRow(p session.HistoryProject, sel bool, w int) string {
	border, chrome := historyCardChrome(sel)
	titleLeft := dimStyle.Render("○") + " " + headlineStyle(sel).Render(p.Label)
	titleRight := dimStyle.Render(relTime(p.LastActivity))

	// Node is shown by the group header above; card carries only counts and path.
	body := []string{
		dimStyle.Render(fmt.Sprintf("%d sessions", p.SessionCount)),
		dimStyle.Render(p.Cwd),
	}
	return cardTitled(titleLeft, titleRight, body, w, border, chrome, "", nil)
}

func historyNodeHeader(p session.HistoryProject) string {
	return Icon.Node.Render() + " " + StyleSecondaryBold.Render(nodeDisplayLabel(p))
}

// nodeDisplayLabel is the human name for a project's origin node, falling back to
// the node id then a local placeholder (direct connections carry no node info).
func nodeDisplayLabel(p session.HistoryProject) string {
	if p.NodeLabel != "" {
		return p.NodeLabel
	}
	if p.NodeID != "" {
		return p.NodeID
	}
	return "this machine"
}

// groupProjectsByNode makes each node's projects contiguous, preserving the
// recency order both across groups (by first occurrence) and within each group.
func groupProjectsByNode(ps []session.HistoryProject) []session.HistoryProject {
	var order []string
	buckets := map[string][]session.HistoryProject{}
	for _, p := range ps {
		if _, ok := buckets[p.NodeID]; !ok {
			order = append(order, p.NodeID)
		}
		buckets[p.NodeID] = append(buckets[p.NodeID], p)
	}
	out := make([]session.HistoryProject, 0, len(ps))
	for _, id := range order {
		out = append(out, buckets[id]...)
	}
	return out
}

func historySessionRow(s session.HistorySession, sel bool, w int, showAgent bool) string {
	border, chrome := historyCardChrome(sel)
	title := historySessionTitle(s)
	titleLeft := dimStyle.Render("○") + " " + headlineStyle(sel).Render(title)
	titleRight := dimStyle.Render(relTime(s.LastActivity))

	var parts []string
	if s.ModelName != "" {
		st := StyleDim
		if sel {
			st = lipgloss.NewStyle().Foreground(modelColorOf(s.ModelColor))
		}
		parts = append(parts, st.Render(s.ModelName))
	}
	if s.TurnCount > 0 {
		parts = append(parts, dimStyle.Render(fmt.Sprintf("%d turns", s.TurnCount)))
	}
	if s.Tokens > 0 {
		parts = append(parts, dimStyle.Render(formatTokens(s.Tokens)))
	}
	if s.DurationMs > 0 {
		parts = append(parts, dimStyle.Render(formatDuration(s.DurationMs)))
	}
	body := []string{strings.Join(parts, dimStyle.Render(" · "))}
	// First-message preview, only when it differs from the title.
	if s.FirstMessage != "" && s.FirstMessage != title {
		body = append(body, StyleDim.Render(s.FirstMessage))
	}
	agentTxt, agentCol := "", color.Color(nil)
	if showAgent {
		agentTxt, agentCol = agentLabel(s.Agent)
	}
	return cardTitled(titleLeft, titleRight, body, w, border, chrome, agentTxt, agentCol)
}

func historyMultiAgent(ss []session.HistorySession) bool {
	seen := ""
	for _, s := range ss {
		if s.Agent == "" || s.Agent == seen {
			continue
		}
		if seen != "" {
			return true
		}
		seen = s.Agent
	}
	return false
}

// headlineStyle renders a card's title: bold/focused when selected, secondary otherwise.
func headlineStyle(sel bool) lipgloss.Style {
	if sel {
		return StylePrimaryBold
	}
	return StyleSecondary
}

// historySessionTitle picks the best label for a past session.
func historySessionTitle(s session.HistorySession) string {
	if s.Title != "" {
		return s.Title
	}
	if s.FirstMessage != "" {
		return s.FirstMessage
	}
	return s.SessionID
}
