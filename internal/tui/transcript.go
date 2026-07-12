package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// Transcript viewer: one card per chunk, chunk-level cursor. Selection/expansion
// are keyed by stable chunk id so they survive the 1s refresh. Full per-item
// bodies (diffs, tool results) live in the detail drill-down (detail.go).

const (
	maxContentWidth   = 160 // cap card column width on very wide terminals
	maxCollapsedLines = 12  // lines shown for a collapsed text preview
)

// containerWidth is the width of the card column, centered within the terminal.
func (m model) containerWidth() int {
	w := m.width
	if w > maxContentWidth {
		w = maxContentWidth
	}
	if w < 24 {
		w = 24
	}
	return w
}

// contentPadX is the right-edge padding for session content (transcript cards and
// the dock body). The left edge carries the cursor-marker column instead, so
// content reads flush-left with a marker + 1-cell gap.
const contentPadX = 2

// transcriptWidth is the card column width: the container minus the right padding.
// centerBlock renders it against the full container, so on normal terminals the
// left gutter is 0 (marker at the edge) and the right gutter is contentPadX.
func (m model) transcriptWidth() int { return max(20, m.containerWidth()-contentPadX) }

// renderMD renders markdown at a wrap width. Caches both the per-width renderer
// and the output (keyed by width+content) so the refresh re-renders only changes.
func (m model) renderMD(text string, width int) string {
	if width < 10 {
		width = 10
	}
	key := strconv.Itoa(width) + "\x00" + text
	if v, ok := m.transcript.mdCache[key]; ok {
		return v
	}
	r := m.transcript.mdRenderers[width]
	if r == nil {
		nr, err := glamour.NewTermRenderer(
			glamour.WithStyles(glamourStyleConfig(m.hasDark)),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return strings.TrimRight(text, "\n")
		}
		m.transcript.mdRenderers[width] = nr
		r = nr
	}
	out, err := r.Render(text)
	if err != nil {
		out = text
	}
	out = strings.Trim(out, "\n")
	m.transcript.mdCache[key] = out
	return out
}

// glamourStyleConfig returns the markdown style for the detected background.
// Nils Document.Color so body text inherits the terminal foreground (the bundled
// dark style hardcodes a gray invisible on light terminals); zeroes the margin
// so cards aren't over-indented.
func glamourStyleConfig(hasDark bool) ansi.StyleConfig {
	cfg := styles.LightStyleConfig
	if hasDark {
		cfg = styles.DarkStyleConfig
	}
	cfg.Document.Color = nil
	zero := uint(0)
	cfg.Document.Margin = &zero
	return cfg
}

func (m model) chunkExpandable(c transcript.Chunk) bool {
	switch c.Kind {
	case transcript.ChunkAI:
		return len(c.Items) > 0
	case transcript.ChunkUser:
		if len(c.Items) > 0 {
			return true
		}
		// Count wrapped display lines, not source lines.
		return len(strings.Split(m.renderMD(c.Text, m.userBubbleInner()), "\n")) > maxCollapsedLines
	case transcript.ChunkSystem:
		return c.Detail != ""
	case transcript.ChunkShell:
		return c.Detail != "" || strings.Count(c.Text, "\n") >= maxCollapsedLines
	default:
		return false
	}
}

func (m model) chunkExpanded(c transcript.Chunk) bool {
	if v, ok := m.transcript.expanded[c.ID]; ok {
		return v
	}
	return false
}

func (m *model) toggleExpand(i int) {
	if i < 0 || i >= len(m.transcript.chunks) {
		return
	}
	c := m.transcript.chunks[i]
	if !m.chunkExpandable(c) {
		return
	}
	m.transcript.expanded[c.ID] = !m.chunkExpanded(c)
}

func (m *model) setAllExpanded(v bool) {
	for _, c := range m.transcript.chunks {
		if m.chunkExpandable(c) {
			m.transcript.expanded[c.ID] = v
		}
	}
}

// currentChunkID returns the id of the selected chunk (for cursor preservation).
func (m model) currentChunkID() string {
	if m.transcript.cursor >= 0 && m.transcript.cursor < len(m.transcript.chunks) {
		return m.transcript.chunks[m.transcript.cursor].ID
	}
	return ""
}

