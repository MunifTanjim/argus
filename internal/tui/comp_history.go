package tui

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// historyComp browses past sessions on disk: the project list, and a project's
// paginated session list. err is shared by both views. The cursors index the
// filtered rows. ws or projectID scopes it to that workspace's or project's
// folders; neither is Home's History. all holds every folder, projects the
// scope's.
type historyComp struct {
	ws            string
	projectID     string
	all           []session.HistoryProject
	projects      []session.HistoryProject
	projCursor    int
	projQuery     string
	err           error
	inProject     bool                   // the sessions view of project, else the projects view
	project       session.HistoryProject // the project being drilled into
	sessions      []session.HistorySession
	sessCursor    int
	sessQuery     string
	filter        filterPrompt
	hasMore       bool
	loading       bool
	pendingExport bool
}

func (h historyComp) section() string           { return "history" }
func (h historyComp) raw(*ctx) bool             { return h.pendingExport || h.filter.on }
func (h historyComp) spins(*ctx) bool           { return false }
func (h historyComp) fullScreen(*ctx) fullLevel { return notFull }
func (h historyComp) close(*ctx) tea.Cmd        { return nil }
func (h historyComp) layer() layer              { return baseLayer }
func (h historyComp) pageStep(c *ctx) int       { return c.m.listPageStep() }

func openHistory(c *ctx) tea.Cmd {
	c.replaceBase(historyComp{})
	return c.m.fetchHistProjects()
}

func (h historyComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	if h.filter.on {
		var cmd tea.Cmd
		var q string
		h.filter, cmd, q = h.filter.handleKey(msg)
		h = h.setQuery(strings.TrimSpace(q))
		return h, cmd, true
	}
	if h.inProject {
		return h.sessionsKey(c, msg)
	}
	return h.projectsKey(c, msg)
}

func (h historyComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	if t.kind == hitTab {
		if h.ws != "" {
			if wsTab(t.index) != wsTabHistory {
				return h, openWsTab(c, h.ws, wsTab(t.index))
			}
			return h, nil
		}
		if h.projectID != "" {
			if projTab(t.index) != projTabHistory {
				openProjectSummary(c, h.projectID)
			}
			return h, nil
		}
		if c.m.homeTabAt(t.index) == tabHistory {
			return h, nil
		}
		return h, switchHomeTab(c, c.m.homeTabAt(t.index))
	}
	if h.inProject {
		switch {
		case t.index >= len(h.shownSessions()):
		case focused && t.index == h.sessCursor:
			return h, h.openSession(c)
		default:
			h.sessCursor = t.index
		}
		return h, nil
	}
	switch {
	case t.index >= len(h.shownProjects()):
	case focused && t.index == h.projCursor:
		h, cmd := h.openProject(c)
		return h, cmd
	default:
		h.projCursor = t.index
	}
	return h, nil
}

func (h historyComp) wheel(_ *ctx, d int) (component, tea.Cmd) {
	if h.inProject {
		h.sessCursor = cursorBy(h.sessCursor, d, len(h.shownSessions()))
	} else {
		h.projCursor = cursorBy(h.projCursor, d, len(h.shownProjects()))
	}
	return h, nil
}

func (h historyComp) menu(*ctx) []binding {
	if !h.inProject || h.sessCursor >= len(h.shownSessions()) {
		return nil
	}
	return []binding{historySessionsKeys.Resume, transcriptKeys.Export}
}

func (h historyComp) projectsKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k, n := c.m, historyProjectsKeys, len(h.shownProjects())
	var cmd tea.Cmd
	switch {
	case m.matches(msg, k.Up):
		h.projCursor = cursorUp(h.projCursor)
	case m.matches(msg, k.Down):
		h.projCursor = cursorDown(h.projCursor, n)
	case m.matches(msg, k.Top):
		h.projCursor = 0
	case m.matches(msg, k.Bottom):
		h.projCursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		h.projCursor = max(0, h.projCursor-m.cardListPageStep())
	case m.matches(msg, k.HalfDown):
		h.projCursor = min(cursorBottom(n), h.projCursor+m.cardListPageStep())
	case m.matches(msg, k.PageUp):
		h.projCursor = cursorBy(h.projCursor, -2*m.cardListPageStep(), n)
	case m.matches(msg, k.PageDown):
		h.projCursor = cursorBy(h.projCursor, 2*m.cardListPageStep(), n)
	case m.matches(msg, k.Open):
		h, cmd = h.openProject(c)
	case m.matches(msg, k.Refresh):
		h.all, h.projects, h.err = nil, nil, nil
		cmd = m.fetchHistProjects()
	case m.matches(msg, projectsKeys.Filter):
		h.filter, cmd = h.filter.start(h.projQuery)
	case m.matches(msg, k.Back) && h.projQuery != "":
		h = h.setQuery("")
	case m.matches(msg, k.Back):
		h.leave(c)
	case m.matches(msg, listKeys.TabPrev):
		cmd = h.stepTab(c, -1)
	case m.matches(msg, listKeys.TabNext):
		cmd = h.stepTab(c, 1)
	default:
		return h, nil, false
	}
	return h, cmd, true
}

