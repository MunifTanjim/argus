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

// botsSidebarComp is the left sidebar's Bots tab: the bots by
// section. Like the Sessions tab it holds only the cursor (sel, and its index
// at for when sel leaves the list) and its text filter.
type botsSidebarComp struct {
	sel    string
	at     int
	query  string
	filter filterPrompt
}

func (s botsSidebarComp) section() string           { return "sidebar-bots" }
func (s botsSidebarComp) raw(*ctx) bool             { return s.filter.on }
func (s botsSidebarComp) fullScreen(*ctx) fullLevel { return notFull }
func (s botsSidebarComp) close(*ctx) tea.Cmd        { return nil }
func (s botsSidebarComp) offers(*ctx) []binding     { return sectionOffers[s.section()].keys }
func (s botsSidebarComp) commands(*ctx) []binding   { return sectionLists[s.section()].own }
func (s botsSidebarComp) pageStep(c *ctx) int       { return c.m.baseComp().pageStep(c) }
func (s botsSidebarComp) layer() layer              { return baseLayer }

func (s botsSidebarComp) update(_ *ctx, msg tea.Msg) (component, tea.Cmd) {
	p, ok := msg.(tea.PasteMsg)
	if !ok || !s.filter.on {
		return s, nil
	}
	var cmd tea.Cmd
	s.filter.input, cmd = s.filter.input.Update(p)
	s.query = strings.TrimSpace(s.filter.input.Value())
	return s, cmd
}

func (s botsSidebarComp) spins(c *ctx) bool {
	return slices.ContainsFunc(c.m.bots, func(a api.BotInfo) bool { return a.Status == "working" })
}

func (s botsSidebarComp) footer(*ctx) []binding {
	return []binding{projectsKeys.SideTabNext, projectsKeys.Enter, botKeys.Filter,
		clearFilterKey(projectsKeys.Back, s.query != ""), projectsKeys.Refresh}
}

func (s botsSidebarComp) footerPrompt(*ctx) string {
	if s.filter.on {
		return s.filter.view()
	}
	return ""
}

func (s botsSidebarComp) titleSuffix(c *ctx) string {
	var t string
	if s.query != "" {
		t = "  /" + s.query
	}
	return StyleDim.Render(t) + c.m.refreshTitle()
}

// botSection is a section of the Bots tab, in display order.
type botSection int

const (
	botSecPrimary botSection = iota
	botSecNeedsYou
	botSecWaiting
	botSecWorking
	botSecUnread
	botSecIdle
	botSecUnreachable
)

var botSectionTitles = []string{"Primary", "Needs you", "Waiting", "Working", "Unread", "Idle", "Unreachable"}

func (m model) botSectionOf(a api.BotInfo) botSection {
	switch {
	case m.botPrimaryOf(a):
		return botSecPrimary
	case a.Status == botUnreachable:
		return botSecUnreachable
	case a.Status == "needs_you":
		return botSecNeedsYou
	case a.Status == "waiting":
		return botSecWaiting
	case a.Status == "working":
		return botSecWorking
	case m.botUnread(a.ID):
		return botSecUnread
	}
	return botSecIdle
}

type botGroup struct {
	section botSection
	bots    []api.BotInfo
}

// groups lists the shown bots by section, by name in each; empty sections
// are left out.
func (s botsSidebarComp) groups(c *ctx) []botGroup {
	m := c.m
	by := map[botSection][]api.BotInfo{}
	for _, a := range m.bots {
		if a.Archived || (s.query != "" && !m.botMatches(a, s.query)) {
			continue
		}
		sec := m.botSectionOf(a)
		by[sec] = append(by[sec], a)
	}
	var out []botGroup
	for i := range botSectionTitles {
		sec := botSection(i)
		if len(by[sec]) == 0 {
			continue
		}
		slices.SortFunc(by[sec], func(a, b api.BotInfo) int {
			return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
		})
		out = append(out, botGroup{sec, by[sec]})
	}
	return out
}

// botMatches matches what a row shows.
func (m model) botMatches(a api.BotInfo, q string) bool {
	lq := strings.ToLower(q)
	return strings.Contains(strings.ToLower(a.Name), lq) || strings.Contains(strings.ToLower(m.botPlace(a)), lq)
}

