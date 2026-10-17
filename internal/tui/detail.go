package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/MunifTanjim/argus/internal/session"
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
	bodySpans       []cardSpan         // what body's lines copy in a line selection
	entry           transcript.Entry   // the entry a body frame renders
	cursor          int                // selected display row index (see frameRows)
	scroll          int                // top line offset
	defaultExpanded bool
	expanded        map[int]bool    // by item index, not display row
	runs            map[string]bool // run key -> expanded, overriding verboseTranscript
	focused         bool            // single-item focus frame: no further drilling
	visual          *selection      // visual-mode selection; nil outside visual mode

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

// frameRows folds a trace frame's items into display rows, as the main stream
// does; a focused leaf or a body frame shows its items as they are.
func (m tview) frameRows(f *detailFrame) []displayRow {
	if f.focused || f.items == nil {
		rows := make([]displayRow, len(f.items))
		for i := range rows {
			rows[i] = displayRow{kind: entryRow, entry: i}
		}
		return rows
	}
	return buildRows(f.items, m.c.m.verboseTranscript, f.runs)
}

func (m tview) frameCursorRow(f *detailFrame) (displayRow, bool) {
	rows := m.frameRows(f)
	if f.cursor < 0 || f.cursor >= len(rows) {
		return displayRow{}, false
	}
	return rows[f.cursor], true
}

func (m tview) frameRowID(f *detailFrame) rowRef {
	if r, ok := m.frameCursorRow(f); ok {
		return rowID(f.items, r)
	}
	return rowRef{}
}