func (h historyComp) openProject(c *ctx) (historyComp, tea.Cmd) {
	shown := h.shownProjects()
	if h.projCursor >= len(shown) {
		return h, nil
	}
	p := shown[h.projCursor]
	h.project, h.inProject = p, true
	h.sessions, h.sessCursor, h.sessQuery, h.hasMore = nil, 0, "", false
	h.err, h.loading = nil, true
	return h, c.m.fetchHistSessions(p.NodeID, p.ProjectDir, 0)
}

func (h historyComp) sessionsKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	c.setFlash("")
	if h.pendingExport {
		h.pendingExport = false
		if msg.String() == "y" {
			return h, h.export(c), true
		}
		return h, nil, true
	}
	m, k, shown := c.m, historySessionsKeys, h.shownSessions()
	n := len(shown)
	var cmd tea.Cmd
	switch {
	case m.matches(msg, k.Up):
		h.sessCursor = cursorUp(h.sessCursor)
	case m.matches(msg, k.Down):
		h.sessCursor = cursorDown(h.sessCursor, n)
	case m.matches(msg, k.Top):
		h.sessCursor = 0
	case m.matches(msg, k.Bottom):
		h.sessCursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		h.sessCursor = max(0, h.sessCursor-m.cardListPageStep())
	case m.matches(msg, k.HalfDown):
		h.sessCursor = min(cursorBottom(n), h.sessCursor+m.cardListPageStep())
	case m.matches(msg, k.PageUp):
		h.sessCursor = cursorBy(h.sessCursor, -2*m.cardListPageStep(), n)
	case m.matches(msg, k.PageDown):
		h.sessCursor = cursorBy(h.sessCursor, 2*m.cardListPageStep(), n)
	case m.matches(msg, k.Open):
		cmd = h.openSession(c)
	case m.matches(msg, k.Resume):
		if h.sessCursor < n {
			cmd = historyResume(c, shown[h.sessCursor], h.project)
		}
	case m.matches(msg, transcriptKeys.Export):
		if h.sessCursor < n {
			h.pendingExport = true
		}
	case m.matches(msg, k.More):
		if h.hasMore && !h.loading {
			h.loading = true
			cmd = m.fetchHistSessions(h.project.NodeID, h.project.ProjectDir, len(h.sessions))
		}
	case m.matches(msg, listKeys.Filter):
		h.filter, cmd = h.filter.start(h.sessQuery)
	case m.matches(msg, k.Back) && h.sessQuery != "":
		h = h.setQuery("")
	case h.ws != "" && m.matches(msg, k.Back):
		h.leave(c)
	case h.ws != "" && m.matches(msg, historyProjectsKeys.Refresh):
		// Back to the folder fetch, so the folder opens again once it lists.
		h.all, h.projects, h.sessions, h.err = nil, nil, nil, nil
		h.inProject, h.loading = false, false
		cmd = m.fetchHistProjects()
	case m.matches(msg, k.Back):
		h.inProject = false
	case h.ws != "" && m.matches(msg, listKeys.TabPrev):
		cmd = h.stepTab(c, -1)
	case h.ws != "" && m.matches(msg, listKeys.TabNext):
		cmd = h.stepTab(c, 1)
	default:
		return h, nil, false
	}
	return h, cmd, true
}

// openSession opens the cursor's transcript over the History tab; back from it
// returns here.
func (h historyComp) openSession(c *ctx) tea.Cmd {
	shown := h.shownSessions()
	if h.sessCursor >= len(shown) {
		return nil
	}
	t := newHistoryTranscript(h.project, shown[h.sessCursor])
	c.open(t)
	return c.m.fetchHistTranscript(t.history.addr())
}