// -- Rendering helpers --------------------------------------------------------

func chevron(expanded bool) string {
	if expanded {
		return Icon.Expanded.Render()
	}
	return Icon.Collapsed.Render()
}

func selIndicator(selected bool) string {
	if selected {
		return Icon.Selected.Render() + " "
	}
	return "  "
}

// spaceBetween lays out left and right with gap-fill spacing to span width.
func spaceBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// indentBlock prefixes every line of a block with indent.
func indentBlock(text, indent string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}

// centerBlock left-pads each line so a contentWidth-wide block sits centered in
// termWidth. No-op when content already fills the terminal.
func centerBlock(content string, contentWidth, termWidth int) string {
	gutter := (termWidth - contentWidth) / 2
	if gutter <= 0 {
		return content
	}
	pad := strings.Repeat(" ", gutter)
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// centerLine left-pads a single line so it sits centered in width. No-op when the
// line already fills width.
func centerLine(line string, width int) string {
	pad := (width - lipgloss.Width(line)) / 2
	if pad <= 0 {
		return line
	}
	return strings.Repeat(" ", pad) + line
}

// pinFooter stacks body and a width-centered footer on the last rows of a
// height-tall viewport. A footer wider than the terminal wraps onto multiple lines
// (rather than clipping) and the gap shrinks to keep them all on-screen.
func pinFooter(body, footer string, width, height int) string {
	footer = xansi.Wrap(footer, max(1, width), "")
	fLines := strings.Split(footer, "\n")
	for i, l := range fLines {
		fLines[i] = centerLine(l, width)
	}
	// Reserve fH rows for the footer: total = bodyH + gap + (fH-1) == height.
	gap := max(1, height-lipgloss.Height(body)-(len(fLines)-1))
	return body + strings.Repeat("\n", gap) + strings.Join(fLines, "\n")
}

// truncateLines caps content to maxLines, returning the text and hidden count.
func truncateLines(content string, maxLines int) (string, int) {
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content, 0
	}
	return strings.Join(lines[:maxLines], "\n"), len(lines) - maxLines
}

func hiddenHint(n int) string {
	return StyleDim.Render(fmt.Sprintf("%s (%d lines hidden)", Icon.Ellipsis.Glyph, n))
}

// -- Chunk rendering ----------------------------------------------------------

// renderChunk renders one chunk to a styled multi-line block (no centering). The
// cursor card keeps its selection indicator regardless of focus but only takes
// the accent border when the history region is focused.
func (m model) renderChunk(i int, selected bool) string {
	c := m.transcript.chunks[i]
	accent := selected && m.historyFocused()
	switch c.Kind {
	case transcript.ChunkAI:
		return m.renderAICard(c, selected, accent)
	case transcript.ChunkUser:
		return m.renderUserCard(c, selected, accent)
	case transcript.ChunkShell:
		return m.renderShellCard(c, selected, accent)
	case transcript.ChunkCompact:
		return m.renderCompact(c)
	default:
		return m.renderSystem(c, selected, accent)
	}
}

func (m model) renderAICard(c transcript.Chunk, selected, accent bool) string {
	container := m.transcriptWidth()
	fraction := 3 * container / 4
	if container < maxContentWidth {
		fraction = 7 * container / 8
	}
	cardW := fraction - 4
	if cardW < 24 {
		cardW = 24
	}
	cw := max(cardW-6, 20)

	sel := selIndicator(selected)
	header := sel + m.aiHeader(c, cardW)
	body := m.aiBody(c, cw)

	borderColor := ColorBorder
	if accent {
		borderColor = ColorAccent
	}
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardW).
		Padding(0, 2).
		Render(body)

	return header + "\n" + indentBlock(card, sel)
}

// assistantBrand uses m.history.openAgent in history mode because the live
// session isn't in m.sessions.
func (m model) assistantBrand() (StyledIcon, string) {
	agent := m.sessions[m.selectedID].Agent
	if m.mode == modeHistoryTranscript {
		agent = m.history.openAgent
	}
	switch agent {
	case "codex":
		return Icon.Claude, "Codex"
	case "antigravity":
		return Icon.Claude, "Antigravity"
	default:
		return Icon.Claude, "Claude"
	}
}