func (s botsSidebarComp) ids(c *ctx) []string {
	var ids []string
	for _, g := range s.groups(c) {
		for _, a := range g.bots {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

func (s botsSidebarComp) index(ids []string) int {
	if i := slices.Index(ids, s.sel); i >= 0 {
		return i
	}
	return max(0, min(s.at, len(ids)-1))
}

func (s botsSidebarComp) moveTo(ids []string, i int) botsSidebarComp {
	if len(ids) == 0 {
		return s
	}
	s.sel, s.at = ids[i], i
	return s
}

func (s botsSidebarComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
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
	case m.matches(msg, botKeys.Filter):
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
			return s, openBot(c, ids[i]), true
		}
	case m.matches(msg, k.Refresh):
		return s, m.loadBotsCmd(), true
	default:
		return s, nil, false
	}
	return s, nil, true
}

func (s botsSidebarComp) click(c *ctx, h hitTarget, focused bool) (component, tea.Cmd) {
	ids := s.ids(c)
	if h.index >= len(ids) {
		return s, nil
	}
	if focused && h.index == s.index(ids) {
		return s, openBot(c, ids[h.index])
	}
	return s.moveTo(ids, h.index), nil
}

func (s botsSidebarComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	ids := s.ids(c)
	return s.moveTo(ids, cursorBy(s.index(ids), d, len(ids))), nil
}

func (s botsSidebarComp) view(c *ctx, w, h int) string {
	m := c.m
	margin := strings.Repeat(" ", screenMargin)
	groups := s.groups(c)
	ids := s.ids(c)
	switch {
	case len(ids) == 0 && s.query != "":
		return margin + StyleDim.Render("no bots match /"+s.query+" · "+m.keyTextOn(s.section(), projectsKeys.Back)+" clears")
	case len(ids) == 0:
		return margin + StyleDim.Render("no bots")
	}
	focused := m.focused == leftSidebar && len(m.popups) == 0
	cur, open := s.index(ids), ""
	if a, ok := m.baseComp().(botComp); ok {
		open = a.botID
	}
	l := itemLines{key: "sidebar-bots"}
	i := 0
	for gi, g := range groups {
		if gi > 0 {
			l.text("")
		}
		l.text(margin + StyleDim.Render(botSectionTitles[g.section]))
		for _, a := range g.bots {
			l.add(i, m.botRow(a, i == cur, focused, a.ID == open, w))
			i++
		}
	}
	return strings.Join(l.window(c, cur, max(1, h)), "\n")
}

// botGlyph is a bot's status glyph and colour, in the sessions' colours.
func (m model) botGlyph(status string) (string, color.Color) {
	switch status {
	case "needs_you":
		return statusGlyph(session.StatusAwaitingInput), awaitingColor(true)
	case "waiting":
		return statusGlyph(session.StatusAwaitingInput), awaitingColor(false)
	case "working":
		return SpinnerFrames[m.spin%len(SpinnerFrames)], ColorOngoing
	case botUnreachable:
		return "○", ColorBorder
	}
	return statusGlyph(session.StatusIdle), statusColor(session.StatusIdle)
}

// unreadMark is the accent dot after an item with unread activity.
var unreadMark = lipgloss.NewStyle().Foreground(ColorAccent).Render(" •")

func (m model) botRow(a api.BotInfo, sel, focused, open bool, w int) string {
	glyph, gcolor := m.botGlyph(a.Status)
	titleStyle := lipgloss.NewStyle()
	switch {
	case a.Status == botUnreachable:
		titleStyle = StyleDim
	case open:
		titleStyle = StylePrimaryBold
	}
	textW := max(1, w-2)
	marker := treeMarker(sel, focused)
	name := titleStyle.Render(xansi.Truncate(a.Name, textW, "…"))
	if m.botUnread(a.ID) {
		name += unreadMark
	}
	top := marker + lipgloss.NewStyle().Foreground(gcolor).Render(glyph) + " " + name
	bottom := marker + "  " + StyleDim.Render(xansi.Truncate(m.botPlace(a), textW, "…"))
	return top + "\n" + bottom
}

// botPlace is a bot's CLI, home node, and thread count.
func (m model) botPlace(a api.BotInfo) string {
	var parts []string
	if name, _ := agentLabel(a.Agent); name != "" {
		parts = append(parts, name)
	} else if a.Agent != "" {
		parts = append(parts, a.Agent)
	}
	if a.NodeLabel != "" {
		parts = append(parts, a.NodeLabel)
	}
	if ts, ok := m.botThreads[a.ID]; ok {
		parts = append(parts, plural(len(ts), "thread"))
	}
	return strings.Join(parts, " · ")
}

func (s botsSidebarComp) hoverItem(c *ctx) (hoverItem, bool) {
	ids := s.ids(c)
	if len(ids) == 0 {
		return hoverItem{}, false
	}
	i := s.index(ids)
	a, _ := c.m.bot(ids[i])
	it := hoverItem{key: "bot:" + a.ID, index: i, title: a.Name}
	it.add("Status", strings.ReplaceAll(a.Status, "_", " "))
	it.add("Description", a.Description)
	name, _ := agentLabel(a.Agent)
	it.add("Bot", name)
	it.add("Model", a.Model)
	it.add("Dir", a.Dir)
	it.add("Node", a.NodeLabel)
	it.add("ID", a.ID)
	return it, true
}
