package tui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
)

// The detail drill-down: a frame stack (detailStack) over a transcript chunk. The
// root frame lists an AI turn's items; drilling into a subagent pushes a frame of
// its trace; drilling into any item focuses it. Non-AI chunks render as a scrolled
// body.

// detailFrame is one level of the drill stack: a navigable item list (an AI
// chunk's items, a subagent's flattened trace, or a single focused item) or a
// pre-rendered body for non-AI chunks.
type detailFrame struct {
	label           string            // breadcrumb segment
	subID           string            // subscription backing this frame (streamed subagent frames only)
	agentID         string            // subagent whose items this frame lists ("" = main transcript); for tool-body fetches
	items           []claudecode.Item // navigable items; nil for a non-AI body frame
	body            string            // pre-rendered body (non-AI chunks)
	cursor          int               // selected item index
	scroll          int               // top line offset
	defaultExpanded bool              // default item expansion for this frame
	expanded        map[int]bool      // per-item expand override (by item index)
	focused         bool              // single-item focus frame: no further drilling
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

// expandOutputs pre-expands the frame's Output (text) items so final output shows
// without a manual unfold. Only items without an existing override are touched, so
// it's safe to re-run as a streamed trace grows (won't re-expand a user-collapsed item).
func (f *detailFrame) expandOutputs() {
	if f.expanded == nil {
		f.expanded = map[int]bool{}
	}
	for i, it := range f.items {
		if it.Kind == claudecode.ItemText {
			if _, ok := f.expanded[i]; !ok {
				f.expanded[i] = true
			}
		}
	}
}

// topFrame returns the active (deepest) frame, or nil when the stack is empty.
func (m model) topFrame() *detailFrame {
	if len(m.transcript.detailStack) == 0 {
		return nil
	}
	return &m.transcript.detailStack[len(m.transcript.detailStack)-1]
}

// flattenTrace collects the AI items across a subagent's trace chunks, in order.
func flattenTrace(chunks []claudecode.Chunk) []claudecode.Item {
	var items []claudecode.Item
	for _, c := range chunks {
		if c.Kind == claudecode.ChunkAI {
			items = append(items, c.Items...)
		}
	}
	return items
}

// drillable reports whether entering an item opens a meaningful sub-trace.
func drillable(it claudecode.Item) bool {
	return it.Kind == claudecode.ItemSubagent && it.HasTrace
}

// drillLabel is the breadcrumb segment for a focused single item.
func drillLabel(it claudecode.Item) string {
	switch it.Kind {
	case claudecode.ItemThinking:
		return "Thinking"
	case claudecode.ItemText:
		return "Output"
	case claudecode.ItemSubagent:
		return subagentLabel(it)
	default:
		return it.ToolName
	}
}

// enterDetail builds the root frame for the selected transcript chunk.
func (m *model) enterDetail() {
	m.transcript.detailStack = nil
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(m.transcript.chunks) {
		return
	}
	c := m.transcript.chunks[m.transcript.cursor]
	f := detailFrame{expanded: map[int]bool{}, defaultExpanded: false}
	if c.Kind == claudecode.ChunkAI {
		f.label = "Claude"
		if c.Model != "" {
			f.label = shortModel(c.Model)
		}
		f.items = c.Items
		f.expandOutputs() // pre-expand Output items
	} else {
		f.label = "detail"
		f.body = m.renderDetail(c)
	}
	m.transcript.detailStack = append(m.transcript.detailStack, f)
}

// drillDetail pushes a frame for the selected item: a subagent's trace, or the
// item focused on its own.
func (m *model) drillDetail() {
	f := m.topFrame()
	if f == nil || len(f.items) == 0 || f.cursor < 0 || f.cursor >= len(f.items) {
		return
	}
	it := f.items[f.cursor]
	if !drillable(it) && f.focused {
		return // already focused on this leaf; nothing deeper to drill
	}
	nf := detailFrame{expanded: map[int]bool{}, agentID: f.agentID}
	if drillable(it) {
		nf.label = subagentLabel(it)
		nf.items = flattenTrace(it.Trace)
		nf.defaultExpanded = false // children start collapsed
		nf.agentID = it.AgentID
		nf.expandOutputs() // but pre-expand the subagent's Output items
	} else {
		nf.label = drillLabel(it)
		nf.items = []claudecode.Item{it}
		nf.defaultExpanded = true
		nf.focused = true
	}
	m.transcript.detailStack = append(m.transcript.detailStack, nf)
}

// popDetail removes the deepest frame; returns true when the stack is now empty.
func (m *model) popDetail() bool {
	if len(m.transcript.detailStack) > 0 {
		m.transcript.detailStack = m.transcript.detailStack[:len(m.transcript.detailStack)-1]
	}
	return len(m.transcript.detailStack) == 0
}

// detailable reports whether a chunk has enough content to warrant a detail view.
func (m model) detailable(c claudecode.Chunk) bool {
	switch c.Kind {
	case claudecode.ChunkAI:
		return len(c.Items) > 0
	case claudecode.ChunkUser:
		return c.Text != ""
	default:
		return c.Detail != ""
	}
}

func (m model) handleDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.topFrame() == nil {
		return m, nil
	}
	if mm, cmd, ok := m.dispatch(msg, detailTable); ok {
		return mm, cmd
	}
	return m, nil
}