func (h historyComp) export(c *ctx) tea.Cmd {
	shown := h.shownSessions()
	if h.sessCursor >= len(shown) || shown[h.sessCursor].TranscriptPath == "" {
		c.setFlash("export: no session open")
		return nil
	}
	s := shown[h.sessCursor]
	c.setFlash("exporting…")
	return c.m.exportCmd(s.Agent, s.TranscriptPath, h.project.NodeID, historyExportMetadata(s, h.project))
}

func (h historyComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case histProjectsMsg:
		h.all, h.err = groupProjectsByNode(msg.projects), msg.err
		h.projects = c.m.scopeFolders(h, h.all)
		if n := len(h.shownProjects()); h.projCursor >= n {
			h.projCursor = max(0, n-1)
		}
		// A workspace has one folder at most: its History is that folder's
		// sessions.
		if h.ws != "" && !h.inProject && len(h.projects) == 1 {
			return h.openProject(c)
		}
	case histSessionsMsg:
		if msg.projectDir != h.project.ProjectDir {
			break
		}
		h.loading = false
		if msg.err != nil {
			h.err = msg.err
			break
		}
		h.err = nil
		if msg.offset == 0 {
			h.sessions = msg.page.Items
		} else {
			h.sessions = append(h.sessions, msg.page.Items...)
		}
		h.hasMore = msg.page.HasMore
		if n := len(h.shownSessions()); h.sessCursor >= n {
			h.sessCursor = max(0, n-1)
		}
	}
	return h, nil
}

func (h historyComp) view(c *ctx, w, ht int) string {
	if h.inProject {
		return h.sessionsView(c, w, ht)
	}
	return h.projectsView(c, w, ht)
}

func (h historyComp) footer(c *ctx) []binding {
	if h.ws != "" && h.inProject {
		return append([]binding{listKeys.TabNext}, append(h.sessionsFooter(), historyProjectsKeys.Refresh)...)
	}
	if !h.inProject {
		return []binding{listKeys.TabNext, historyProjectsKeys.Bottom, projectsKeys.Filter,
			clearFilterKey(historyProjectsKeys.Back, h.projQuery != ""), historyProjectsKeys.Refresh, projectsKeys.Help}
	}
	return h.sessionsFooter()
}

func (h historyComp) sessionsFooter() []binding {
	binds := []binding{historySessionsKeys.Bottom, historySessionsKeys.Resume, transcriptKeys.Export, listKeys.Filter,
		clearFilterKey(historySessionsKeys.Back, h.sessQuery != "")}
	if h.hasMore {
		binds = append(binds, historySessionsKeys.More)
	}
	return binds
}

func (h historyComp) footerPrompt(*ctx) string {
	if h.filter.on {
		return h.filter.view()
	}
	return exportPrompt(h.pendingExport)
}

func (h historyComp) query() string {
	if h.inProject {
		return h.sessQuery
	}
	return h.projQuery
}

// setQuery filters the open view and puts its cursor on the first row.
func (h historyComp) setQuery(q string) historyComp {
	if h.inProject {
		if q != h.sessQuery {
			h.sessQuery, h.sessCursor = q, 0
		}
		return h
	}
	if q != h.projQuery {
		h.projQuery, h.projCursor = q, 0
	}
	return h
}

func (h historyComp) shownProjects() []session.HistoryProject {
	if h.projQuery == "" {
		return h.projects
	}
	var out []session.HistoryProject
	for _, p := range h.projects {
		if historyProjectMatches(p, h.projQuery) {
			out = append(out, p)
		}
	}
	return out
}

func (h historyComp) shownSessions() []session.HistorySession {
	if h.sessQuery == "" {
		return h.sessions
	}
	var out []session.HistorySession
	for _, s := range h.sessions {
		if historySessionMatches(s, h.sessQuery) {
			out = append(out, s)
		}
	}
	return out
}

func historyProjectMatches(p session.HistoryProject, q string) bool {
	return containsFold(q, p.Label, p.Cwd, nodeDisplayLabel(p))
}

func historySessionMatches(s session.HistorySession, q string) bool {
	agent, _ := agentLabel(s.Agent)
	return containsFold(q, s.Title, s.FirstMessage, agent, s.ModelName)
}

func containsFold(q string, fields ...string) bool {
	lq := strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), lq) {
			return true
		}
	}
	return false
}

// noMatchHint is the line a filtered view shows when its filter hides every row.
func (h historyComp) noMatchHint(c *ctx, what string, back binding) string {
	return dimStyle.Render("no " + what + " match /" + h.query() + " · " + c.m.keyTextOn(h.section(), back) + " clears")
}

