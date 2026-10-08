package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// Transcript viewer: a flat stream of display rows (entries, with runs of
// thinking/tool calls folded; see buildRows) and a row-level cursor.
// Expansion is keyed by stable entry id or run key so it survives the 1s
// refresh. Full per-entry bodies live in the detail drill-down (detail.go).

const (
	maxContentWidth   = 160 // cap content column width on very wide terminals
	maxCollapsedLines = 12  // lines shown for a collapsed text preview
)

// containerWidth is the width of the content column, centered within the terminal.
func (m model) containerWidth() int { return containerWidthOf(m.bodyWidth()) }

func containerWidthOf(w int) int {
	if w > maxContentWidth {
		w = maxContentWidth
	}
	if w < 24 {
		w = 24
	}
	return w
}

// contentPadX is the right-edge padding for session content (transcript entries and
// the dock body). The left edge carries the cursor-marker column instead, so
// content reads flush-left with a marker + 1-cell gap.
const contentPadX = 2

// transcriptWidth is the content column width: the container minus the right padding.
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
	if v, ok := m.render.mdCache[key]; ok {
		return v
	}
	r := m.render.mdRenderers[width]
	if r == nil {
		nr, err := glamour.NewTermRenderer(
			glamour.WithStyles(glamourStyleConfig(m.hasDark)),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return strings.TrimRight(text, "\n")
		}
		m.render.mdRenderers[width] = nr
		r = nr
	}
	out, err := r.Render(text)
	if err != nil {
		out = text
	}
	out = strings.Trim(out, "\n")
	m.render.mdCache[key] = out
	return out
}

// glamourStyleConfig returns the markdown style for the detected background.
// Nils Document.Color so body text inherits the terminal foreground (the bundled
// dark style hardcodes a gray invisible on light terminals); zeroes the margin
// so entries aren't over-indented.
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

func (m tview) entryExpandable(e transcript.Entry) bool {
	switch e.Kind {
	case transcript.EntryThinking:
		return strings.TrimSpace(e.Text) != ""
	case transcript.EntryTool, transcript.EntrySkill:
		return true
	case transcript.EntrySubagent:
		if s, ok := soleSubagent(e); ok && s.IsTeammate {
			// A long agent message (e.g. a subagent's report) folds like a prompt.
			body := m.c.m.renderMD(e.Text, teammateWrapWidth(m.c.m.transcriptWidth()))
			return !s.Idle && strings.TrimSpace(e.Text) != "" && strings.Count(body, "\n")+1 > maxCollapsedLines
		}
		return true
	case transcript.EntryUser:
		return userLineCount(e.Text, m.c.m.transcriptWidth()) > maxCollapsedLines
	case transcript.EntrySystem:
		return e.Detail != ""
	case transcript.EntryShell:
		return e.Detail != "" || strings.Count(e.Text, "\n") >= maxCollapsedLines
	default:
		return false
	}
}

func (m tview) entryExpanded(e transcript.Entry) bool {
	return m.transcript.expanded[e.ID]
}

func (m tview) setExpanded(i int, on bool) {
	if i < 0 || i >= len(m.transcript.entries) {
		return
	}
	e := m.transcript.entries[i]
	if !m.entryExpandable(e) {
		return
	}
	m.transcript.expanded[e.ID] = on
}

func (m tview) displayRows() []displayRow {
	return buildRows(m.transcript.entries, m.c.m.verboseTranscript, m.transcript.runs)
}

func (m tview) cursorRow() (displayRow, bool) {
	rows := m.displayRows()
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(rows) {
		return displayRow{}, false
	}
	return rows[m.transcript.cursor], true
}

// currentRowID returns the cursor row's identity and whether it is the last row.
func (m tview) currentRowID() (ref rowRef, last bool) {
	rows, c := m.displayRows(), m.transcript.cursor
	if c < 0 || c >= len(rows) {
		return rowRef{}, false
	}
	return rowID(m.transcript.entries, rows[c]), c == len(rows)-1
}