// detailTable maps detail-view bindings to actions. Each action mutates the top
// frame (a pointer into the shared detailStack backing).
var detailTable = []keyTableEntry{
	{detailKeys.Down, model.actDetailDown},
	{detailKeys.Up, model.actDetailUp},
	{detailKeys.Fold, model.actDetailFold},
	{detailKeys.Drill, model.actDetailDrill},
	{detailKeys.HalfDown, model.actDetailHalfDown},
	{detailKeys.HalfUp, model.actDetailHalfUp},
	{detailKeys.Top, model.actDetailTop},
	{detailKeys.Bottom, model.actDetailBottom},
}

func (m model) actDetailDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.topFrame()
	if f.items != nil {
		// Scroll within a cursor item taller than the viewport before advancing,
		// so long bodies stay reachable.
		if h, _, end, ok := m.cursorOverflow(f); ok && f.scroll < end-h {
			f.scroll++
		} else {
			f.cursor = min(len(f.items)-1, f.cursor+1)
			m.ensureDetailVisible()
		}
	} else {
		f.scroll++
	}
	return m, nil
}

func (m model) actDetailUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.topFrame()
	if f.items != nil {
		if _, start, _, ok := m.cursorOverflow(f); ok && f.scroll > start {
			f.scroll--
		} else {
			f.cursor = max(0, f.cursor-1)
			m.ensureDetailVisible()
		}
	} else if f.scroll > 0 {
		f.scroll--
	}
	return m, nil
}

// cursorOverflow reports whether the cursor item is taller than the visible
// height h, returning h and the item's [start,end) line range. h matches
// detailBody's content area so it agrees with ensureDetailVisible.
func (m model) cursorOverflow(f *detailFrame) (h, start, end int, ok bool) {
	_, start, end = m.frameLines(f, m.containerWidth())
	h = max(1, m.viewportHeight()-3)
	return h, start, end, end-start > h
}

func (m model) actDetailFold(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.topFrame()
	if f.items != nil && f.cursor >= 0 && f.cursor < len(f.items) {
		f.toggle(f.cursor)
		m.ensureDetailVisible()
		if f.isExpanded(f.cursor) {
			// Expanding a tool reveals its body; fetch on demand.
			return m, m.fetchToolBodyCmd(f.items[f.cursor], f.agentID)
		}
	}
	return m, nil
}

// subagentLabel returns the breadcrumb label for a subagent item.
func subagentLabel(it claudecode.Item) string {
	if it.SubagentType != "" {
		return it.SubagentType
	}
	return "Subagent"
}

func (m model) actDetailDrill(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.topFrame()
	if f == nil || f.items == nil || f.cursor < 0 || f.cursor >= len(f.items) {
		return m, nil
	}
	it := f.items[f.cursor]
	if it.Kind == claudecode.ItemSubagent && it.HasTrace && len(it.Trace) == 0 && it.AgentID != "" {
		if m.mode == modeHistoryTranscript {
			// Past session: one-shot fetch (no live subscription).
			m.transcript.detailStack = append(m.transcript.detailStack, detailFrame{
				label: subagentLabel(it), agentID: it.AgentID, expanded: map[int]bool{},
			})
			return m, m.fetchHistSubagent(m.history.openNodeID, m.history.openPath, it.AgentID)
		}
		// Live session: stream the subagent trace into a new frame. Stash the
		// session subRef so pop can restore it without a leak.
		m.sessionSub = m.activeSub
		ref := subRef{subID: newSubID(), sessionID: m.selectedID, agentID: it.AgentID}
		m.activeSub = ref // subagent stream is active while drilled in
		m.transcript.detailStack = append(m.transcript.detailStack, detailFrame{
			label: subagentLabel(it), subID: ref.subID, agentID: ref.agentID, expanded: map[int]bool{},
		})
		have := len(m.transcriptCache[ref.key()].chunks)
		return m, m.subscribeCmd(ref, have)
	}
	m.drillDetail() // inline (history) or focus a leaf item
	// Focusing a tool leaf shows its body expanded; fetch on demand.
	if nf := m.topFrame(); nf != nil && nf.focused && len(nf.items) == 1 {
		return m, m.fetchToolBodyCmd(nf.items[0], nf.agentID)
	}
	return m, nil
}