func (m model) aiHeader(c transcript.Chunk, width int) string {
	chev := ""
	if m.chunkExpandable(c) {
		chev = chevron(m.chunkExpanded(c)) + " "
	}
	icon, name := m.assistantBrand()
	left := chev + icon.RenderBold() + " " + StylePrimaryBold.Render(name)
	if c.ModelName != "" {
		left += " " + lipgloss.NewStyle().Foreground(modelColorOf(c.ModelColor)).Render(c.ModelName)
	}
	left += aiStats(c)
	return spaceBetween(left, aiMeta(c), width)
}

func aiStats(c transcript.Chunk) string {
	var parts []string
	if c.Thinking > 0 {
		parts = append(parts, Icon.Thinking.Render()+" "+StyleSecondary.Render(strconv.Itoa(c.Thinking)))
	}
	if c.ToolCount > 0 {
		parts = append(parts, Icon.Tool.Ok.Render()+" "+StyleSecondary.Render(strconv.Itoa(c.ToolCount)))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + Icon.Dot.Render() + " " + strings.Join(parts, "  ")
}

func aiMeta(c transcript.Chunk) string {
	var parts []string
	if c.Usage.Output > 0 {
		parts = append(parts, Icon.Token.Render()+" "+StyleSecondary.Render(formatTokens(c.Usage.Output)))
	}
	if ctx := formatContext(c); ctx != "" {
		parts = append(parts, ctx)
	}
	if c.DurationMs > 0 {
		parts = append(parts, Icon.Clock.Render()+" "+StyleSecondary.Render(formatDuration(c.DurationMs)))
	}
	if ts := clockTime(c.Timestamp); ts != "" {
		parts = append(parts, StyleDim.Render(ts))
	}
	return strings.Join(parts, "  ")
}

func (m model) aiBody(c transcript.Chunk, cw int) string {
	if m.chunkExpanded(c) {
		var rows []string
		for _, it := range c.Items {
			rows = append(rows, itemRow(it))
		}
		if lo, ok := c.LastOutput(); ok && lo.Kind == transcript.ItemText {
			rows = append(rows, "", m.renderMD(lo.Text, cw)) // expanded: full output
		}
		return strings.Join(rows, "\n")
	}

	// Collapsed: preview the last meaningful output.
	lo, ok := c.LastOutput()
	if !ok {
		text, hidden := truncateLines(c.Text, maxCollapsedLines)
		out := m.renderMD(text, cw)
		if hidden > 0 {
			out += "\n" + hiddenHint(hidden)
		}
		return out
	}
	switch lo.Kind {
	case transcript.ItemText:
		text, hidden := truncateLines(lo.Text, maxCollapsedLines)
		out := m.renderMD(text, cw)
		if hidden > 0 {
			out += "\n" + hiddenHint(hidden)
		}
		return out
	default: // tool / subagent
		return toolPreview(lo)
	}
}

func itemRow(it transcript.Item) string {
	var indicator, name, summary string
	padName := true
	switch it.Kind {
	case transcript.ItemThinking:
		indicator, name, summary = Icon.Thinking.Render(), "Thinking", firstLine(it.Text)
	case transcript.ItemText:
		indicator, name, summary = Icon.Output.Render(), "Output", firstLine(it.Text)
	case transcript.ItemPrompt:
		indicator, name, summary = Icon.User.Render(), "Prompt", firstLine(it.Text)
	case transcript.ItemSubagent:
		if s, ok := soleSubagent(it); ok && s.IsTeammate {
			nameStr := lipgloss.NewStyle().Bold(true).Foreground(teamColor(s.Color)).Render(s.Name)
			summary = firstLine(it.Text)
			if s.Idle {
				summary = "is done"
			}
			if summary == "" {
				return Icon.Teammate.Render() + " " + nameStr
			}
			return Icon.Teammate.Render() + " " + nameStr + " " + StyleSecondary.Render(truncate(summary, 60))
		}
		indicator = Icon.Subagent.Render()
		if isAgentRefTool(it.ToolName) {
			name = agentToolLabel(it)
		} else {
			s, _ := soleSubagent(it)
			name = spawnAgentLabel(s.Type, s.Name)
		}
		padName = false // full identity label, not a short name column
	default: // tool
		indicator = toolIcon(it.ToolName, it.ResultIsError).Render()
		name = toolDisplayName(it.ToolName)
		summary = it.InputPreview
	}
	nameFmt := name
	if padName {
		nameFmt = fmt.Sprintf("%-12s", name)
	}
	nameStr := StylePrimaryBold.Render(nameFmt)
	if summary == "" {
		return indicator + " " + nameStr
	}
	return indicator + " " + nameStr + " " + StyleSecondary.Render(truncate(summary, 60))
}

func toolPreview(it transcript.Item) string {
	icon := toolIcon(it.ToolName, it.ResultIsError)
	res := it.Result
	if res == "" || it.Kind == transcript.ItemSkill {
		res = it.InputPreview // skill result is the file body; preview its identifier
	}
	res = strings.ReplaceAll(res, "\n", " ")
	name := toolDisplayName(it.ToolName)
	if s, ok := soleSubagent(it); ok && s.Type != "" {
		name = s.Type
	}
	out := icon.Render() + " " + StylePrimaryBold.Render(name)
	if res != "" {
		out += " " + StyleSecondary.Render(truncate(res, 80))
	}
	return out
}

func (m model) userBubbleWidth() int {
	return max(m.transcriptWidth()*3/4, 20)
}

func (m model) userBubbleInner() int {
	return max(m.userBubbleWidth()-6, 20)
}

func (m model) renderUserCard(c transcript.Chunk, selected, accent bool) string {
	container := m.transcriptWidth()
	maxBubble := m.userBubbleWidth()
	sel := selIndicator(selected)
	expandable := m.chunkExpandable(c)
	expanded := m.chunkExpanded(c)

	chev := ""
	if expandable {
		chev = chevron(expanded) + " "
	}
	right := StyleDim.Render(clockTime(c.Timestamp)) + "  " + chev +
		StylePrimaryBold.Render("You") + " " + Icon.User.Render()
	gap := container - lipgloss.Width(sel) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	header := sel + strings.Repeat(" ", gap) + right

	body := m.renderMD(c.Text, m.userBubbleInner())
	if expanded {
		for _, it := range c.Items {
			row := itemRow(it)
			if body == "" {
				body = row
			} else {
				body += "\n" + row
			}
		}
	} else {
		// Truncate wrapped display lines so long single lines collapse too.
		if t, hidden := truncateLines(body, maxCollapsedLines); hidden > 0 {
			body = t + "\n" + hiddenHint(hidden)
		}
	}

	borderColor := ColorTextMuted
	if accent {
		borderColor = ColorAccent
	}
	bubble := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(maxBubble).
		Padding(0, 2).
		Render(body)

	aligned := lipgloss.PlaceHorizontal(container-lipgloss.Width(sel), lipgloss.Right, bubble)
	return header + "\n" + indentBlock(aligned, sel)
}

func (m model) renderSystem(c transcript.Chunk, selected, accent bool) string {
	container := m.transcriptWidth()
	fraction := 3 * container / 4
	if container < maxContentWidth {
		fraction = 7 * container / 8
	}
	cardW := max(fraction-4, 24) // match the AI card's right edge

	icon := Icon.System
	label := StyleSecondary.Render("System")
	if c.IsError {
		icon = Icon.SystemErr
		label = lipgloss.NewStyle().Foreground(ColorError).Render("System")
	}
	body := icon.Render() + " " + label + "  " +
		Icon.Dot.Glyph + "  " + StyleDim.Render(clockTime(c.Timestamp))
	if c.Label != "" { // preview after the timestamp (e.g. "Recap")
		body += "  " + StyleDim.Render(c.Label)
	}
	if m.chunkExpanded(c) && c.Detail != "" {
		body += "\n" + indentBlock(StyleDim.Render(strings.TrimRight(c.Detail, "\n")), "  ")
	}

	borderColor := ColorBorder
	if accent {
		borderColor = ColorAccent
	}
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardW).
		Padding(0, 2).
		Render(body)

	return indentBlock(card, selIndicator(selected))
}