// setRun expands or collapses run key, keeping the cursor on the run's summary
// or head row, and fetches the bodies of expanded tools it reveals.
func (m tview) setRun(key string, on bool) tea.Cmd {
	m.transcript.runs[key] = on
	rows := m.displayRows()
	if i := runRowIndex(rows, key); i >= 0 {
		m.transcript.cursor = i
	}
	m.ensureEntryVisible()
	if !on {
		return nil
	}
	m.revealRun(key)
	var cmds []tea.Cmd
	for _, r := range rows {
		if r.kind == entryRow && r.run.key == key {
			cmds = append(cmds, m.fetchIfExpandedTool(r.entry))
		}
	}
	return tea.Batch(cmds...)
}

func runEdges(rows []displayRow, key string) (head, foot int, ok bool) {
	head, foot = -1, -1
	for i, r := range rows {
		if r.run.key != key {
			continue
		}
		switch r.kind {
		case runHead:
			head = i
		case runFoot:
			foot = i
		}
	}
	return head, foot, head >= 0 && foot >= 0
}

// revealRun scrolls a just-expanded run into view: as much of it as fits, from
// its head down.
func (m tview) revealRun(key string) {
	head, foot, ok := runEdges(m.displayRows(), key)
	if !ok {
		return
	}
	lines, first := m.layoutEntries()
	start, _ := itemSpan(head, first, len(lines))
	_, end := itemSpan(foot, first, len(lines))
	m.transcript.scroll = windowScroll(len(lines), start, end, m.viewportHeight(), m.transcript.scroll)
}

func (m tview) toggleRunRow(r displayRow) tea.Cmd {
	return m.setRun(r.run.key, r.kind == runSummary)
}

// -- Rendering helpers --------------------------------------------------------

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

// center places a view's content column in the middle of the view's width (the
// pane when framed).
func (m model) center(content string, contentWidth int) string {
	return centerBlock(content, contentWidth, m.bodyWidth())
}

func centerGutter(contentWidth, termWidth int) int { return max(0, (termWidth-contentWidth)/2) }

