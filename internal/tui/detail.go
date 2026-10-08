package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// The detail drill-down: a frame stack (detailStack) opened from one entry. A
// subagent opens a frame listing its trace's entries; any other agent entry opens
// a focused leaf frame; user/system/shell entries open a pre-rendered body.

// detailFrame is one level of the drill stack: a navigable entry list (a
// subagent's trace, or a single focused entry) or a pre-rendered body.
type detailFrame struct {
	label           string             // breadcrumb segment
	subID           string             // subscription backing this frame (streamed subagent frames only)
	agentID         string             // subagent whose items this frame lists ("" = main transcript); for tool-body fetches
	items           []transcript.Entry // nil for a body frame
	body            string             // pre-rendered body (user/system/shell entries)
	cursor          int                // selected item index
	scroll          int                // top line offset
	defaultExpanded bool               // default item expansion for this frame
	expanded        map[int]bool       // per-item expand override (by item index)
	focused         bool               // single-item focus frame: no further drilling

	// Identity header for a subagent's drilled-in trace frame.
	subagentType   string
	subagentName   string
	subagentStatus string
	subagentInput  string
}

func (f *detailFrame) isExpanded(i int) bool {
	if v, ok := f.expanded[i]; ok {
		return v
	}
	return f.defaultExpanded
}

func (f *detailFrame) toggle(i int) {
	if f.expanded == nil {
		f.expanded = map[int]bool{}
	}
	f.expanded[i] = !f.isExpanded(i)
}

func (m tview) topFrame() *detailFrame {
	if len(m.transcript.detailStack) == 0 {
		return nil
	}
	return &m.transcript.detailStack[len(m.transcript.detailStack)-1]
}

func soleSubagent(it transcript.Entry) (transcript.Subagent, bool) {
	if it.Kind == transcript.EntrySubagent && len(it.Subagents) == 1 {
		return it.Subagents[0], true
	}
	return transcript.Subagent{}, false
}

// drillable reports whether entering an item opens a meaningful sub-trace.
func drillable(it transcript.Entry) bool {
	s, ok := soleSubagent(it)
	return ok && s.HasTrace
}

func drillLabel(e transcript.Entry) string {
	switch e.Kind {
	case transcript.EntryThinking:
		return "Thinking"
	case transcript.EntryText:
		return "Output"
	case transcript.EntrySubagent:
		return subagentLabel(e)
	default:
		return toolDisplayName(e.ToolName)
	}
}

func (m tview) enterDetail() tea.Cmd {
	m.transcript.detailStack = nil
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(m.transcript.entries) {
		return nil
	}
	return m.drillEntry(m.transcript.entries[m.transcript.cursor], "")
}

func (m tview) drillEntry(e transcript.Entry, agentID string) tea.Cmd {
	if s, ok := soleSubagent(e); ok && s.HasTrace && !s.IsTeammate {
		return m.drillTrace(e, s)
	}
	switch e.Kind {
	case transcript.EntryUser, transcript.EntrySystem, transcript.EntryShell:
		m.transcript.detailStack = append(m.transcript.detailStack,
			detailFrame{label: "detail", body: m.c.m.renderDetail(e)})
		return nil
	}
	m.transcript.detailStack = append(m.transcript.detailStack, detailFrame{
		label: drillLabel(e), items: []transcript.Entry{e}, agentID: agentID,
		defaultExpanded: true, focused: true, expanded: map[int]bool{},
	})
	return m.fetchToolBodyCmd(e, agentID)
}

