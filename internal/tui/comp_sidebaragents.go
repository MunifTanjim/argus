package tui

import (
	"cmp"
	"image/color"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// agentsSidebarComp is the left sidebar's Agents tab: the persistent agents by
// section. Like the Sessions tab it holds only the cursor (sel, and its index
// at for when sel leaves the list) and its text filter.
type agentsSidebarComp struct {
	sel    string
	at     int
	query  string
	filter filterPrompt
}

func (s agentsSidebarComp) section() string           { return "sidebar-agents" }
func (s agentsSidebarComp) raw(*ctx) bool             { return s.filter.on }
func (s agentsSidebarComp) fullScreen(*ctx) fullLevel { return notFull }
func (s agentsSidebarComp) close(*ctx) tea.Cmd        { return nil }
func (s agentsSidebarComp) offers(*ctx) []binding     { return sectionOffers[s.section()].keys }
func (s agentsSidebarComp) commands(*ctx) []binding   { return sectionLists[s.section()].own }
func (s agentsSidebarComp) pageStep(c *ctx) int       { return c.m.baseComp().pageStep(c) }
func (s agentsSidebarComp) layer() layer              { return baseLayer }

func (s agentsSidebarComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return s, nil }

func (s agentsSidebarComp) spins(c *ctx) bool {
	return slices.ContainsFunc(c.m.pagents, func(a api.PAgentInfo) bool { return a.Status == "working" })
}

func (s agentsSidebarComp) footer(*ctx) []binding {
	return []binding{projectsKeys.SideTabNext, projectsKeys.Enter, agentKeys.Filter,
		clearFilterKey(projectsKeys.Back, s.query != ""), projectsKeys.Refresh}
}

func (s agentsSidebarComp) footerPrompt(*ctx) string {
	if s.filter.on {
		return s.filter.view()
	}
	return ""
}

func (s agentsSidebarComp) titleSuffix(c *ctx) string {
	var t string
	if s.query != "" {
		t = "  /" + s.query
	}
	return StyleDim.Render(t) + c.m.refreshTitle()
}

// agentSection is a section of the Agents tab, in display order.
type agentSection int

const (
	agentSecPrimary agentSection = iota
	agentSecNeedsYou
	agentSecWaiting
	agentSecWorking
	agentSecUnread
	agentSecIdle
	agentSecUnreachable
)

var agentSectionTitles = []string{"Primary", "Needs you", "Waiting", "Working", "Unread", "Idle", "Unreachable"}

func (m model) agentSectionOf(a api.PAgentInfo) agentSection {
	switch {
	case m.pagentPrimaryOf(a):
		return agentSecPrimary
	case a.Status == pagentUnreachable:
		return agentSecUnreachable
	case a.Status == "needs_you":
		return agentSecNeedsYou
	case a.Status == "waiting":
		return agentSecWaiting
	case a.Status == "working":
		return agentSecWorking
	case m.pagentUnread(a.ID):
		return agentSecUnread
	}
	return agentSecIdle
}

type agentGroup struct {
	section agentSection
	agents  []api.PAgentInfo
}

// groups lists the shown agents by section, by name in each; empty sections
// are left out.
func (s agentsSidebarComp) groups(c *ctx) []agentGroup {
	m := c.m
	by := map[agentSection][]api.PAgentInfo{}
	for _, a := range m.pagents {
		if a.Archived || (s.query != "" && !m.agentMatches(a, s.query)) {
			continue
		}
		sec := m.agentSectionOf(a)
		by[sec] = append(by[sec], a)
	}
	var out []agentGroup
	for i := range agentSectionTitles {
		sec := agentSection(i)
		if len(by[sec]) == 0 {
			continue
		}
		slices.SortFunc(by[sec], func(a, b api.PAgentInfo) int {
			return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
		})
		out = append(out, agentGroup{sec, by[sec]})
	}
	return out
}

// agentMatches matches what a row shows.
func (m model) agentMatches(a api.PAgentInfo, q string) bool {
	lq := strings.ToLower(q)
	return strings.Contains(strings.ToLower(a.Name), lq) || strings.Contains(strings.ToLower(m.agentPlace(a)), lq)
}

func (s agentsSidebarComp) ids(c *ctx) []string {
	var ids []string
	for _, g := range s.groups(c) {
		for _, a := range g.agents {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

func (s agentsSidebarComp) index(ids []string) int {
	if i := slices.Index(ids, s.sel); i >= 0 {
		return i
	}
	return max(0, min(s.at, len(ids)-1))
}

func (s agentsSidebarComp) moveTo(ids []string, i int) agentsSidebarComp {
	if len(ids) == 0 {
		return s
	}
	s.sel, s.at = ids[i], i
	return s
}

func (s agentsSidebarComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	k, m := projectsKeys, c.m
	if s.filter.on {
		var cmd tea.Cmd
		s.filter, cmd, s.query = s.filter.handleKey(msg)
		s.query = strings.TrimSpace(s.query)
		return s, cmd, true
	}
	ids := s.ids(c)
	i, n := s.index(ids), len(ids)
	switch {
	case m.matches(msg, k.Back) && s.query != "":
		s.query = ""
	case m.matches(msg, k.Back):
		c.focusOn(mainPane)
	case m.matches(msg, agentKeys.Filter):
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
			return s, openAgent(c, ids[i]), true
		}
	case m.matches(msg, k.Refresh):
		return s, m.loadPAgentsCmd(), true
	default:
		return s, nil, false
	}
	return s, nil, true
}

func (s agentsSidebarComp) click(c *ctx, h hitTarget, focused bool) (component, tea.Cmd) {
	ids := s.ids(c)
	if h.index >= len(ids) {
		return s, nil
	}
	if focused && h.index == s.index(ids) {
		return s, openAgent(c, ids[h.index])
	}
	return s.moveTo(ids, h.index), nil
}

func (s agentsSidebarComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	ids := s.ids(c)
	return s.moveTo(ids, cursorBy(s.index(ids), d, len(ids))), nil
}

func (s agentsSidebarComp) view(c *ctx, w, h int) string {
	m := c.m
	margin := strings.Repeat(" ", screenMargin)
	groups := s.groups(c)
	ids := s.ids(c)
	switch {
	case len(ids) == 0 && s.query != "":
		return margin + StyleDim.Render("no agents match /"+s.query+" · "+m.keyTextOn(s.section(), projectsKeys.Back)+" clears")
	case len(ids) == 0:
		return margin + StyleDim.Render("no agents")
	}
	focused := m.focused == leftSidebar && len(m.popups) == 0
	cur, open := s.index(ids), ""
	if a, ok := m.baseComp().(agentComp); ok {
		open = a.agentID
	}
	l := itemLines{key: "sidebar-agents"}
	i := 0
	for gi, g := range groups {
		if gi > 0 {
			l.text("")
		}
		l.text(margin + StyleDim.Render(agentSectionTitles[g.section]))
		for _, a := range g.agents {
			l.add(i, m.agentRow(a, i == cur, focused, a.ID == open, w))
			i++
		}
	}
	return strings.Join(l.window(c, cur, max(1, h)), "\n")
}

// agentGlyph is an agent's status glyph and colour, in the sessions' colours.
func (m model) agentGlyph(status string) (string, color.Color) {
	switch status {
	case "needs_you":
		return statusGlyph(session.StatusAwaitingInput), awaitingColor(true)
	case "waiting":
		return statusGlyph(session.StatusAwaitingInput), awaitingColor(false)
	case "working":
		return SpinnerFrames[m.spin%len(SpinnerFrames)], ColorOngoing
	case pagentUnreachable:
		return "○", ColorBorder
	}
	return statusGlyph(session.StatusIdle), statusColor(session.StatusIdle)
}

// unreadMark is the accent dot after an item with unread activity.
var unreadMark = lipgloss.NewStyle().Foreground(ColorAccent).Render(" •")

func (m model) agentRow(a api.PAgentInfo, sel, focused, open bool, w int) string {
	glyph, gcolor := m.agentGlyph(a.Status)
	titleStyle := lipgloss.NewStyle()
	switch {
	case a.Status == pagentUnreachable:
		titleStyle = StyleDim
	case open:
		titleStyle = StylePrimaryBold
	}
	textW := max(1, w-2)
	marker := treeMarker(sel, focused)
	name := titleStyle.Render(xansi.Truncate(a.Name, textW, "…"))
	if m.pagentUnread(a.ID) {
		name += unreadMark
	}
	top := marker + lipgloss.NewStyle().Foreground(gcolor).Render(glyph) + " " + name
	bottom := marker + "  " + StyleDim.Render(xansi.Truncate(m.agentPlace(a), textW, "…"))
	return top + "\n" + bottom
}

// agentPlace is an agent's CLI, home node, and thread count.
func (m model) agentPlace(a api.PAgentInfo) string {
	var parts []string
	if name, _ := agentLabel(a.Agent); name != "" {
		parts = append(parts, name)
	} else if a.Agent != "" {
		parts = append(parts, a.Agent)
	}
	if a.NodeLabel != "" {
		parts = append(parts, a.NodeLabel)
	}
	if ts, ok := m.pagentThreads[a.ID]; ok {
		parts = append(parts, plural(len(ts), "thread"))
	}
	return strings.Join(parts, " · ")
}

func (s agentsSidebarComp) hoverItem(c *ctx) (hoverItem, bool) {
	ids := s.ids(c)
	if len(ids) == 0 {
		return hoverItem{}, false
	}
	i := s.index(ids)
	a, _ := c.m.pagent(ids[i])
	it := hoverItem{key: "pagent:" + a.ID, index: i, title: a.Name}
	it.add("Status", strings.ReplaceAll(a.Status, "_", " "))
	it.add("Description", a.Description)
	name, _ := agentLabel(a.Agent)
	it.add("Agent", name)
	it.add("Model", a.Model)
	it.add("Dir", a.Dir)
	it.add("Node", a.NodeLabel)
	it.add("ID", a.ID)
	return it, true
}