func (h historyComp) filterTitle() string {
	if q := h.query(); q != "" {
		return dimStyle.Render("  /" + q)
	}
	return ""
}

func (h historyComp) projectsView(c *ctx, w, ht int) string {
	m := c.m
	cardW := historyWidth(w)
	title := h.tabsHeader(c, centerGutter(cardW, w)) + h.filterTitle() + "\n" + h.scopeLine(c)
	backHint := m.keyTextOn(h.section(), historyProjectsKeys.Back) + " back"
	if h.err != nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("error: "+h.err.Error())+"\n\n"+dimStyle.Render(backHint), cardW, w)
	}
	if h.all == nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("loading projects…"), cardW, w)
	}
	if len(h.projects) == 0 {
		return centerBlock(title+"\n\n"+dimStyle.Render(h.emptyText())+"\n\n"+dimStyle.Render(backHint), cardW, w)
	}
	shown := h.shownProjects()
	if len(shown) == 0 {
		return centerBlock(title+"\n\n"+h.noMatchHint(c, "projects", historyProjectsKeys.Back), cardW, w)
	}
	cards := make([]string, len(shown))
	prevNode := ""
	for i, p := range shown {
		card := historyProjectRow(p, i == h.projCursor, c.m.mainFocused(), cardW)
		if !h.scoped() && (i == 0 || p.NodeID != prevNode) {
			card = historyNodeHeader(p) + "\n" + card
		}
		prevNode = p.NodeID
		cards[i] = card
	}
	body := renderCardList(c.below(lipgloss.Height(title)+1), "history-projects", cards, h.projCursor, max(1, ht-3-lipgloss.Height(title)))
	return centerBlock(title+"\n\n"+body, cardW, w)
}

func (h historyComp) sessionsView(c *ctx, w, ht int) string {
	m := c.m
	cardW := historyWidth(w)
	title := headerStyle.Render("history · "+h.project.Label) + dimStyle.Render("  "+truncate(h.project.Cwd, 50)) + h.filterTitle()
	if h.ws != "" {
		title = h.tabsHeader(c, centerGutter(cardW, w)) + h.filterTitle() + "\n" + h.scopeLine(c)
	}
	backHint := m.keyTextOn(h.section(), historySessionsKeys.Back) + " back"
	if h.err != nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("error: "+h.err.Error())+"\n\n"+dimStyle.Render(backHint), cardW, w)
	}
	if len(h.sessions) == 0 {
		msg := "loading sessions…"
		if !h.loading {
			msg = "no sessions in this project"
		}
		return centerBlock(title+"\n\n"+dimStyle.Render(msg)+"\n\n"+dimStyle.Render(backHint), cardW, w)
	}
	shown := h.shownSessions()
	if len(shown) == 0 {
		return centerBlock(title+"\n\n"+h.noMatchHint(c, "sessions", historySessionsKeys.Back), cardW, w)
	}
	showAgent := historyMultiAgent(h.sessions)
	cards := make([]string, len(shown))
	for i, s := range shown {
		cards[i] = historySessionRow(s, i == h.sessCursor, c.m.mainFocused(), cardW, showAgent)
	}
	body := renderCardList(c.below(lipgloss.Height(title)+1), "history-sessions", cards, h.sessCursor, max(1, ht-3-lipgloss.Height(title)))
	return centerBlock(title+"\n\n"+body, cardW, w)
}

func (m model) historyAt() int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if _, ok := m.main[i].(historyComp); ok {
			return i
		}
	}
	return -1
}