// drillTrace pushes a frame listing a subagent's trace: inline when shipped, a
// one-shot fetch for a past session, or a live subscription.
func (m tview) drillTrace(e transcript.Entry, s transcript.Subagent) tea.Cmd {
	f := detailFrame{
		label: subagentLabel(e), agentID: s.ID, expanded: map[int]bool{},
		subagentType: s.Type, subagentName: s.Name, subagentStatus: s.Status, subagentInput: s.Desc,
	}
	if len(s.Trace) > 0 || s.ID == "" {
		f.items = s.Trace
		m.transcript.detailStack = append(m.transcript.detailStack, f)
		return nil
	}
	if !m.live {
		m.transcript.detailStack = append(m.transcript.detailStack, f)
		return m.c.m.fetchHistSubagent(m.history.addr(), s.ID)
	}
	// Stash the session subRef so pop can restore it without a leak.
	m.sessionSub = m.activeSub
	ref := subRef{subID: newSubID(), sessionID: m.sessionID, agentID: s.ID, cacheKey: m.c.m.cacheKeyFor(m.sessionID)}
	m.activeSub = ref
	f.subID = ref.subID
	m.transcript.detailStack = append(m.transcript.detailStack, f)
	return m.c.m.subscribeCmd(ref, len(m.c.m.transcriptCache[ref.key()].entries))
}

// popDetail removes the deepest frame; returns true when the stack is now empty.
func (m tview) popDetail() bool {
	if len(m.transcript.detailStack) > 0 {
		m.transcript.detailStack = m.transcript.detailStack[:len(m.transcript.detailStack)-1]
	}
	return len(m.transcript.detailStack) == 0
}

func (m model) detailable(e transcript.Entry) bool {
	switch e.Kind {
	case transcript.EntryThinking, transcript.EntryText:
		return strings.TrimSpace(e.Text) != ""
	case transcript.EntryTool, transcript.EntrySkill, transcript.EntrySubagent:
		return true
	case transcript.EntryUser:
		return e.Text != ""
	case transcript.EntryTurnEnd, transcript.EntryCompact:
		return false
	default:
		return e.Detail != ""
	}
}

func (m tview) handleDetailKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.topFrame() == nil {
		return nil
	}
	cmd, _ := m.dispatch(msg, detailTable)
	return cmd
}

// detailTable maps detail-view bindings to actions. Each action mutates the top
// frame (a pointer into the shared detailStack backing).
var detailTable = []keyTableEntry{
	{detailKeys.Down, tview.actDetailDown},
	{detailKeys.Up, tview.actDetailUp},
	{detailKeys.Collapse, tview.actDetailCollapse},
	{detailKeys.Expand, tview.actDetailExpand},
	{detailKeys.Drill, tview.actDetailDrill},
	{detailKeys.HalfDown, tview.actDetailHalfDown},
	{detailKeys.HalfUp, tview.actDetailHalfUp},
	{detailKeys.Top, tview.actDetailTop},
	{detailKeys.Bottom, tview.actDetailBottom},
}

func (m tview) actDetailDown(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	if f.items == nil {
		f.scroll++
		m.clampDetailScroll()
		return nil
	}
	if !m.detailCursorVisible(f) {
		f.cursor = m.firstVisibleItem(f) // re-anchor to viewport; don't jump from the off-screen cursor
		return nil
	}
	// Scroll within a cursor item taller than the viewport before advancing.
	if h, _, end, ok := m.cursorOverflow(f); ok && f.scroll < end-h {
		f.scroll++
		m.clampDetailScroll()
	} else if f.cursor < len(f.items)-1 {
		f.cursor++
		m.ensureDetailVisible()
	}
	return nil
}

func (m tview) actDetailUp(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	if f.items == nil {
		if f.scroll > 0 {
			f.scroll--
		}
		return nil
	}
	if !m.detailCursorVisible(f) {
		f.cursor = m.lastVisibleItem(f) // re-anchor to viewport
		return nil
	}
	if _, start, _, ok := m.cursorOverflow(f); ok && f.scroll > start {
		f.scroll--
	} else if f.cursor > 0 {
		f.cursor--
		m.ensureDetailVisible()
	}
	return nil
}

// cursorOverflow reports whether the cursor item is taller than the visible
// height h, returning h and the item's [start,end) line range. h matches
// detailBody's content area so it agrees with ensureDetailVisible.
func (m tview) cursorOverflow(f *detailFrame) (h, start, end int, ok bool) {
	_, start, end = m.frameLines(f, m.c.m.transcriptWidth())
	h = max(1, m.viewportHeight()-3)
	return h, start, end, end-start > h
}