// centerBlock left-pads each line so a contentWidth-wide block sits centered in
// termWidth. No-op when content already fills the terminal.
func centerBlock(content string, contentWidth, termWidth int) string {
	gutter := centerGutter(contentWidth, termWidth)
	if gutter == 0 {
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

func hiddenText(n int) string { return fmt.Sprintf("%s (%d lines hidden)", Icon.Ellipsis.Glyph, n) }

func hiddenHint(n int) string { return StyleDim.Render(hiddenText(n)) }

// -- Entry rendering ----------------------------------------------------------

// gutterBar is the cursor column every entry block starts with: an accent bar
// on the selected entry (dim when the transcript is not focused), else blank.
func gutterBar(selected, focused bool) string {
	if !selected {
		return strings.Repeat(" ", detailGutter)
	}
	c := ColorBorder
	if focused {
		c = ColorAccent
	}
	return lipgloss.NewStyle().Foreground(c).Render(GlyphAccentBarFocused) + " "
}

// renderRow renders a display row of the main stream (no centering).
func (m tview) renderRow(r displayRow, selected bool) string {
	if r.isControl() {
		return controlBlock(r, selected, m.c.m.historyFocused(), m.c.m.transcriptWidth())
	}
	return m.renderEntry(r.entry, selected)
}

// renderEntry renders entry i of the main stream (no centering).
func (m tview) renderEntry(i int, selected bool) string {
	e := m.transcript.entries[i]
	return m.entryBlock(e, m.entryExpanded(e), selected, m.c.m.historyFocused(), false, m.c.m.transcriptWidth())
}

// entryBlock renders one entry, gutter included, at width. full shows a tool's
// whole body (the focused detail frame) instead of the first lines.
func (m tview) entryBlock(e transcript.Entry, expanded, selected, focused, full bool, width int) string {
	iw := max(width-detailGutter, 10)
	var body string
	if e.Kind == transcript.EntryUser {
		body = userBand(e, expanded, iw)
	} else {
		body = m.entryContent(e, expanded, full, iw)
	}
	return indentBlock(body, gutterBar(selected, focused))
}

func (m tview) entryContent(e transcript.Entry, expanded, full bool, iw int) string {
	switch e.Kind {
	case transcript.EntryText:
		return hang(Icon.Output.Render(), m.c.m.renderMD(e.Text, max(iw-2, 10)))
	case transcript.EntryThinking:
		head := Icon.Thinking.Render() + " " + StyleDim.Render("Thinking…")
		if !expanded || strings.TrimSpace(e.Text) == "" {
			return head
		}
		return head + "\n" + indentBlock(wrapDim(e.Text, iw-2), "  ")
	case transcript.EntryTurnEnd:
		return turnEndLine(e, iw)
	case transcript.EntrySystem:
		return systemRow(e, expanded, iw)
	case transcript.EntryShell:
		return m.shellRow(e, expanded, iw)
	case transcript.EntryCompact:
		return renderCompact(e, iw)
	case transcript.EntrySubagent:
		if s, ok := soleSubagent(e); ok && s.IsTeammate {
			return m.teammateRow(e, s, expanded || full, iw)
		}
	}
	return m.callRow(e, expanded, full, iw)
}

// callRow renders a tool, skill, or subagent op.
func (m tview) callRow(e transcript.Entry, expanded, full bool, iw int) string {
	row := itemRow(e)
	if drillable(e) {
		row += "  " + StyleDim.Render("↵")
	}
	row = truncateLine(row, iw)
	if !expanded {
		return row
	}
	var body string
	if s, ok := soleSubagent(e); ok && e.Kind == transcript.EntrySubagent && !isAgentRefTool(e.ToolName) {
		// The row above already names the agent; under it go only the status
		// and the Input section from the spawn's header block.
		parts := subagentHeaderLines(s.Type, s.Name, "", s.Desc, iw-2)[1:]
		if s.Status != "" {
			parts = append([]string{StyleSecondary.Render("[" + s.Status + "]")}, parts...)
		}
		if !s.HasTrace {
			if b := m.toolBody(e, iw-2); b != "" {
				parts = append(parts, b)
			}
		}
		body = strings.Join(parts, "\n")
	} else {
		body = hardWrap(m.toolBody(e, iw-2), iw-2)
	}
	if !full {
		if t, hidden := truncateLines(body, maxCollapsedLines); hidden > 0 {
			body = t + "\n" + hiddenHint(hidden)
		}
	}
	if body == "" {
		return row
	}
	return row + "\n" + indentBlock(body, "  ")
}

// hang prefixes a block's first line with icon and indents the rest under it.
func hang(icon, block string) string {
	pad := strings.Repeat(" ", lipgloss.Width(icon)+1)
	lines := strings.Split(block, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = icon + " " + lines[i]
		} else {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// userTextWidth is the wrap width of a user prompt inside its band.
func userTextWidth(width int) int { return max(width-detailGutter-4, 10) }

func userLineCount(text string, width int) int {
	return strings.Count(xansi.Wrap(strings.TrimRight(strings.ReplaceAll(text, "\r", ""), "\n"), userTextWidth(width), ""), "\n") + 1
}

// userBand renders a prompt as a full-width shaded band. Every piece carries
// the background so inner style resets don't punch holes in the band.
func userBand(e transcript.Entry, expanded bool, iw int) string {
	bg := lipgloss.NewStyle().Background(ColorUserBg)
	textW := max(iw-4, 10)
	head := bg.Foreground(Icon.User.Color).Render(Icon.User.Glyph) +
		bg.Bold(true).Foreground(ColorTextPrimary).Render(" You")
	clock := bg.Foreground(ColorTextDim).Render(clockTime(e.Timestamp))
	gap := max(1, textW-lipgloss.Width(head)-lipgloss.Width(clock))
	lines := []string{head + bg.Render(strings.Repeat(" ", gap)) + clock}

	body := xansi.Wrap(strings.TrimRight(strings.ReplaceAll(e.Text, "\r", ""), "\n"), textW, "")
	if !expanded {
		if t, hidden := truncateLines(body, maxCollapsedLines); hidden > 0 {
			body = t + "\n" + bg.Foreground(ColorTextDim).Render(hiddenText(hidden))
		}
	}
	text := bg.Foreground(ColorTextPrimary)
	for _, l := range strings.Split(body, "\n") {
		if !strings.Contains(l, "\x1b[") {
			l = text.Render(l)
		}
		lines = append(lines, l)
	}
	for i, l := range lines {
		pad := max(0, textW-lipgloss.Width(l))
		lines[i] = bg.Render("  ") + l + bg.Render(strings.Repeat(" ", pad+2))
	}
	blank := bg.Render(strings.Repeat(" ", textW+4)) // top and bottom padding
	return blank + "\n" + strings.Join(lines, "\n") + "\n" + blank
}

// turnEndLine renders a finished turn's footer as a dim rule carrying its stats.
func turnEndLine(e transcript.Entry, width int) string {
	var parts []string
	if e.Interrupted {
		parts = append(parts, lipgloss.NewStyle().Foreground(ColorError).Render("interrupted"))
	}
	if e.ModelName != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(modelColorOf(e.ModelColor)).Render(e.ModelName))
	}
	if e.Thinking > 0 {
		parts = append(parts, Icon.Thinking.Render()+" "+StyleSecondary.Render(strconv.Itoa(e.Thinking)))
	}
	if e.ToolCount > 0 {
		parts = append(parts, Icon.Tool.Ok.Render()+" "+StyleSecondary.Render(strconv.Itoa(e.ToolCount)))
	}
	if e.Usage.Output > 0 {
		parts = append(parts, Icon.Token.Render()+" "+StyleSecondary.Render(formatTokens(e.Usage.Output)))
	}
	if ctx := formatContext(e); ctx != "" {
		parts = append(parts, ctx)
	}
	if e.DurationMs > 0 {
		parts = append(parts, Icon.Clock.Render()+" "+StyleSecondary.Render(formatDuration(e.DurationMs)))
	}
	if ts := clockTime(e.Timestamp); ts != "" {
		parts = append(parts, StyleDim.Render(ts))
	}
	rule := func(n int) string { return StyleMuted.Render(strings.Repeat(GlyphHRule, max(0, n))) }
	if len(parts) == 0 {
		return rule(width)
	}
	label := strings.Join(parts, StyleDim.Render(" · "))
	return rule(2) + " " + label + " " + rule(width-lipgloss.Width(label)-4)
}

func systemRow(e transcript.Entry, expanded bool, iw int) string {
	icon, label := Icon.System, StyleSecondary.Render("System")
	if e.IsError {
		icon, label = Icon.SystemErr, lipgloss.NewStyle().Foreground(ColorError).Render("System")
	}
	head := icon.Render() + " " + label + "  " + StyleDim.Render(clockTime(e.Timestamp))
	if e.Label != "" { // preview after the timestamp (e.g. "Recap")
		head += "  " + StyleDim.Render(e.Label)
	}
	if expanded && e.Detail != "" {
		head += "\n" + indentBlock(wrapDim(strings.TrimRight(e.Detail, "\n"), iw-2), "  ")
	}
	return head
}

func (m tview) shellRow(e transcript.Entry, expanded bool, iw int) string {
	label := StylePrimaryBold.Render("Shell")
	if e.IsError {
		label = lipgloss.NewStyle().Bold(true).Foreground(ColorError).Render("Shell")
	}
	out := Icon.Shell.Render() + " " + label + "  " + StyleDim.Render(clockTime(e.Timestamp))
	cmd, hidden := e.Text, 0
	if !expanded {
		cmd, hidden = truncateLines(cmd, maxCollapsedLines)
	}
	out += "\n" + indentBlock(StyleSecondaryBold.Render("$")+" "+cmd, "  ")
	if hidden > 0 {
		out += "\n" + indentBlock(hiddenHint(hidden), "  ")
	}
	if expanded && e.Detail != "" {
		res := "Result"
		if e.IsError {
			res = "Error"
		}
		out += "\n\n" + indentBlock(sectionLabel(res, e.IsError)+"\n"+m.c.m.execCommandResultBody(e.Detail, iw-2), "  ")
	}
	return out
}

// teammateWrapWidth is the markdown wrap width of an agent message's body at a
// transcript width: the cursor gutter and the body's 2-col indent come off.
func teammateWrapWidth(width int) int { return max(width-detailGutter-2, 10) }

// teammateRow renders an agent message: a teammate's, or a subagent's report.
func (m tview) teammateRow(e transcript.Entry, s transcript.Subagent, expanded bool, iw int) string {
	head := Icon.Teammate.Render() + " " + lipgloss.NewStyle().Bold(true).Foreground(teamColor(s.Color)).Render(s.Name)
	if s.Idle {
		return head + " " + StyleSecondary.Render("is done")
	}
	if strings.TrimSpace(e.Text) == "" {
		return head
	}
	body := m.c.m.renderMD(e.Text, max(iw-2, 10))
	if !expanded {
		if t, hidden := truncateLines(body, maxCollapsedLines); hidden > 0 {
			body = t + "\n" + hiddenHint(hidden)
		}
	}
	return head + "\n" + indentBlock(body, "  ")
}

func renderCompact(e transcript.Entry, width int) string {
	text := e.Summary
	if text == "" {
		text = "Context compressed"
	}
	tw := lipgloss.Width(text) + 2
	leftPad := max(0, (width-tw)/2)
	rightPad := max(0, width-leftPad-tw)
	return StyleMuted.Render(strings.Repeat(GlyphHRule, leftPad) + " " + text + " " + strings.Repeat(GlyphHRule, rightPad))
}

func itemRow(it transcript.Entry) string {
	if it.Kind == transcript.EntrySubagent {
		var name string
		if isAgentRefTool(it.ToolName) {
			name = agentToolLabel(it)
		} else {
			s, _ := soleSubagent(it)
			name = spawnAgentLabel(s.Type, s.Name)
		}
		return Icon.Subagent.Render() + " " + StylePrimaryBold.Render(name)
	}
	row := toolIcon(it.ToolName, it.ResultIsError).Render() + " " + StylePrimaryBold.Render(fmt.Sprintf("%-12s", toolDisplayName(it.ToolName)))
	if it.InputPreview == "" {
		return row
	}
	return row + " " + StyleSecondary.Render(truncate(it.InputPreview, 60))
}

// -- Layout, view, scrolling --------------------------------------------------

// rowKey holds the inputs besides the entry itself that a rendered block
// depends on. Entry content changes drop the cached block instead (setEntries,
// applyEntryDelta). A control row's counts are its content.
type rowKey struct {
	width                       int
	selected, focused, expanded bool
	thinking, tools             int
}

type rowEntry struct {
	key   rowKey
	lines []string
}

// layoutEntries separates rows with a blank line, as in the agent TUIs.
func (m tview) layoutEntries() (lines []string, first []int) {
	bodyW, containerW := m.c.m.bodyWidth(), m.c.m.containerWidth()
	focused := m.c.m.historyFocused()
	rows := m.displayRows()
	first = make([]int, len(rows))
	for i, dr := range rows {
		if i > 0 {
			lines = append(lines, "")
		}
		first[i] = len(lines)
		selected := i == m.transcript.cursor
		key := rowKey{width: bodyW, selected: selected, focused: focused}
		if dr.isControl() {
			key.thinking, key.tools = dr.run.thinking, dr.run.tools
		} else {
			key.expanded = m.entryExpanded(m.transcript.entries[dr.entry])
		}
		id := rowID(m.transcript.entries, dr)
		r, ok := m.transcript.rows[id]
		if !ok || r.key != key {
			block := centerBlock(m.renderRow(dr, selected), containerW, bodyW)
			r = rowEntry{key: key, lines: strings.Split(block, "\n")}
			m.transcript.rows[id] = r
		}
		lines = append(lines, r.lines...)
	}
	return lines, first
}

// viewportHeight is the layout's history height for a live transcript so
// scroll math matches what the frame draws. NOTE: sessionLayout must not call
// this (recursion).
func (m tview) viewportHeight() int {
	if m.live {
		h, _ := m.c.m.sessionLayout()
		return h
	}
	return max(1, m.c.m.bodyHeight()-5)
}

func (m tview) ensureEntryVisible() {
	lines, first := m.layoutEntries()
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(first) {
		return
	}
	start, end := itemSpan(m.transcript.cursor, first, len(lines))
	m.transcript.scroll = windowScroll(len(lines), start, end, m.viewportHeight(), m.transcript.scroll)
}

// cursorVisible reports whether the selected entry overlaps the current viewport.
func (m tview) cursorVisible() bool {
	lines, first := m.layoutEntries()
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(first) {
		return false
	}
	start, end := itemSpan(m.transcript.cursor, first, len(lines))
	return start < m.transcript.scroll+m.viewportHeight() && end > m.transcript.scroll
}

// keepCursorVisible moves the cursor one entry at a time toward the viewport,
// stopping at the first entry not wholly outside it.
func (m tview) keepCursorVisible() {
	lines, first := m.layoutEntries()
	c := &m.transcript.cursor
	if *c < 0 || *c >= len(first) {
		return
	}
	top, bottom := m.transcript.scroll, m.transcript.scroll+m.viewportHeight()
	for *c < len(first)-1 {
		if _, end := itemSpan(*c, first, len(lines)); end > top {
			break
		}
		*c++
	}
	for *c > 0 && first[*c] >= bottom {
		*c--
	}
}

// entryAtLine returns the index of the entry whose span contains the given line
// (the fallback when a single entry is taller than the viewport).
func (m tview) entryAtLine(line int) int {
	_, first := m.layoutEntries()
	idx := 0
	for i, s := range first {
		if s <= line {
			idx = i
		}
	}
	return idx
}

// firstVisibleEntry/lastVisibleEntry return the first/last entry starting within
// the viewport, falling back to entryAtLine(scroll) when a tall entry fills it.
func (m tview) firstVisibleEntry() int {
	_, first := m.layoutEntries()
	h := m.viewportHeight()
	for i, s := range first {
		if s >= m.transcript.scroll && s < m.transcript.scroll+h {
			return i
		}
	}
	return m.entryAtLine(m.transcript.scroll)
}

func (m tview) lastVisibleEntry() int {
	_, first := m.layoutEntries()
	h := m.viewportHeight()
	last := -1
	for i, s := range first {
		if s >= m.transcript.scroll && s < m.transcript.scroll+h {
			last = i
		}
	}
	if last < 0 {
		return m.entryAtLine(m.transcript.scroll)
	}
	return last
}

// clampScrollNow clamps the line scroll to the current layout's valid range.
func (m tview) clampScrollNow() {
	m.transcript.scroll = max(0, min(m.transcript.scroll, m.maxScroll()))
}

// maxScroll returns the largest valid top-line offset for the current layout.
func (m tview) maxScroll() int {
	lines, _ := m.layoutEntries()
	return max(0, len(lines)-m.viewportHeight())
}

func (m tview) clampCursor() {
	if n := len(m.displayRows()); m.transcript.cursor >= n {
		m.transcript.cursor = max(0, n-1)
	}
	if m.transcript.cursor < 0 {
		m.transcript.cursor = 0
	}
}

// restoreEntryCursor re-resolves the cursor to the same row identity (see
// rowID) after a refresh without moving the viewport. When follow is true the
// view pins to the bottom so a live session keeps tailing, and a cursor that was
// on the last row (wasLast) moves to the new last row.
func (m tview) restoreEntryCursor(ref rowRef, follow, wasLast bool) {
	m.transcript.cursor = restoreRowCursor(m.transcript.entries, m.displayRows(), ref, 0, follow && wasLast)
	maxScroll := m.maxScroll()
	if follow {
		m.transcript.scroll = maxScroll
	}
	m.transcript.scroll = max(0, min(m.transcript.scroll, maxScroll))
}

// transcriptBody renders the transcript pane.
func (m tview) transcriptBody() string {
	var b strings.Builder

	if m.transcript.err != nil {
		b.WriteString(dimStyle.Render("transcript unavailable: " + m.transcript.err.Error()))
		return b.String()
	}
	if len(m.transcript.entries) == 0 {
		b.WriteString(dimStyle.Render("(no transcript yet)"))
		return b.String()
	}

	lines, first := m.layoutEntries()
	h := m.viewportHeight()
	scroll := m.transcript.scroll
	if maxScroll := max(0, len(lines)-h); scroll > maxScroll {
		scroll = maxScroll
	}
	end := min(len(lines), scroll+h)
	hitStarts(m.c, len(first), func(i int) (int, int) { return itemSpan(i, first, len(lines)) }, scroll, end)
	m.hitFoldMarkers(lines, m.displayRows(), first, scroll, end)
	b.WriteString(strings.Join(lines[scroll:end], "\n"))
	return b.String()
}

// hitFoldMarkers covers the leading icon of each visible expandable entry (the
// entry's fold marker) and the arrow of each control row. It sits past the
// cursor gutter (and the user band's inner pad) on the row's first line.
func (m tview) hitFoldMarkers(lines []string, rows []displayRow, first []int, scroll, end int) {
	if !m.c.recording() {
		return
	}
	x0 := centerGutter(m.c.m.containerWidth(), m.c.m.bodyWidth()) + detailGutter
	for i, top := range first {
		if top < scroll || top >= end {
			continue
		}
		if rows[i].isControl() {
			m.c.hitZone(uv.Rect(x0, top-scroll, 1, 1), hitTarget{kind: hitFold, index: i})
			continue
		}
		e := m.transcript.entries[rows[i].entry]
		if !m.entryExpandable(e) {
			continue
		}
		x, y := x0, top
		if e.Kind == transcript.EntryUser {
			x += 2 // userBand's inner pad
			y++    // userBand's top padding line
			if y >= end {
				continue
			}
		}
		w := 1
		if e.Kind == transcript.EntryUser {
			w = lipgloss.Width(Icon.User.Glyph) + len(" You") // the whole "<icon> You" label
		} else if cell := []rune(xansi.Cut(xansi.Strip(lines[y]), x, x+2)); len(cell) > 0 {
			w = max(1, xansi.StringWidth(string(cell[0])))
		}
		m.c.hitZone(uv.Rect(x, y-scroll, w, 1), hitTarget{kind: hitFold, index: i})
	}
}

// -- Edit diff rendering (used by the detail drill-down view) ------------------

// editDiff renders the input of an edit-like tool (Edit/MultiEdit/Write/
// NotebookEdit) as a colored diff. Returns ok=false for any other tool.
func editDiff(name, input string) (string, bool) {
	switch name {
	case "Edit", "MultiEdit", "Write", "NotebookEdit", // claude
		"edit", "write": // opencode
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
	// OpenCode uses "path"/"filePath"; the field name varies by agent model.
	if path := str(in["file_path"], in["notebook_path"], in["path"], in["filePath"]); path != "" {
		sb.WriteString(dimStyle.Render("● "+path) + "\n")
	}

	switch name {
	case "Edit", "edit":
		oldS, newS := str(in["old_string"], in["oldString"]), str(in["new_string"], in["newString"])
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
	case "Write", "write":
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