func (m model) updateHistory(msg tea.Msg) (tea.Model, tea.Cmd) {
	i := m.historyAt()
	if i < 0 {
		return m, nil
	}
	c := &ctx{m: &m}
	comp, cmd := m.main[i].update(c, msg)
	m.main = m.main.replaceAt(i, comp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (h historyComp) commands(*ctx) []binding {
	if h.ws != "" && h.inProject {
		return bindingsOf(historySessionsKeys, transcriptKeys.Export, listKeys.Filter, listKeys.TabPrev, listKeys.TabNext, historyProjectsKeys.Refresh)
	}
	if h.inProject {
		return bindingsOf(historySessionsKeys, transcriptKeys.Export, listKeys.Filter)
	}
	return bindingsOf(historyProjectsKeys, listKeys.TabPrev, listKeys.TabNext, projectsKeys.Filter)
}

func (h historyComp) offers(*ctx) []binding {
	if h.inProject {
		return nil
	}
	return sectionOffers[h.section()].keys
}

func (h historyComp) scoped() bool { return h.ws != "" || h.projectID != "" }

// row is the tree row of h's scope, "" for Home.
func (h historyComp) row() string {
	if h.ws != "" {
		return h.ws
	}
	return h.projectID
}

// scopedHistoryUnder is the scoped History that a past session's transcript
// was opened from.
func (m model) scopedHistoryUnder() (historyComp, bool) {
	if i := m.historyAt(); i >= 0 {
		if h := m.main[i].(historyComp); h.scoped() {
			return h, true
		}
	}
	return historyComp{}, false
}

// historyScopeDirs is the node and the cleaned workspace dirs of h's scope;
// dirs is nil for Home.
func (m model) historyScopeDirs(h historyComp) (nodeID string, dirs map[string]bool) {
	for _, p := range m.left.tree.data {
		for _, w := range p.Workspaces {
			if w.ID == h.ws || p.ID == h.projectID && !w.IsGone {
				if dirs == nil {
					dirs = map[string]bool{}
				}
				nodeID, dirs[filepath.Clean(w.Dir)] = p.NodeID, true
			}
		}
	}
	return nodeID, dirs
}

// scopeFolders keeps the folders of all that are exactly a dir of h's scope,
// on its node. Home keeps all.
func (m model) scopeFolders(h historyComp, all []session.HistoryProject) []session.HistoryProject {
	if !h.scoped() {
		return all
	}
	nodeID, dirs := m.historyScopeDirs(h)
	var out []session.HistoryProject
	for _, p := range all {
		if p.NodeID == nodeID && dirs[filepath.Clean(p.Cwd)] {
			out = append(out, p)
		}
	}
	return out
}

// scopeLine names what the History on screen covers.
func (h historyComp) scopeLine(c *ctx) string {
	switch {
	case h.ws != "":
		for _, p := range c.m.left.tree.data {
			for _, w := range p.Workspaces {
				if w.ID == h.ws {
					return dimStyle.Render("workspace · " + p.Name + " › " + filepath.Base(w.Dir) + "  " + w.Dir)
				}
			}
		}
		return dimStyle.Render("workspace")
	case h.projectID != "":
		p, _ := c.m.findProject(h.projectID)
		dir := p.Root
		if dir == "" {
			dir = p.Dir
		}
		return dimStyle.Render("project · " + p.Name + "  " + dir)
	}
	return dimStyle.Render("all projects · " + plural(len(h.projects), "folder"))
}

func (h historyComp) workspace(*ctx) string { return h.ws }

func (h historyComp) paneHeader(c *ctx, w int) (string, int) {
	col := centerGutter(historyWidth(w), w)
	switch {
	case h.ws != "":
		if r, ok := c.m.wsRow(h.ws); ok {
			return c.m.wsHeader(r), col
		}
	case h.projectID != "":
		if r, ok := (summaryComp{kind: rowProject, id: h.projectID}).row(c); ok {
			return c.m.rowHeader(r), col
		}
	}
	return "", 0
}

func (h historyComp) tabsHeader(c *ctx, x int) string {
	if h.ws != "" {
		return c.m.wsTabsHeader(c, h.ws, wsTabHistory, x)
	}
	if h.projectID != "" {
		c.m.hitProjTabs(c, x)
		return c.m.projTabs(projTabHistory)
	}
	c.m.hitHomeTabs(c, x)
	return c.m.homeTabs(tabHistory)
}

func (h historyComp) stepTab(c *ctx, d int) tea.Cmd {
	if h.ws != "" {
		return stepWsTab(c, h.ws, wsTabHistory, d)
	}
	if h.projectID != "" {
		openProjectSummary(c, h.projectID)
		return nil
	}
	return stepHomeTab(c, tabHistory, d)
}

// leave is back from the top of the History tab: to the scope's first tab.
func (h historyComp) leave(c *ctx) {
	switch {
	case h.ws != "":
		openWorkspaceSessions(c, h.ws)
	case h.projectID != "":
		openProjectSummary(c, h.projectID)
	default:
		c.replaceBase(c.m.homePane())
	}
}

func (h historyComp) emptyText() string {
	switch {
	case h.ws != "":
		return "no past sessions in this workspace"
	case h.projectID != "":
		return "no past sessions in this project"
	}
	return "no past sessions found"
}