func (m tview) actDetailCollapse(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	if f.items != nil && f.cursor >= 0 && f.cursor < len(f.items) && f.isExpanded(f.cursor) {
		f.toggle(f.cursor)
		m.ensureDetailVisible()
	}
	return nil
}

func (m tview) actDetailExpand(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	if f.items != nil && f.cursor >= 0 && f.cursor < len(f.items) && !f.isExpanded(f.cursor) {
		f.toggle(f.cursor)
		m.ensureDetailVisible()
		return m.fetchToolBodyCmd(f.items[f.cursor], f.agentID)
	}
	return nil
}

func subagentLabel(it transcript.Entry) string {
	s, _ := soleSubagent(it)
	if s.IsTeammate {
		if s.Name != "" {
			return "Teammate · " + s.Name
		}
		return "Teammate"
	}
	name := s.Type
	if name == "" {
		name = "Subagent"
	}
	if s.Name != "" {
		return name + " · " + s.Name
	}
	return name
}

func spawnAgentLabel(agentType, nickname string) string {
	label := "Spawn Agent"
	if nickname != "" {
		label += ": " + nickname
	}
	if agentType != "" {
		label += " (" + agentType + ")"
	}
	return label
}

// subagentHeaderLines renders a spawn_agent's identity header and Input section.
func subagentHeaderLines(agentType, nickname, status, input string, iw int) []string {
	head := Icon.Subagent.Render() + " " + StylePrimaryBold.Render(spawnAgentLabel(agentType, nickname))
	if status != "" {
		head += " " + StyleSecondary.Render("["+status+"]")
	}
	lines := []string{hardWrap(head, iw)}
	if input != "" {
		lines = append(lines, StyleSecondaryBold.Render("Input")+"\n"+wrapDim(input, iw))
	}
	return lines
}

func (m tview) actDetailDrill(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	if f == nil || f.items == nil || f.focused || f.cursor < 0 || f.cursor >= len(f.items) ||
		!m.c.m.detailable(f.items[f.cursor]) {
		return nil
	}
	return m.drillEntry(f.items[f.cursor], f.agentID)
}

func (m tview) actDetailHalfDown(tea.KeyPressMsg) tea.Cmd {
	m.topFrame().scroll += max(1, m.viewportHeight()/2)
	m.clampDetailScroll()
	return nil
}

func (m tview) actDetailHalfUp(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	f.scroll = max(0, f.scroll-max(1, m.viewportHeight()/2))
	return nil
}

func (m tview) actDetailTop(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	f.scroll = 0
	if f.items != nil {
		f.cursor = 0
	}
	return nil
}

func (m tview) actDetailBottom(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	if f.items != nil {
		f.cursor = max(0, len(f.items)-1)
	}
	f.scroll = m.frameMaxScroll(f) // true bottom; works for body frames and tall items too
	return nil
}

// frameLines renders all of a frame's items to display lines and returns the
// [start,end) line range of the cursor item (0,0 for a body frame).
func (m tview) frameLines(f *detailFrame, width int) (lines []string, curStart, curEnd int) {
	if f.items == nil {
		// A body frame has no cursor gutter; indent to align with the
		// breadcrumb/header and the padded session header.
		body := indentBlock(f.body, strings.Repeat(" ", detailGutter))
		return strings.Split(body, "\n"), 0, 0
	}
	for i, it := range f.items {
		if i > 0 {
			lines = append(lines, "") // blank separator
		}
		start := len(lines)
		block := m.entryBlock(it, f.isExpanded(i), i == f.cursor, true, f.focused, width)
		lines = append(lines, strings.Split(block, "\n")...)
		if i == f.cursor {
			curStart, curEnd = start, len(lines)
		}
	}
	return lines, curStart, curEnd
}

// detailBreadcrumb renders the drill path (e.g. "opus4.8 › explorer › Read").
func (m tview) detailBreadcrumb() string {
	var labels []string
	for i := range m.transcript.detailStack {
		labels = append(labels, m.transcript.detailStack[i].label)
	}
	return StyleDim.Render(strings.Join(labels, " › "))
}