func (m model) actDetailHalfDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.topFrame().scroll += max(1, m.viewportHeight()/2)
	return m, nil
}

func (m model) actDetailHalfUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.topFrame()
	f.scroll = max(0, f.scroll-max(1, m.viewportHeight()/2))
	return m, nil
}

func (m model) actDetailTop(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.topFrame()
	f.scroll = 0
	if f.items != nil {
		f.cursor = 0
	}
	return m, nil
}

func (m model) actDetailBottom(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.topFrame()
	if f.items != nil {
		f.cursor = max(0, len(f.items)-1)
	}
	m.ensureDetailVisible()
	return m, nil
}

// frameLines renders all of a frame's items to display lines and returns the
// [start,end) line range of the cursor item (0,0 for a non-AI body frame).
func (m model) frameLines(f *detailFrame, width int) (lines []string, curStart, curEnd int) {
	if f.items == nil {
		return strings.Split(f.body, "\n"), 0, 0
	}
	for i, it := range f.items {
		if i > 0 {
			lines = append(lines, "") // blank separator
		}
		start := len(lines)
		block := m.detailRowBlock(it, f.isExpanded(i), i == f.cursor, width)
		lines = append(lines, strings.Split(block, "\n")...)
		if i == f.cursor {
			curStart, curEnd = start, len(lines)
		}
	}
	return lines, curStart, curEnd
}

// detailBreadcrumb renders the drill path (e.g. "opus4.8 › explorer › Read").
func (m model) detailBreadcrumb() string {
	var labels []string
	for i := range m.transcript.detailStack {
		labels = append(labels, m.transcript.detailStack[i].label)
	}
	return StyleDim.Render(strings.Join(labels, " › "))
}