// toggleFrameRun acts on a control row of the top frame: a summary expands its
// run, a head or a foot collapses it, keeping the cursor on the run's summary
// or head row. It fetches the bodies of expanded tools it reveals.
func (m tview) toggleFrameRun(f *detailFrame, r displayRow) tea.Cmd {
	on := r.kind == runSummary
	if f.runs == nil {
		f.runs = map[string]bool{}
	}
	f.runs[r.run.key] = on
	rows := m.frameRows(f)
	if i := runRowIndex(rows, r.run.key); i >= 0 {
		f.cursor = i
	}
	lines, first := m.frameLines(f, m.c.m.transcriptWidth())
	total, h := len(lines), m.detailWindowHeight(f)
	start, end := f.cursorSpan(first, total)
	f.scroll = windowScroll(total, start, end, h, f.scroll)
	if !on {
		return nil
	}
	if head, foot, ok := runEdges(rows, r.run.key); ok {
		start, _ := itemSpan(head, first, total)
		_, end := itemSpan(foot, first, total)
		f.scroll = windowScroll(total, start, end, h, f.scroll)
		m.clampDetailScroll()
	}
	var cmds []tea.Cmd
	for _, dr := range rows {
		if dr.kind == entryRow && dr.run.key == r.run.key && f.isExpanded(dr.entry) {
			cmds = append(cmds, m.fetchToolBodyCmd(f.items[dr.entry], f.agentID))
		}
	}
	return tea.Batch(cmds...)
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

func drillLabel(agent string, e transcript.Entry) string {
	switch e.Kind {
	case transcript.EntryThinking:
		return "Thinking"
	case transcript.EntryText:
		return "Output"
	case transcript.EntrySubagent:
		return subagentLabel(e)
	default:
		return toolDisplayName(agent, e.ToolName)
	}
}

func (m tview) enterDetail() tea.Cmd {
	m.transcript.detailStack = nil
	r, ok := m.cursorRow()
	if !ok || r.isControl() {
		return nil
	}
	return m.drillEntry(m.transcript.entries[r.entry], "")
}

func (m tview) drillEntry(e transcript.Entry, agentID string) tea.Cmd {
	if s, ok := soleSubagent(e); ok && s.HasTrace && !s.IsTeammate {
		return m.drillTrace(e, s)
	}
	switch e.Kind {
	case transcript.EntryUser, transcript.EntrySystem, transcript.EntryShell:
		m.transcript.detailStack = append(m.transcript.detailStack,
			m.bodyFrame(e))
		return nil
	}
	m.transcript.detailStack = append(m.transcript.detailStack, detailFrame{
		label: drillLabel(m.agent(), e), items: []transcript.Entry{e}, agentID: agentID,
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

// closeFrames closes every open frame as detailBack closes them, so back
// returns to the session's stream.
func (m tview) closeFrames() []tea.Cmd {
	var cmds []tea.Cmd
	for f := m.topFrame(); f != nil; f = m.topFrame() {
		if f.subID != "" {
			cmds = append(cmds, m.c.m.unsubscribeCmd(f.subID))
			m.activeSub = m.sessionSub
			m.sessionSub = subRef{}
		}
		m.popDetail()
	}
	return cmds
}

// drillRunning opens a running subagent's trace as the only frame.
func (m tview) drillRunning(r session.RunningSubagent) tea.Cmd {
	cmds := m.closeFrames()
	s := transcript.Subagent{ID: r.ID, Type: r.Type, Name: r.Name, Desc: r.Desc, Status: "running", HasTrace: true}
	m.historyView = histDetail
	cmds = append(cmds, m.drillTrace(transcript.Entry{Kind: transcript.EntrySubagent, Subagents: []transcript.Subagent{s}}, s))
	return tea.Batch(cmds...)
}

// drillShell opens a running shell's Bash call as the only frame.
func (m tview) drillShell(r session.RunningShell) tea.Cmd {
	cmds := m.closeFrames()
	m.historyView = histDetail
	cmds = append(cmds, m.drillEntry(transcript.Entry{Kind: transcript.EntryTool, ToolName: "Bash", ToolID: r.ToolUseID}, ""))
	return tea.Batch(cmds...)
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
	{detailKeys.PageDown, tview.actDetailPageDown},
	{detailKeys.PageUp, tview.actDetailPageUp},
	{detailKeys.Top, tview.actDetailTop},
	{detailKeys.Bottom, tview.actDetailBottom},
	{detailKeys.Copy, tview.actCopyCard},
	{detailKeys.Visual, func(m tview, _ tea.KeyPressMsg) tea.Cmd { return m.startVisual(false) }},
	{detailKeys.VisualLine, func(m tview, _ tea.KeyPressMsg) tea.Cmd { return m.startVisual(true) }},
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
	} else if f.cursor < len(m.frameRows(f))-1 {
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
	lines, first := m.frameLines(f, m.c.m.transcriptWidth())
	start, end = f.cursorSpan(first, len(lines))
	h = max(1, m.viewportHeight()-3)
	return h, start, end, end-start > h
}

func (m tview) actDetailCollapse(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	r, ok := m.frameCursorRow(f)
	switch {
	case !ok || r.kind == runSummary:
		return nil
	case r.isControl():
		return m.toggleFrameRun(f, r)
	}
	if f.isExpanded(r.entry) {
		f.toggle(r.entry)
		m.ensureDetailVisible()
	}
	return nil
}

func (m tview) actDetailExpand(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	r, ok := m.frameCursorRow(f)
	if !ok {
		return nil
	}
	if r.isControl() {
		if r.kind != runSummary {
			return nil
		}
		return m.toggleFrameRun(f, r)
	}
	if !f.isExpanded(r.entry) {
		f.toggle(r.entry)
		m.ensureDetailVisible()
		return m.fetchToolBodyCmd(f.items[r.entry], f.agentID)
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
	if f == nil || f.items == nil || f.focused {
		return nil
	}
	r, ok := m.frameCursorRow(f)
	if !ok {
		return nil
	}
	if r.isControl() {
		return m.toggleFrameRun(f, r)
	}
	if !m.c.m.detailable(f.items[r.entry]) {
		return nil
	}
	return m.drillEntry(f.items[r.entry], f.agentID)
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

func (m tview) actDetailPageDown(tea.KeyPressMsg) tea.Cmd {
	m.topFrame().scroll += max(1, m.viewportHeight())
	m.clampDetailScroll()
	return nil
}

func (m tview) actDetailPageUp(tea.KeyPressMsg) tea.Cmd {
	f := m.topFrame()
	f.scroll = max(0, f.scroll-max(1, m.viewportHeight()))
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
		f.cursor = max(0, len(m.frameRows(f))-1)
	}
	f.scroll = m.frameMaxScroll(f) // true bottom; works for body frames and tall items too
	return nil
}

// frameLines renders all of a frame's rows to display lines, with each row's
// first line (nil for a body frame).
func (m tview) frameLines(f *detailFrame, width int) (lines []string, first []int) {
	if f.items == nil {
		// A body frame has no cursor gutter; indent to align with the
		// breadcrumb/header and the padded session header.
		body := indentBlock(f.body, strings.Repeat(" ", detailGutter))
		return strings.Split(body, "\n"), nil
	}
	return m.frameLayout(f, width)
}

// cursorSpan is the [start,end) line range of the cursor row (0,0 when there
// is none).
func (f *detailFrame) cursorSpan(first []int, total int) (int, int) {
	if f.cursor < 0 || f.cursor >= len(first) {
		return 0, 0
	}
	return itemSpan(f.cursor, first, total)
}

func (m tview) frameLayout(f *detailFrame, width int) (lines []string, first []int) {
	rows := m.frameRows(f)
	first = make([]int, len(rows))
	for i, r := range rows {
		if i > 0 {
			lines = append(lines, "")
		}
		first[i] = len(lines)
		var block string
		if r.isControl() {
			block = controlBlock(r, i == f.cursor, true, width)
		} else {
			block = m.entryBlock(f.items[r.entry], f.isExpanded(r.entry), i == f.cursor, true, f.focused, width)
		}
		lines = append(lines, strings.Split(block, "\n")...)
	}
	return lines, first
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

// detailWindowHeight is the item lines the cursor keeps in view, assuming a
// breadcrumb and a scroll indicator.
func (m tview) detailWindowHeight(f *detailFrame) int {
	h := max(1, m.viewportHeight()-3) // breadcrumb(2) + hint(1)
	if header := f.detailHeaderText(m.c.m.transcriptWidth() - detailGutter); header != "" {
		h = max(1, h-(len(strings.Split(header, "\n"))+1)) // header lines + trailing blank
	}
	return h
}

func (m tview) frameItemStarts(f *detailFrame, width int) (first []int, total int) {
	lines, first := m.frameLayout(f, width)
	return first, len(lines)
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
	return rowAtLine(first, line)
}

func (m tview) detailCursorVisible(f *detailFrame) bool {
	if f == nil || f.items == nil || f.cursor < 0 {
		return false
	}
	first, total := m.frameItemStarts(f, m.c.m.transcriptWidth())
	if f.cursor >= len(first) {
		return false
	}
	start, end := itemSpan(f.cursor, first, total)
	return start < f.scroll+m.detailWindowHeight(f) && end > f.scroll
}

func (m tview) firstVisibleItem(f *detailFrame) int {
	first, _ := m.frameItemStarts(f, m.c.m.transcriptWidth())
	h := m.detailWindowHeight(f)
	for i, s := range first {
		if s >= f.scroll && s < f.scroll+h {
			return i
		}
	}
	return m.itemAtLine(f, f.scroll)
}

func (m tview) lastVisibleItem(f *detailFrame) int {
	first, _ := m.frameItemStarts(f, m.c.m.transcriptWidth())
	h := m.detailWindowHeight(f)
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
	lines, first := m.frameLines(f, m.c.m.transcriptWidth())
	start, end := f.cursorSpan(first, len(lines))
	f.scroll = windowScroll(len(lines), start, end, m.detailWindowHeight(f), f.scroll)
}

func (m tview) frameMaxScroll(f *detailFrame) int {
	lines, _ := m.frameLines(f, m.c.m.transcriptWidth())
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

// detailPrefix is what sits above a frame's items: the breadcrumb and the
// subagent header, with rows the lines it takes.
func (m tview) detailPrefix(f *detailFrame) (prefix string, rows int) {
	cw := m.c.m.transcriptWidth()
	// Align the breadcrumb/header with item text, which sits past the accent gutter.
	gutter := strings.Repeat(" ", detailGutter)
	if crumb := m.detailCrumbLine(cw - detailGutter); crumb != "" {
		prefix = indentBlock(crumb, gutter) + "\n\n"
	}
	if header := f.detailHeaderText(cw - detailGutter); header != "" {
		prefix += indentBlock(header, gutter) + "\n\n"
	}
	return prefix, strings.Count(prefix, "\n")
}

// detailBodyHeight is the lines total item lines get under a prefix of rows
// lines; overflow reserves one for the scroll indicator.
func (m tview) detailBodyHeight(rows, total int) (h int, overflow bool) {
	h = max(1, m.viewportHeight()-rows)
	if total <= h {
		return h, false
	}
	return max(1, h-1), true
}

// detailBody renders the active frame: breadcrumb + item list sliced to the
// viewport (a row reserved for the scroll indicator on overflow), centered.
func (m tview) detailBody() string {
	cw := m.c.m.transcriptWidth()
	f := m.topFrame()
	if f == nil {
		return m.c.m.transcriptText(dimStyle.Render("(nothing to show)"))
	}
	lines, first := m.frameLines(f, cw)
	prefix, rows := m.detailPrefix(f)
	m.hitCrumbClose(cw - detailGutter)
	ch, overflow := m.detailBodyHeight(rows, len(lines))
	if !overflow {
		m.hitItems(f, first, len(lines), rows, 0, len(lines))
		m.hitDetailText(rows, 0, len(lines))
		return m.c.m.center(prefix+strings.Join(m.paintVisual(lines), "\n"), m.c.m.containerWidth())
	}
	scroll := max(0, min(f.scroll, len(lines)-ch))
	end := scroll + ch
	m.hitItems(f, first, len(lines), rows, scroll, end)
	m.hitDetailText(rows, scroll, end)
	body := strings.Join(m.paintVisual(lines[scroll:end]), "\n")
	hint := scrollHint(scroll, len(lines)-end, cw)
	return m.c.m.center(prefix+body+"\n"+hint, m.c.m.containerWidth())
}

// detailCrumbLine ends the breadcrumb with a close button when the mouse is on.
// It only draws; hitCrumbClose records the button.
func (m tview) detailCrumbLine(w int) string {
	if !m.c.m.mouse {
		return truncateLine(m.detailBreadcrumb(), w)
	}
	return spaceBetween(truncateLine(m.detailBreadcrumb(), max(1, w-2)), StyleDim.Render(glyphClose), w)
}

// hitCrumbClose records the breadcrumb's close button, the last of w cells.
func (m tview) hitCrumbClose(w int) {
	if !m.c.m.mouse {
		return
	}
	x := m.c.m.transcriptTextX() + w - 1
	m.c.hitZone(uv.Rect(x, 0, 1, 1), hitTarget{kind: hitClose})
}

func (m tview) hitItems(f *detailFrame, first []int, total, rows, scroll, end int) {
	if f.items == nil || !m.c.recording() {
		return
	}
	hitStarts(m.c.below(rows), len(first), func(i int) (int, int) { return itemSpan(i, first, total) }, scroll, end)
}

func (m tview) clickItem(i int, focused bool) tea.Cmd {
	f := m.topFrame()
	if f == nil || f.items == nil {
		return nil
	}
	rows := m.frameRows(f)
	if i < 0 || i >= len(rows) {
		return nil
	}
	if rows[i].isControl() {
		f.cursor = i
		return m.toggleFrameRun(f, rows[i])
	}
	if focused && i == f.cursor {
		return m.actDetailDrill(tea.KeyPressMsg{})
	}
	f.cursor = i
	return nil
}

// wheelDetail moves like j/k, one step per notch (see wheelEntries).
func (m tview) wheelDetail(d int) {
	if m.topFrame() == nil {
		return
	}
	for ; d > 0; d-- {
		m.actDetailDown(tea.KeyPressMsg{})
	}
	for ; d < 0; d++ {
		m.actDetailUp(tea.KeyPressMsg{})
	}
}

func (m tview) bodyFrame(e transcript.Entry) detailFrame {
	body, spans := m.c.m.renderDetail(e)
	return detailFrame{label: "detail", body: body, bodySpans: spans, entry: e}
}

// renderDetail renders the body frame of a user, system, or shell entry, and
// what its lines copy in a line selection.
func (m model) renderDetail(e transcript.Entry) (string, []cardSpan) {
	width := m.transcriptWidth() - detailGutter
	switch e.Kind {
	case transcript.EntryUser:
		head := StylePrimaryBold.Render("You") + " " + Icon.User.Render() + "  " + StyleDim.Render(clockTime(e.Timestamp))
		body := m.renderMD(e.Text, width-2)
		return head + "\n\n" + body, append(chromeSpans(2), textSpan(body, e.Text))
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
			return head, chromeSpans(1)
		}
		body := hardWrap(StyleDim.Render(strings.TrimRight(e.Detail, "\n")), width-2)
		return head + "\n\n" + body, append(chromeSpans(2), textSpan(body, e.Detail))
	default:
		label := StylePrimaryBold.Render("Shell")
		if e.IsError {
			label = lipgloss.NewStyle().Bold(true).Foreground(ColorError).Render("Shell")
		}
		head := Icon.Shell.Render() + " " + label + "  " + StyleDim.Render(clockTime(e.Timestamp))
		body := StyleSecondaryBold.Render("$") + " " + e.Text
		spans := append(chromeSpans(2), wholeSpan(linesIn(e.Text), e.Text))
		if e.Detail != "" {
			resultLabel := "Result"
			if e.IsError {
				resultLabel = "Error"
			}
			result := m.execCommandResultBody(e.Detail, width-2)
			body += "\n\n" + sectionLabel(resultLabel, e.IsError) + "\n" + result
			spans = append(spans, chromeSpans(2)...) // the blank line and the label
			spans = append(spans, textSpan(result, e.Detail))
		}
		return head + "\n\n" + body, spans
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
	return m.c.m.renderToolBody(m.agent(), it, width)
}

// renderToolBody renders agent's tool; it needs the input and result already fetched.
func (m model) renderToolBody(agent string, it transcript.Entry, width int) string {
	if body, ok := m.toolDetailBody(agent, it, width); ok {
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
// view at the bottom stays there, and a cursor on the last row of such a view
// moves to the new last row; otherwise the cursor keeps its row (see rowID). A
// first load tails the trace.
func (m tview) followFrame(f *detailFrame, entries []transcript.Entry) {
	first := len(f.items) == 0
	wasLast := first || f.cursor == len(m.frameRows(f))-1
	atBottom := tailFollows(first || f.scroll >= m.frameMaxScroll(f), f.visual)
	ref := m.frameRowID(f)
	f.items = entries
	f.cursor = restoreRowCursor(f.items, m.frameRows(f), ref, f.cursor, atBottom && wasLast)
	if atBottom {
		f.scroll = m.frameMaxScroll(f)
	}
}