func (f *detailFrame) detailHeaderText(width int) string {
	if f.subagentType == "" && f.subagentName == "" && f.subagentStatus == "" && f.subagentInput == "" {
		return ""
	}
	return strings.Join(subagentHeaderLines(f.subagentType, f.subagentName, f.subagentStatus, f.subagentInput, width), "\n")
}

func (m tview) detailBodyHeight(f *detailFrame) int {
	h := max(1, m.viewportHeight()-3) // breadcrumb(2) + hint(1)
	if header := f.detailHeaderText(m.c.m.transcriptWidth() - detailGutter); header != "" {
		h = max(1, h-(len(strings.Split(header, "\n"))+1)) // header lines + trailing blank
	}
	return h
}

func (m tview) frameItemStarts(f *detailFrame, width int) (first []int, total int) {
	first = make([]int, len(f.items))
	for i, it := range f.items {
		if i > 0 {
			total++ // blank separator
		}
		first[i] = total
		block := m.entryBlock(it, f.isExpanded(i), i == f.cursor, true, f.focused, width)
		total += strings.Count(block, "\n") + 1
	}
	return first, total
}

func itemSpan(i int, first []int, total int) (int, int) {
	start := first[i]
	end := total
	if i+1 < len(first) {
		end = first[i+1] - 1
	}
	return start, end
}

func (m tview) itemAtLine(f *detailFrame, line int) int {
	first, _ := m.frameItemStarts(f, m.c.m.transcriptWidth())
	idx := 0
	for i, s := range first {
		if s <= line {
			idx = i
		}
	}
	return idx
}

func (m tview) detailCursorVisible(f *detailFrame) bool {
	if f == nil || f.items == nil || f.cursor < 0 || f.cursor >= len(f.items) {
		return false
	}
	first, total := m.frameItemStarts(f, m.c.m.transcriptWidth())
	start, end := itemSpan(f.cursor, first, total)
	return start < f.scroll+m.detailBodyHeight(f) && end > f.scroll
}

func (m tview) firstVisibleItem(f *detailFrame) int {
	first, _ := m.frameItemStarts(f, m.c.m.transcriptWidth())
	h := m.detailBodyHeight(f)
	for i, s := range first {
		if s >= f.scroll && s < f.scroll+h {
			return i
		}
	}
	return m.itemAtLine(f, f.scroll)
}

func (m tview) lastVisibleItem(f *detailFrame) int {
	first, _ := m.frameItemStarts(f, m.c.m.transcriptWidth())
	h := m.detailBodyHeight(f)
	last := -1
	for i, s := range first {
		if s >= f.scroll && s < f.scroll+h {
			last = i
		}
	}
	if last < 0 {
		return m.itemAtLine(f, f.scroll)
	}
	return last
}