func (m model) renderShellCard(c transcript.Chunk, selected, accent bool) string {
	container := m.transcriptWidth()
	fraction := 3 * container / 4
	if container < maxContentWidth {
		fraction = 7 * container / 8
	}
	cardW := max(fraction-4, 24)
	iw := max(cardW-4, 10) // card padding(0,2) eats 4 cols

	sel := selIndicator(selected)
	header := sel + m.shellHeader(c, cardW)
	body := m.shellBody(c, iw)

	borderColor := ColorBorder
	if accent {
		borderColor = ColorAccent
	}
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardW).
		Padding(0, 2).
		Render(body)

	return header + "\n" + indentBlock(card, sel)
}

func (m model) shellHeader(c transcript.Chunk, width int) string {
	chev := ""
	if m.chunkExpandable(c) {
		chev = chevron(m.chunkExpanded(c)) + " "
	}
	label := StylePrimaryBold.Render("Shell")
	if c.IsError {
		label = lipgloss.NewStyle().Bold(true).Foreground(ColorError).Render("Shell")
	}
	left := chev + Icon.Shell.Render() + " " + label
	return spaceBetween(left, StyleDim.Render(clockTime(c.Timestamp)), width)
}

func (m model) shellBody(c transcript.Chunk, iw int) string {
	if !m.chunkExpanded(c) {
		text, hidden := truncateLines(c.Text, maxCollapsedLines)
		body := StyleSecondaryBold.Render("$") + " " + text
		if hidden > 0 {
			body += "\n" + hiddenHint(hidden)
		}
		return body
	}
	var sb strings.Builder
	sb.WriteString(StyleSecondaryBold.Render("$"))
	sb.WriteString(" " + c.Text + "\n")
	if c.Detail != "" {
		label := "Result"
		if c.IsError {
			label = "Error"
		}
		sb.WriteString("\n" + sectionLabel(label, c.IsError) + "\n")
		sb.WriteString(m.execCommandResultBody(c.Detail, iw))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) renderCompact(c transcript.Chunk) string {
	container := m.transcriptWidth()
	text := c.Summary
	if text == "" {
		text = "Context compressed"
	}
	tw := lipgloss.Width(text) + 2
	leftPad := (container - tw) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	rightPad := container - leftPad - tw
	if rightPad < 0 {
		rightPad = 0
	}
	return StyleMuted.Render(strings.Repeat(GlyphHRule, leftPad) + " " + text + " " + strings.Repeat(GlyphHRule, rightPad))
}

// -- Layout, view, scrolling --------------------------------------------------

// layoutChunks renders every chunk to display lines, recording each chunk's
// first line index (for cursor scrolling). A blank separator precedes each card.
func (m model) layoutChunks() (lines []string, first []int) {
	first = make([]int, len(m.transcript.chunks))
	for i := range m.transcript.chunks {
		if i > 0 {
			lines = append(lines, "")
		}
		first[i] = len(lines)
		block := centerBlock(m.renderChunk(i, i == m.transcript.cursor), m.containerWidth(), m.width)
		lines = append(lines, strings.Split(block, "\n")...)
	}
	return lines, first
}

// viewportHeight is the line count of the scrollable history region. On the
// session screen it equals the layout's history height so scroll math matches
// what sessionView renders. NOTE: sessionLayout must not call this (recursion).
func (m model) viewportHeight() int {
	if m.mode == modeSession {
		h, _ := m.sessionLayout()
		return h
	}
	return max(1, m.height-5)
}

// chunkSpan returns the [start,end) line range of chunk i within first/total.
func chunkSpan(i int, first []int, total int) (int, int) {
	start := first[i]
	end := total
	if i+1 < len(first) {
		end = first[i+1] - 1 // exclude the blank separator before the next chunk
	}
	return start, end
}

// selectedChunkOverflow returns the selected chunk's [start,end) line span,
// viewport height, and whether it overflows. j/k scroll an oversized card before
// moving the selection.
func (m model) selectedChunkOverflow() (start, end, h int, overflow bool) {
	lines, first := m.layoutChunks()
	h = m.viewportHeight()
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(first) {
		return 0, 0, h, false
	}
	start, end = chunkSpan(m.transcript.cursor, first, len(lines))
	return start, end, h, end-start > h
}

// ensureChunkVisible scrolls so the selected chunk sits within the viewport.
func (m *model) ensureChunkVisible() {
	lines, first := m.layoutChunks()
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(first) {
		return
	}
	h := m.viewportHeight()
	start, end := chunkSpan(m.transcript.cursor, first, len(lines))
	if start < m.transcript.scroll {
		m.transcript.scroll = start
	} else if end > m.transcript.scroll+h {
		m.transcript.scroll = end - h
		if m.transcript.scroll > start {
			m.transcript.scroll = start // tall chunk: pin to its top
		}
	}
	m.clampScroll(len(lines), h)
}

// cursorVisible reports whether the selected chunk overlaps the current viewport.
func (m model) cursorVisible() bool {
	lines, first := m.layoutChunks()
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(first) {
		return false
	}
	start, end := chunkSpan(m.transcript.cursor, first, len(lines))
	return start < m.transcript.scroll+m.viewportHeight() && end > m.transcript.scroll
}

// chunkAtLine returns the index of the chunk whose span contains the given line
// (the fallback when a single chunk is taller than the viewport).
func (m model) chunkAtLine(line int) int {
	_, first := m.layoutChunks()
	idx := 0
	for i, s := range first {
		if s <= line {
			idx = i
		}
	}
	return idx
}

// firstVisibleChunk/lastVisibleChunk return the first/last chunk starting within
// the viewport, falling back to chunkAtLine(scroll) when a tall chunk fills it.
// j/k use these to re-anchor selection after line-scrolling.
func (m model) firstVisibleChunk() int {
	_, first := m.layoutChunks()
	h := m.viewportHeight()
	for i, s := range first {
		if s >= m.transcript.scroll && s < m.transcript.scroll+h {
			return i
		}
	}
	return m.chunkAtLine(m.transcript.scroll)
}

func (m model) lastVisibleChunk() int {
	_, first := m.layoutChunks()
	h := m.viewportHeight()
	last := -1
	for i, s := range first {
		if s >= m.transcript.scroll && s < m.transcript.scroll+h {
			last = i
		}
	}
	if last < 0 {
		return m.chunkAtLine(m.transcript.scroll)
	}
	return last
}

func (m *model) clampScroll(total, h int) {
	if maxScroll := max(0, total-h); m.transcript.scroll > maxScroll {
		m.transcript.scroll = maxScroll
	}
	if m.transcript.scroll < 0 {
		m.transcript.scroll = 0
	}
}

// clampScrollNow clamps the line scroll to the current layout's valid range.
func (m *model) clampScrollNow() {
	lines, _ := m.layoutChunks()
	m.clampScroll(len(lines), m.viewportHeight())
}

// maxScroll returns the largest valid top-line offset for the current layout.
func (m model) maxScroll() int {
	lines, _ := m.layoutChunks()
	return max(0, len(lines)-m.viewportHeight())
}

func (m *model) clampCursor() {
	if m.transcript.cursor >= len(m.transcript.chunks) {
		m.transcript.cursor = max(0, len(m.transcript.chunks)-1)
	}
	if m.transcript.cursor < 0 {
		m.transcript.cursor = 0
	}
}

// restoreChunkCursor re-resolves the cursor to the same chunk id after a refresh
// without moving the viewport. When follow is true the view pins to the bottom so
// a live session keeps tailing.
func (m *model) restoreChunkCursor(id string, follow bool) {
	m.transcript.cursor = -1
	if id != "" {
		for i, c := range m.transcript.chunks {
			if c.ID == id {
				m.transcript.cursor = i
				break
			}
		}
	}
	if m.transcript.cursor < 0 {
		m.clampCursor()
	}
	if follow {
		m.transcript.scroll = m.maxScroll()
	}
	m.clampScrollNow()
}

// transcriptBody renders the transcript pane.
func (m model) transcriptBody() string {
	var b strings.Builder

	if m.transcript.err != nil {
		b.WriteString(dimStyle.Render("transcript unavailable: " + m.transcript.err.Error()))
		return b.String()
	}
	if len(m.transcript.chunks) == 0 {
		b.WriteString(dimStyle.Render("(no transcript yet)"))
		return b.String()
	}

	lines, _ := m.layoutChunks()
	h := m.viewportHeight()
	scroll := m.transcript.scroll
	if maxScroll := max(0, len(lines)-h); scroll > maxScroll {
		scroll = maxScroll
	}
	end := min(len(lines), scroll+h)
	b.WriteString(strings.Join(lines[scroll:end], "\n"))
	return b.String()
}

// -- Edit diff rendering (used by the detail drill-down view) ------------------

// editDiff renders the input of an edit-like tool (Edit/MultiEdit/Write/
// NotebookEdit) as a colored diff. Returns ok=false for any other tool.
func editDiff(name, input string) (string, bool) {
	switch name {
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
	default:
		return "", false
	}
	if input == "" {
		return "", false
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", false
	}

	var sb strings.Builder
	if path := str(in["file_path"], in["notebook_path"]); path != "" {
		sb.WriteString(dimStyle.Render("● "+path) + "\n")
	}

	switch name {
	case "Edit":
		oldS, newS := str(in["old_string"]), str(in["new_string"])
		if oldS == "" && newS == "" {
			return "", false
		}
		if ra, _ := in["replace_all"].(bool); ra {
			sb.WriteString(dimStyle.Render("  (replace all)") + "\n")
		}
		sb.WriteString(strings.Join(lineDiff(oldS, newS), "\n"))
	case "MultiEdit":
		edits, ok := in["edits"].([]any)
		if !ok || len(edits) == 0 {
			return "", false
		}
		for i, e := range edits {
			em, _ := e.(map[string]any)
			if i > 0 {
				sb.WriteString("\n" + dimStyle.Render("  ─── edit "+strconv.Itoa(i+1)+" ───") + "\n")
			}
			sb.WriteString(strings.Join(lineDiff(str(em["old_string"]), str(em["new_string"])), "\n"))
		}
	case "Write":
		content := str(in["content"])
		if content == "" {
			return "", false
		}
		sb.WriteString(strings.Join(addedLines(content), "\n"))
	case "NotebookEdit":
		src := str(in["new_source"])
		if src == "" {
			return "", false
		}
		sb.WriteString(strings.Join(addedLines(src), "\n"))
	}
	return sb.String(), true
}

// lineDiff produces a line-level diff (LCS) between old and new text: context
// lines are plain, removals are red "- ", additions are green "+ ".
func lineDiff(oldS, newS string) []string {
	a, b := splitLines(oldS), splitLines(newS)
	n, mm := len(a), len(b)
	diffDel := lipgloss.NewStyle().Foreground(ColorDiffDel)
	diffAdd := lipgloss.NewStyle().Foreground(ColorDiffAdd)
	if n+mm > 2000 {
		out := make([]string, 0, n+mm)
		for _, l := range a {
			out = append(out, diffDel.Render("- "+l))
		}
		return append(out, addedLines(newS)...)
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, mm+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := mm - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < mm {
		switch {
		case a[i] == b[j]:
			out = append(out, "  "+a[i])
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffDel.Render("- "+a[i]))
			i++
		default:
			out = append(out, diffAdd.Render("+ "+b[j]))
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffDel.Render("- "+a[i]))
	}
	for ; j < mm; j++ {
		out = append(out, diffAdd.Render("+ "+b[j]))
	}
	return out
}

func addedLines(s string) []string {
	diffAdd := lipgloss.NewStyle().Foreground(ColorDiffAdd)
	lines := splitLines(s)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = diffAdd.Render("+ " + l)
	}
	return out
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// str returns the first non-empty string among the given values.
func str(vals ...any) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	return s
}
