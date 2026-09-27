package tui

import (
	"fmt"
	"image/color"
	"strings"

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

func (m model) fetchHistTranscript(addr histAddr) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var view transcript.TranscriptView
		err := client.Call(api.MethodSessionsHistoryTranscript, api.HistoryTranscriptParams{
			NodeID: addr.nodeID, TranscriptPath: addr.path, Agent: addr.agent,
		}, &view)
		return histTranscriptMsg{addr: addr, chunks: view.Chunks, err: err}
	}
}

type histSubagentMsg struct {
	addr    histAddr
	agentID string
	chunks  []transcript.Chunk
	err     error
}

// fetchHistSubagent one-shot fetches a subagent transcript (history has no live
// subscription).
func (m model) fetchHistSubagent(addr histAddr, agentID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var view transcript.TranscriptView
		err := client.Call(api.MethodSessionsHistoryTranscript, api.HistoryTranscriptParams{
			NodeID: addr.nodeID, TranscriptPath: addr.path, Agent: addr.agent, AgentID: agentID,
		}, &view)
		return histSubagentMsg{addr: addr, agentID: agentID, chunks: view.Chunks, err: err}
	}
}

type histAddr struct{ nodeID, path, agent string }

func (h historyState) addr() histAddr {
	return histAddr{nodeID: h.openNodeID, path: h.openPath, agent: h.openAgent}
}

func reads(addr histAddr) func(transcriptComp) bool {
	return func(t transcriptComp) bool { return !t.live && t.history.addr() == addr }
}

// --- key handling -------------------------------------------------------------

// takePendingExport consumes an armed export confirmation: "y" runs the export,
// any other key cancels. ok reports that the key was handled here.
func (m tview) takePendingExport(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.pendingExport {
		return nil, false
	}
	m.pendingExport = false
	if msg.String() == "y" {
		return m.actExportSession(msg), true
	}
	return nil, true
}

func exportPrompt(pending bool) string {
	if !pending {
		return ""
	}
	return asstStyle.Render("export this session? y/n")
}

func historyResume(c *ctx, resumable bool, nodeID, agent, sessionID, cwd string) tea.Cmd {
	if !resumable {
		c.setFlash("resume not supported for this session")
		return nil
	}
	if cwd == "" {
		c.setFlash("resume unavailable: unknown working directory")
		return nil
	}
	return c.m.resumeCmd(nodeID, agent, sessionID, cwd)
}

// --- views --------------------------------------------------------------------

// renderCardList lays out blank-line-separated cards, windowed to avail height
// with the cursor card kept fully visible (mirrors the Home pane).
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

func (m tview) historyTranscriptView() string {
	header := m.c.m.center(indentBlock(m.historyTranscriptHeader(), strings.Repeat(" ", contentPadX)), m.c.m.containerWidth())
	body := m.historyBody() // reuses live transcript/detail renderers (read-only)
	if m.redactListActive() {
		// The list (D) replaces the transcript body so the queued secrets are visible.
		body = m.c.m.center(indentBlock(m.redactListBody(), strings.Repeat(" ", contentPadX)), m.c.m.containerWidth())
	}
	return header + "\n\n" + body
}

func (m tview) historyTranscriptBinds() []binding {
	binds := []binding{transcriptKeys.ScrollUp, transcriptKeys.CardNext, transcriptKeys.Collapse, transcriptKeys.Detail, transcriptKeys.Bottom}
	if !m.c.m.viewer {
		binds = append(binds, transcriptKeys.Resume) // resume is meaningless offline
	}
	return append(binds, transcriptKeys.Back)
}

// historyTranscriptHeader renders the open-transcript header: a manifest-driven
// summary offline (no live node/history breadcrumb), else the history breadcrumb.
func (m tview) historyTranscriptHeader() string {
	if m.c.m.viewer {
		s := m.history.openSession
		title := s.Title
		if title == "" {
			title = s.FirstMessage
		}
		label := m.history.project.Label
		if label == "" {
			label = "session"
		}
		header := headerStyle.Render("argus · " + label)
		if title != "" {
			header += dimStyle.Render("  " + truncate(title, 50))
		}
		if s.ModelName != "" {
			header += dimStyle.Render("  · " + s.ModelName)
		}
		return header
	}
	parts := []string{"history"}
	if lbl := m.history.project.Label; lbl != "" {
		parts = append(parts, lbl)
	}
	header := headerStyle.Render(strings.Join(parts, " · "))
	if m.history.title != "" {
		header += dimStyle.Render("  " + truncate(m.history.title, 60))
	}
	return header
}

// --- row rendering ------------------------------------------------------------

func historyWidth(w int) int {
	return max(30, min(containerWidthOf(w), 78))
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