func (m tview) ensureDetailVisible() {
	f := m.topFrame()
	if f == nil || f.items == nil {
		return
	}
	lines, start, end := m.frameLines(f, m.c.m.transcriptWidth())
	h := m.detailBodyHeight(f)
	if start < f.scroll {
		f.scroll = start
	} else if end > f.scroll+h {
		f.scroll = end - h
		if f.scroll > start {
			f.scroll = start // tall item: pin to its top
		}
	}
	if maxScroll := max(0, len(lines)-h); f.scroll > maxScroll {
		f.scroll = maxScroll
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}

func (m tview) frameMaxScroll(f *detailFrame) int {
	lines, _, _ := m.frameLines(f, m.c.m.transcriptWidth())
	bodyH := m.viewportHeight()
	if crumb := truncateLine(m.detailBreadcrumb(), m.c.m.transcriptWidth()); crumb != "" {
		bodyH = max(1, bodyH-2)
	}
	if header := f.detailHeaderText(m.c.m.transcriptWidth() - detailGutter); header != "" {
		bodyH = max(1, bodyH-(len(strings.Split(header, "\n"))+1))
	}
	if len(lines) <= bodyH {
		return 0
	}
	return max(0, len(lines)-max(1, bodyH-1)) // bodyH-1: a row is reserved for the scroll hint
}

func (m tview) clampDetailScroll() {
	f := m.topFrame()
	if f == nil {
		return
	}
	if maxS := m.frameMaxScroll(f); f.scroll > maxS {
		f.scroll = maxS
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}

func scrollHint(above, below, width int) string {
	var parts []string
	if above > 0 {
		parts = append(parts, fmt.Sprintf("▲ %d", above))
	}
	if below > 0 {
		parts = append(parts, fmt.Sprintf("▼ %d", below))
	}
	txt := strings.Join(parts, "   ")
	return lipgloss.NewStyle().Foreground(ColorTextMuted).Width(max(width, 1)).
		Align(lipgloss.Right).Render(txt)
}

// detailBody renders the active frame: breadcrumb + item list sliced to the
// viewport (a row reserved for the scroll indicator on overflow), centered.
func (m tview) detailBody() string {
	cw := m.c.m.transcriptWidth()
	f := m.topFrame()
	if f == nil {
		return m.c.m.center(dimStyle.Render("(nothing to show)"), m.c.m.containerWidth())
	}
	lines, _, _ := m.frameLines(f, cw)
	// Align the breadcrumb/header with item text, which sits past the accent gutter.
	gutter := strings.Repeat(" ", detailGutter)
	crumb := m.detailCrumbLine(cw - detailGutter)
	h := m.viewportHeight()
	bodyH := h
	prefix := ""
	if crumb != "" {
		prefix = indentBlock(crumb, gutter) + "\n\n"
		bodyH = max(1, h-2)
	}
	if header := f.detailHeaderText(cw - detailGutter); header != "" {
		prefix += indentBlock(header, gutter) + "\n\n"
		bodyH = max(1, bodyH-(len(strings.Split(header, "\n"))+1))
	}
	rows := strings.Count(prefix, "\n")
	if len(lines) <= bodyH {
		m.hitItems(f, cw, rows, 0, len(lines))
		return m.c.m.center(prefix+strings.Join(lines, "\n"), m.c.m.containerWidth())
	}
	ch := max(1, bodyH-1) // reserve a row for the scroll indicator
	scroll := min(f.scroll, len(lines)-ch)
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + ch
	m.hitItems(f, cw, rows, scroll, end)
	body := strings.Join(lines[scroll:end], "\n")
	hint := scrollHint(scroll, len(lines)-end, cw)
	return m.c.m.center(prefix+body+"\n"+hint, m.c.m.containerWidth())
}

// detailCrumbLine ends the breadcrumb with a close button when the mouse is on.
func (m tview) detailCrumbLine(w int) string {
	if !m.c.m.mouse {
		return truncateLine(m.detailBreadcrumb(), w)
	}
	x := centerGutter(m.c.m.containerWidth(), m.c.m.bodyWidth()) + detailGutter + w - 1
	m.c.hitZone(uv.Rect(x, 0, 1, 1), hitTarget{kind: hitClose})
	return spaceBetween(truncateLine(m.detailBreadcrumb(), max(1, w-2)), StyleDim.Render(glyphClose), w)
}

func (m tview) hitItems(f *detailFrame, cw, rows, scroll, end int) {
	if f.items == nil || !m.c.recording() {
		return
	}
	first, total := m.frameItemStarts(f, cw)
	hitStarts(m.c.below(rows), len(first), func(i int) (int, int) { return itemSpan(i, first, total) }, scroll, end)
}

func (m tview) clickItem(i int, focused bool) tea.Cmd {
	f := m.topFrame()
	if f == nil || f.items == nil || i < 0 || i >= len(f.items) {
		return nil
	}
	if focused && i == f.cursor {
		return m.actDetailDrill(tea.KeyPressMsg{})
	}
	f.cursor = i
	return nil
}

func (m tview) wheelDetail(d int) {
	f := m.topFrame()
	if f == nil {
		return
	}
	f.scroll += d
	m.clampDetailScroll()
	if f.items == nil || m.detailCursorVisible(f) {
		return
	}
	if d > 0 {
		f.cursor = m.firstVisibleItem(f)
	} else {
		f.cursor = m.lastVisibleItem(f)
	}
}

// renderDetail renders the body frame of a user, system, or shell entry.
func (m model) renderDetail(e transcript.Entry) string {
	width := m.transcriptWidth() - detailGutter
	switch e.Kind {
	case transcript.EntryUser:
		head := StylePrimaryBold.Render("You") + " " + Icon.User.Render() + "  " + StyleDim.Render(clockTime(e.Timestamp))
		return head + "\n\n" + m.renderMD(e.Text, width-2)
	case transcript.EntrySystem:
		icon := Icon.System
		label := StyleSecondary.Render("System")
		if e.IsError {
			icon = Icon.SystemErr
			label = lipgloss.NewStyle().Foreground(ColorError).Render("System")
		}
		head := icon.Render() + " " + label + "  " + Icon.Dot.Glyph + "  " + StyleDim.Render(clockTime(e.Timestamp))
		if e.Label != "" { // preview after the timestamp (e.g. "Recap")
			head += "  " + StyleDim.Render(e.Label)
		}
		if e.Detail == "" {
			return head
		}
		return head + "\n\n" + hardWrap(StyleDim.Render(strings.TrimRight(e.Detail, "\n")), width-2)
	default:
		label := StylePrimaryBold.Render("Shell")
		if e.IsError {
			label = lipgloss.NewStyle().Bold(true).Foreground(ColorError).Render("Shell")
		}
		head := Icon.Shell.Render() + " " + label + "  " + StyleDim.Render(clockTime(e.Timestamp))
		body := StyleSecondaryBold.Render("$") + " " + e.Text
		if e.Detail != "" {
			resultLabel := "Result"
			if e.IsError {
				resultLabel = "Error"
			}
			body += "\n\n" + sectionLabel(resultLabel, e.IsError) + "\n" + m.execCommandResultBody(e.Detail, width-2)
		}
		return head + "\n\n" + body
	}
}

// truncateLine caps a styled string to width columns on one line (ANSI-aware).
func truncateLine(s string, width int) string {
	return lipgloss.NewStyle().MaxWidth(max(width, 1)).Render(s)
}

// toolBody renders a tool's input/result via a per-tool renderer or a generic
// layout. Heavy bodies are fetched on demand; show a placeholder while outstanding.
func (m tview) toolBody(it transcript.Entry, width int) string {
	it, fetched := m.filledTool(it)
	if !fetched && it.ToolID != "" {
		return StyleDim.Render("loading…")
	}
	return m.c.m.renderToolBody(it, width)
}

// renderToolBody needs the tool's input and result already fetched.
func (m model) renderToolBody(it transcript.Entry, width int) string {
	if body, ok := m.toolDetailBody(it, width); ok {
		return body
	}
	return m.genericToolBody(it, width)
}

// filledTool populates on-demand body fields from the cache. Items with no ToolID
// are treated as already-resolved.
func (m tview) filledTool(it transcript.Entry) (transcript.Entry, bool) {
	if it.ToolID == "" {
		return it, true
	}
	e, ok := m.toolBodies[it.ToolID]
	if !ok || !e.done {
		return it, false
	}
	it.ToolInput, it.Result, it.ResultIsError = e.toolInput, e.result, e.resultIsError
	return it, true
}

func wrapDim(text string, width int) string {
	return lipgloss.NewStyle().Foreground(ColorTextDim).Width(max(width, 10)).Render(text)
}

// followFrame replaces a streamed frame's entries with the main stream's rule: a
// view at the bottom stays there, and a cursor on the last entry of such a view
// moves to the new last entry. A first load tails the trace.
func (m tview) followFrame(f *detailFrame, entries []transcript.Entry) {
	first := len(f.items) == 0
	wasLast := first || f.cursor == len(f.items)-1
	atBottom := first || f.scroll >= m.frameMaxScroll(f)
	f.items = entries
	if atBottom && wasLast && len(entries) > 0 {
		f.cursor = len(entries) - 1
	}
	if atBottom {
		f.scroll = m.frameMaxScroll(f)
	}
}
