package tui

import (
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// historyComp browses past sessions on disk: the project list, and a project's
// paginated session list. err is shared by both views.
type historyComp struct {
	projects      []session.HistoryProject
	projCursor    int
	err           error
	inProject     bool                   // the sessions view of project, else the projects view
	project       session.HistoryProject // the project being drilled into
	sessions      []session.HistorySession
	sessCursor    int
	hasMore       bool
	loading       bool
	pendingExport bool
}

func (h historyComp) section() string           { return "history" }
func (h historyComp) raw(*ctx) bool             { return h.pendingExport }
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
	if h.inProject {
		return h.sessionsKey(c, msg)
	}
	return h.projectsKey(c, msg)
}

func (h historyComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	if t.kind == hitTab {
		if c.m.homeTabAt(t.index) == tabHistory {
			return h, nil
		}
		return h, switchHomeTab(c, c.m.homeTabAt(t.index))
	}
	if h.inProject {
		switch {
		case t.index >= len(h.sessions):
		case focused && t.index == h.sessCursor:
			return h, h.openSession(c)
		default:
			h.sessCursor = t.index
		}
		return h, nil
	}
	switch {
	case t.index >= len(h.projects):
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
		h.sessCursor = cursorBy(h.sessCursor, d, len(h.sessions))
	} else {
		h.projCursor = cursorBy(h.projCursor, d, len(h.projects))
	}
	return h, nil
}

func (h historyComp) menu(*ctx) []binding {
	if !h.inProject || h.sessCursor >= len(h.sessions) {
		return nil
	}
	return []binding{historySessionsKeys.Resume, transcriptKeys.Export}
}

func (h historyComp) projectsKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k, n := c.m, historyProjectsKeys, len(h.projects)
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
	case m.matches(msg, k.Open):
		h, cmd = h.openProject(c)
	case m.matches(msg, k.Refresh):
		h.projects, h.err = nil, nil
		cmd = m.fetchHistProjects()
	case m.matches(msg, k.Back):
		c.replaceBase(c.m.homePane())
	case m.matches(msg, listKeys.TabPrev):
		cmd = stepHomeTab(c, tabHistory, -1)
	case m.matches(msg, listKeys.TabNext):
		cmd = stepHomeTab(c, tabHistory, 1)
	default:
		return h, nil, false
	}
	return h, cmd, true
}

func (h historyComp) openProject(c *ctx) (historyComp, tea.Cmd) {
	if h.projCursor >= len(h.projects) {
		return h, nil
	}
	p := h.projects[h.projCursor]
	h.project, h.inProject = p, true
	h.sessions, h.sessCursor, h.hasMore = nil, 0, false
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
	m, k, n := c.m, historySessionsKeys, len(h.sessions)
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
	case m.matches(msg, k.Open):
		cmd = h.openSession(c)
	case m.matches(msg, k.Resume):
		if h.sessCursor < n {
			s := h.sessions[h.sessCursor]
			cmd = historyResume(c, s.Resumable, h.project.NodeID, s.Agent, s.SessionID, h.project.Cwd)
		}
	case m.matches(msg, transcriptKeys.Export):
		if h.sessCursor < n {
			h.pendingExport = true
		}
	case m.matches(msg, k.More):
		if h.hasMore && !h.loading {
			h.loading = true
			cmd = m.fetchHistSessions(h.project.NodeID, h.project.ProjectDir, n)
		}
	case m.matches(msg, k.Back):
		h.inProject = false
	default:
		return h, nil, false
	}
	return h, cmd, true
}

// openSession opens the cursor's transcript over the History tab; back from it
// returns here.
func (h historyComp) openSession(c *ctx) tea.Cmd {
	if h.sessCursor >= len(h.sessions) {
		return nil
	}
	t := newHistoryTranscript(h.project, h.sessions[h.sessCursor])
	c.open(t)
	return c.m.fetchHistTranscript(t.history.addr())
}

func (h historyComp) export(c *ctx) tea.Cmd {
	if h.sessCursor >= len(h.sessions) || h.sessions[h.sessCursor].TranscriptPath == "" {
		c.setFlash("export: no session open")
		return nil
	}
	s := h.sessions[h.sessCursor]
	c.setFlash("exporting…")
	return c.m.exportCmd(s.Agent, s.TranscriptPath, h.project.NodeID, historyExportMetadata(s, h.project))
}

func (h historyComp) update(_ *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case histProjectsMsg:
		h.projects, h.err = groupProjectsByNode(msg.projects), msg.err
		if h.projCursor >= len(h.projects) {
			h.projCursor = max(0, len(h.projects)-1)
		}
	case histSessionsMsg:
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
		if h.sessCursor >= len(h.sessions) {
			h.sessCursor = max(0, len(h.sessions)-1)
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
	if !h.inProject {
		return []binding{listKeys.TabNext, historyProjectsKeys.Bottom, historyProjectsKeys.Refresh, projectsKeys.Help}
	}
	binds := []binding{historySessionsKeys.Bottom, historySessionsKeys.Resume, transcriptKeys.Export}
	if h.hasMore {
		binds = append(binds, historySessionsKeys.More)
	}
	return binds
}

func (h historyComp) footerPrompt(*ctx) string { return exportPrompt(h.pendingExport) }

func (h historyComp) projectsView(c *ctx, w, ht int) string {
	m := c.m
	title := m.homeTabs(tabHistory)
	cardW := historyWidth(w)
	m.hitHomeTabs(c, centerGutter(cardW, w))
	backHint := m.keyText(historyProjectsKeys.Back) + " back"
	if h.err != nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("error: "+h.err.Error())+"\n\n"+dimStyle.Render(backHint), cardW, w)
	}
	if h.projects == nil {
		return centerBlock(title+"\n\n"+dimStyle.Render("loading projects…"), cardW, w)
	}
	if len(h.projects) == 0 {
		return centerBlock(title+"\n\n"+dimStyle.Render("no past sessions found")+"\n\n"+dimStyle.Render(backHint), cardW, w)
	}
	cards := make([]string, len(h.projects))
	prevNode := ""
	for i, p := range h.projects {
		card := historyProjectRow(p, i == h.projCursor, cardW)
		if i == 0 || p.NodeID != prevNode {
			card = historyNodeHeader(p) + "\n" + card
		}
		prevNode = p.NodeID
		cards[i] = card
	}
	body := renderCardList(c.below(2), cards, h.projCursor, max(1, ht-4))
	return centerBlock(title+"\n\n"+body, cardW, w)
}

func (h historyComp) sessionsView(c *ctx, w, ht int) string {
	m := c.m
	title := headerStyle.Render("history · "+h.project.Label) + dimStyle.Render("  "+truncate(h.project.Cwd, 50))
	cardW := historyWidth(w)
	backHint := m.keyText(historySessionsKeys.Back) + " back"
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
	showAgent := historyMultiAgent(h.sessions)
	cards := make([]string, len(h.sessions))
	for i, s := range h.sessions {
		cards[i] = historySessionRow(s, i == h.sessCursor, cardW, showAgent)
	}
	body := renderCardList(c.below(lipgloss.Height(title)+1), cards, h.sessCursor, max(1, ht-4))
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
	if h.inProject {
		return bindingsOf(historySessionsKeys, transcriptKeys.Export)
	}
	return bindingsOf(historyProjectsKeys, listKeys.TabPrev, listKeys.TabNext)
}

func (h historyComp) offers(*ctx) []binding {
	if h.inProject {
		return nil
	}
	return sectionOffers[h.section()].keys
}