// ensureDetailVisible scrolls the active frame so the cursor item stays on screen.
func (m *model) ensureDetailVisible() {
	f := m.topFrame()
	if f == nil || f.items == nil {
		return
	}
	lines, start, end := m.frameLines(f, m.containerWidth())
	h := max(1, m.viewportHeight()-3) // breadcrumb(2) + hint(1)
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

// scrollHint renders a right-aligned "▲ N   ▼ N" indicator for hidden lines.
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
func (m model) detailBody() string {
	cw, tw := m.containerWidth(), m.width
	f := m.topFrame()
	if f == nil {
		return centerBlock(dimStyle.Render("(nothing to show)"), cw, tw)
	}
	lines, _, _ := m.frameLines(f, cw)
	crumb := truncateLine(m.detailBreadcrumb(), cw)
	h := m.viewportHeight()
	bodyH := h
	prefix := ""
	if crumb != "" {
		prefix = crumb + "\n\n"
		bodyH = max(1, h-2)
	}
	if len(lines) <= bodyH {
		return centerBlock(prefix+strings.Join(lines, "\n"), cw, tw)
	}
	ch := max(1, bodyH-1) // reserve a row for the scroll indicator
	scroll := min(f.scroll, len(lines)-ch)
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + ch
	body := strings.Join(lines[scroll:end], "\n")
	hint := scrollHint(scroll, len(lines)-end, cw)
	return centerBlock(prefix+body+"\n"+hint, cw, tw)
}

// renderDetail renders a non-AI chunk's body for a root frame. (AI chunks are
// navigated as item frames instead.)
func (m model) renderDetail(c claudecode.Chunk) string {
	width := m.containerWidth()
	switch c.Kind {
	case claudecode.ChunkUser:
		head := StylePrimaryBold.Render("You") + " " + Icon.User.Render() + "  " + StyleDim.Render(clockTime(c.Timestamp))
		return head + "\n\n" + m.renderMD(c.Text, width-2)
	case claudecode.ChunkSystem:
		icon := Icon.System
		label := StyleSecondary.Render("System")
		if c.IsError {
			icon = Icon.SystemErr
			label = lipgloss.NewStyle().Foreground(ColorError).Render("System")
		}
		head := icon.Render() + " " + label + "  " + Icon.Dot.Glyph + "  " + StyleDim.Render(clockTime(c.Timestamp))
		if c.Detail == "" {
			return head
		}
		return head + "\n\n" + hardWrap(StyleDim.Render(strings.TrimRight(c.Detail, "\n")), width-2)
	default:
		head := Icon.System.Render() + " " + StyleSecondary.Render(c.Summary)
		if c.Detail == "" {
			return head
		}
		return head + "\n\n" + hardWrap(StyleDim.Render(strings.TrimRight(c.Detail, "\n")), width-2)
	}
}

// detailRowBlock renders one item: collapsed (one-line summary) or expanded (full
// body), accent rule brightened when selected.
func (m model) detailRowBlock(it claudecode.Item, expanded, selected bool, width int) string {
	c := itemAccentColor(it)
	bar := GlyphAccentBar
	if selected {
		c = ColorFocus
		bar = GlyphAccentBarFocused
	}
	if expanded {
		return m.detailItemBody(it, c, bar, width)
	}
	row := itemRow(it)
	if drillable(it) {
		row += "  " + StyleDim.Render("↵")
	}
	// One line; the gutter eats 2 cols.
	return accentBlock(truncateLine(row, max(width-2, 10)), c, bar)
}

// truncateLine caps a styled string to width columns on one line (ANSI-aware).
func truncateLine(s string, width int) string {
	return lipgloss.NewStyle().MaxWidth(max(width, 1)).Render(s)
}

// detailItemBody renders an item's expanded body under an accent rule of color c.
// The "┃ " gutter eats 2 cols, so inner width is width-2. Subagents show a header
// + drill hint (the trace is reached by drilling in, not inlined).
func (m model) detailItemBody(it claudecode.Item, c color.Color, bar string, width int) string {
	iw := max(width-2, 10)
	switch it.Kind {
	case claudecode.ItemThinking:
		head := Icon.Thinking.Render() + " " + StyleSecondaryBold.Render("Thinking")
		return accentBlock(head+"\n"+wrapDim(it.Text, iw), c, bar)
	case claudecode.ItemText:
		head := Icon.Output.Render() + " " + StyleSecondaryBold.Render("Output")
		return accentBlock(head+"\n"+m.renderMD(it.Text, iw), c, bar)
	case claudecode.ItemSubagent:
		name := it.SubagentType
		if name == "" {
			name = "Subagent"
		}
		head := Icon.Subagent.Render() + " " + StylePrimaryBold.Render(name)
		if it.SubagentDesc != "" {
			head += "  " + StyleSecondary.Render(truncate(it.SubagentDesc, 70))
		}
		if n := len(flattenTrace(it.Trace)); n > 0 {
			hint := StyleDim.Render(fmt.Sprintf("↵ drill into %d steps", n))
			return accentBlock(hardWrap(head+"\n"+hint, iw), c, bar)
		} else if it.HasTrace {
			hint := StyleDim.Render("↵ drill in (streaming)")
			return accentBlock(hardWrap(head+"\n"+hint, iw), c, bar)
		}
		body := m.toolBody(it, iw)
		return accentBlock(hardWrap(joinItem(head, body), iw), c, bar)
	default: // tool
		head := toolIcon(it.ToolName, it.ResultIsError).Render() + " " + StylePrimaryBold.Render(it.ToolName)
		if it.InputPreview != "" {
			head += "  " + StyleSecondary.Render(truncate(it.InputPreview, 70))
		}
		body := m.toolBody(it, iw)
		return accentBlock(hardWrap(joinItem(head, body), iw), c, bar)
	}
}

// joinItem joins header and body, omitting the separator when body is empty.
func joinItem(head, body string) string {
	if body == "" {
		return head
	}
	return head + "\n" + body
}

// toolBody renders a tool's input/result via a per-tool renderer or a generic
// layout. Heavy bodies are stripped from the wire and fetched on demand, so fill
// from the cache here; show a placeholder while a fetch is outstanding.
func (m model) toolBody(it claudecode.Item, width int) string {
	it, fetched := m.filledTool(it)
	if !fetched && it.ToolID != "" {
		return StyleDim.Render("loading…")
	}
	if body, ok := m.toolDetailBody(it, width); ok {
		return body
	}
	return m.genericToolBody(it, width)
}

// filledTool populates it's on-demand body fields from the cache. fetched
// distinguishes "not yet loaded" from "loaded but empty". Items with no ToolID
// (not addressable) are treated as already-resolved.
func (m model) filledTool(it claudecode.Item) (claudecode.Item, bool) {
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

// wrapDim word-wraps text to width and dims it.
func wrapDim(text string, width int) string {
	return lipgloss.NewStyle().Foreground(ColorTextDim).Width(max(width, 10)).Render(text)
}
