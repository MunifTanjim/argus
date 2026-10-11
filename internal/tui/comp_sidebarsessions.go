package tui

import (
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

// sidebarSessionsComp is the left sidebar's Sessions tab. It reads the
// sessions each time, so it holds only the cursor (the session under it, and
// its index for when that session leaves the list) and its own text filter,
// apart from the home list's.
type sidebarSessionsComp struct {
	sel    string
	at     int
	query  string
	filter filterPrompt
}

func (s sidebarSessionsComp) section() string           { return "sidebar-sessions" }
func (s sidebarSessionsComp) raw(*ctx) bool             { return s.filter.on }
func (s sidebarSessionsComp) fullScreen(*ctx) fullLevel { return notFull }
func (s sidebarSessionsComp) close(*ctx) tea.Cmd        { return nil }
func (s sidebarSessionsComp) offers(*ctx) []binding     { return sectionOffers[s.section()].keys }
func (s sidebarSessionsComp) commands(*ctx) []binding   { return sectionLists[s.section()].own }
func (s sidebarSessionsComp) pageStep(c *ctx) int       { return c.m.baseComp().pageStep(c) }
func (s sidebarSessionsComp) layer() layer              { return baseLayer }

func (s sidebarSessionsComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return s, nil }

func (s sidebarSessionsComp) spins(c *ctx) bool {
	for _, ss := range c.m.sessions {
		if ss.Status == session.StatusWorking && !ss.Offline {
			return true
		}
	}
	return false
}

func (s sidebarSessionsComp) footer(*ctx) []binding {
	return []binding{projectsKeys.SideTabNext, projectsKeys.Enter, listKeys.Filter,
		clearFilterKey(projectsKeys.Back, s.query != "")}
}

func (s sidebarSessionsComp) footerPrompt(*ctx) string {
	if s.filter.on {
		return s.filter.view()
	}
	return ""
}

// groups are the listed sessions: the live ones that match the filter.
func (s sidebarSessionsComp) groups(c *ctx) []sidebarSessionGroup {
	if s.query == "" {
		return sidebarSessionGroups(c.m.sessions)
	}
	shown := map[string]session.Session{}
	for id, ss := range c.m.sessions {
		if c.m.sidebarSessionMatches(ss, s.query) {
			shown[id] = ss
		}
	}
	return sidebarSessionGroups(shown)
}

func (s sidebarSessionsComp) ids(c *ctx) []string { return sidebarSessionIDs(s.groups(c)) }

// sidebarSessionMatches matches what a row shows, besides what the home list's
// filter matches.
func (m model) sidebarSessionMatches(ss session.Session, q string) bool {
	lq := strings.ToLower(q)
	return sessionMatches(ss, q) || strings.Contains(strings.ToLower(sessionLabel(ss)), lq) ||
		strings.Contains(strings.ToLower(m.sidebarSessionPlace(ss, false)), lq)
}

// titleSuffix follows the Sessions tab: its filter.
func (s sidebarSessionsComp) titleSuffix() string {
	if s.query == "" {
		return ""
	}
	return StyleDim.Render("  /" + s.query)
}

// index is the cursor's row: the selected session's, or its last place,
// clamped, once it has left.
func (s sidebarSessionsComp) index(ids []string) int {
	if i := slices.Index(ids, s.sel); i >= 0 {
		return i
	}
	return max(0, min(s.at, len(ids)-1))
}

func (s sidebarSessionsComp) moveTo(ids []string, i int) sidebarSessionsComp {
	if len(ids) == 0 {
		return s
	}
	s.sel, s.at = ids[i], i
	return s
}

func (s sidebarSessionsComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	k, m := projectsKeys, c.m
	ids := s.ids(c)
	i, n := s.index(ids), len(ids)
	if s.filter.on {
		var cmd tea.Cmd
		s.filter, cmd, s.query = s.filter.handleKey(msg)
		s.query = strings.TrimSpace(s.query)
		return s, cmd, true
	}
	switch {
	case m.matches(msg, k.Back) && s.query != "":
		s.query = ""
	case m.matches(msg, k.Back):
		c.focusOn(mainPane)
	case m.matches(msg, listKeys.Filter):
		var cmd tea.Cmd
		s.filter, cmd = s.filter.start(s.query)
		return s, cmd, true
	case m.matches(msg, k.Up):
		s = s.moveTo(ids, cursorUp(i))
	case m.matches(msg, k.Down):
		s = s.moveTo(ids, cursorDown(i, n))
	case m.matches(msg, k.Top):
		s = s.moveTo(ids, 0)
	case m.matches(msg, k.Bottom):
		s = s.moveTo(ids, cursorBottom(n))
	case m.matches(msg, k.HalfUp):
		s = s.moveTo(ids, cursorBy(i, -s.pageStep(c), n))
	case m.matches(msg, k.HalfDown):
		s = s.moveTo(ids, cursorBy(i, s.pageStep(c), n))
	case m.matches(msg, k.Enter):
		if n > 0 {
			c.openSession(ids[i])
		}
	default:
		return s, nil, false
	}
	return s, nil, true
}

func (s sidebarSessionsComp) click(c *ctx, h hitTarget, focused bool) (component, tea.Cmd) {
	ids := s.ids(c)
	if h.index >= len(ids) {
		return s, nil
	}
	if focused && h.index == s.index(ids) {
		c.openSession(ids[h.index])
		return s, nil
	}
	return s.moveTo(ids, h.index), nil
}

func (s sidebarSessionsComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	ids := s.ids(c)
	return s.moveTo(ids, cursorBy(s.index(ids), d, len(ids))), nil
}

// view draws the list under the tab strip.
func (s sidebarSessionsComp) view(c *ctx, w, h int) string {
	m := c.m
	margin := strings.Repeat(" ", screenMargin)
	groups := s.groups(c)
	ids := sidebarSessionIDs(groups)
	switch {
	case len(ids) == 0 && s.query != "":
		return margin + StyleDim.Render("no sessions match /"+s.query+" · "+m.keyTextOn(s.section(), projectsKeys.Back)+" clears")
	case len(ids) == 0:
		return margin + StyleDim.Render("no live sessions")
	}
	focused := m.focused == leftSidebar && len(m.popups) == 0
	cur, open, showAgent := s.index(ids), m.liveSessionID(), m.listedMultiAgent(ids)
	l := itemLines{key: "sidebar-sessions"}
	i := 0
	for gi, g := range groups {
		if gi > 0 {
			l.text("")
		}
		l.text(margin + StyleDim.Render(sidebarSectionTitles[g.section]))
		for _, id := range g.ids {
			l.add(i, m.sidebarSessionRow(m.sessions[id], i == cur, focused, id == open, showAgent, w))
			i++
		}
	}
	return strings.Join(l.window(c, cur, max(1, h)), "\n")
}

// listedMultiAgent reports whether the listed sessions run more than one agent kind.
func (m model) listedMultiAgent(ids []string) bool {
	seen := ""
	for _, id := range ids {
		a := m.sessions[id].Agent
		if a == "" || a == seen {
			continue
		}
		if seen != "" {
			return true
		}
		seen = a
	}
	return false
}

func (m model) sidebarSessionRow(s session.Session, sel, focused, open, showAgent bool, w int) string {
	glyph, gcolor := statusGlyph(s.Status), statusColor(s.Status)
	if s.Status == session.StatusWorking {
		glyph, gcolor = SpinnerFrames[m.spin%len(SpinnerFrames)], ColorOngoing
	}
	titleStyle := lipgloss.NewStyle()
	switch {
	case s.Offline:
		glyph, gcolor, titleStyle = "○", ColorBorder, StyleDim
	case open:
		titleStyle = StylePrimaryBold
	}
	textW := max(1, w-2)
	marker := treeMarker(sel, focused)
	top := marker + lipgloss.NewStyle().Foreground(gcolor).Render(glyph) + " " + titleStyle.Render(xansi.Truncate(sessionLabel(s), textW, "…"))
	bottom := marker + "  " + StyleDim.Render(xansi.Truncate(m.sidebarSessionPlace(s, showAgent), textW, "…"))
	return top + "\n" + bottom
}

// sidebarSessionPlace is where a session runs: its project and workspace (its
// repo when the tree lacks them), and its agent when several kinds run.
func (m model) sidebarSessionPlace(s session.Session, showAgent bool) string {
	var parts []string
	if p, w, ok := m.workspaceOf(s.WorkspaceID); ok {
		parts = append(parts, p.Name+" / "+filepath.Base(w.Dir))
	} else if s.Repo != "" {
		parts = append(parts, s.Repo)
	}
	if name, _ := agentLabel(s.Agent); showAgent && name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, " · ")
}

func (s sidebarSessionsComp) hoverItem(c *ctx) (hoverItem, bool) {
	ids := s.ids(c)
	if len(ids) == 0 {
		return hoverItem{}, false
	}
	i := s.index(ids)
	ss := c.m.sessions[ids[i]]
	it := hoverItem{key: "session:" + ss.ID, index: i, title: sessionLabel(ss)}
	status := ss.StatusLabel
	if status == "" {
		status = string(ss.Status)
	}
	if ss.Offline {
		status += ", offline"
	}
	it.add("Status", status)
	name, _ := agentLabel(ss.Agent)
	it.add("Agent", name)
	if ss.Summary != nil {
		it.add("Task", ss.Summary.Task)
		it.add("Last activity", relTime(ss.Summary.LastActivity))
	}
	it.add("Dir", ss.Cwd)
	it.add("Branch", ss.Branch)
	it.add("Node", ss.NodeLabel)
	if ss.Tmux.PaneID != "" {
		it.add("Tmux", strings.Join(slices.DeleteFunc([]string{ss.Tmux.SessionName, ss.Tmux.PaneID, string(ss.Tmux.Server)},
			func(p string) bool { return p == "" }), " · "))
	}
	it.add("ID", ss.ID)
	return it, true
}
