package tui

import (
	"math"
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// entrySource is the text a card copies: what the agent or the user wrote,
// not how the card draws it. ok is false for cards with nothing to copy;
// loading is true when a tool's full body is not fetched yet.
func (m tview) entrySource(e transcript.Entry) (src string, ok, loading bool) {
	src, ok, loading, _ = m.sourceOf(e)
	return src, ok, loading
}

// sourceOf is entrySource plus unavailable: the tool body was fetched but came
// back empty (a failed fetch), so src is the loaded preview instead.
func (m tview) sourceOf(e transcript.Entry) (src string, ok, loading, unavailable bool) {
	join := func(parts ...string) string {
		var out []string
		for _, p := range parts {
			if p = strings.TrimRight(p, "\n"); p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, "\n\n")
	}
	switch e.Kind {
	case transcript.EntryTurnEnd, transcript.EntryCompact:
		return "", false, false, false
	case transcript.EntryUser, transcript.EntryText, transcript.EntryThinking, transcript.EntrySkill:
		return e.Text, e.Text != "", false, false
	case transcript.EntryShell:
		s := join(e.Text, e.Detail)
		return s, s != "", false, false
	case transcript.EntrySystem:
		s := join(e.Label, e.Detail)
		return s, s != "", false, false
	}
	if e.IsTeammate() {
		return e.Text, e.Text != "", false, false
	}
	full, fetched := m.filledTool(e)
	s := join(full.ToolInput, full.Result)
	if fetched && s == "" && e.ToolID != "" {
		s = join(e.ToolInput, e.Result)
		return s, s != "", false, s != ""
	}
	return s, s != "", !fetched, false
}

func (m tview) hasSource(e transcript.Entry) bool {
	_, ok, _ := m.entrySource(e)
	return ok
}

// copyTarget is the card a card copy takes: the selected card, the selected
// detail item, or the entry a body frame renders. ok is false on a control row
// or with nothing selected.
func (m tview) copyTarget() (e transcript.Entry, agentID string, ok bool) {
	if m.historyView == histDetail {
		f := m.topFrame()
		switch {
		case f == nil:
			return transcript.Entry{}, "", false
		case f.items == nil:
			return f.entry, "", true
		}
		r, ok := m.frameCursorRow(f)
		if !ok || r.isControl() {
			return transcript.Entry{}, "", false
		}
		return f.items[r.entry], f.agentID, true
	}
	r, ok := m.cursorRow()
	if !ok || r.isControl() {
		return transcript.Entry{}, "", false
	}
	return m.transcript.entries[r.entry], "", true
}

// copyEntry copies a card's source. A tool whose body is still loading copies
// what is here and fetches the rest.
func (m tview) copyEntry(e transcript.Entry, agentID string) tea.Cmd {
	src, _, loading, unavailable := m.sourceOf(e)
	switch {
	case src != "" && loading:
		return tea.Batch(copyText(m.c, src, "copied (output still loading)"), m.fetchToolBodyCmd(e, agentID))
	case unavailable:
		return copyText(m.c, src, "copied (output unavailable)")
	}
	return copyText(m.c, src, "copied")
}

func (m tview) actCopyCard(tea.KeyPressMsg) tea.Cmd {
	e, agentID, ok := m.copyTarget()
	if !ok {
		m.c.setFlash("nothing to copy")
		return nil
	}
	return m.copyEntry(e, agentID)
}

// textInset is the cells between the cursor gutter and a card's text: the
// icon and its space, or the prompt band's pad.
const textInset = 2

// visualTarget is the pane surface on screen and the state visual mode edits.
type visualTarget struct {
	s      textSurface
	sel    **selection
	scroll *int
	cursor *int                 // the card cursor, which follows the visual cursor
	source func(row int) string // a row's source, for rejoining wraps
}

func rowIDs(es []transcript.Entry, rows []displayRow) []rowRef {
	ids := make([]rowRef, len(rows))
	for i, r := range rows {
		ids[i] = rowID(es, r)
	}
	return ids
}

// bodyRow is a body frame's one row.
var bodyRow = []rowRef{{id: "body"}}

func (m tview) streamSurface() textSurface {
	lines, first := m.layoutEntries()
	ids := rowIDs(m.transcript.entries, m.displayRows())
	h := m.viewportHeight()
	scroll := min(m.transcript.scroll, max(0, len(lines)-h)) // as transcriptBody draws it
	return newSurface(lines, first, ids, m.c.m.transcriptTextX()+textInset, m.c.m.transcriptWidth()-detailGutter-textInset, scroll, h)
}

// detailSurface is the top frame's lines. A body frame is one row with no
// cursor gutter; its text starts right after the indent.
func (m tview) detailSurface(f *detailFrame) textSurface {
	cw := m.c.m.transcriptWidth()
	lines, first := m.frameLines(f, cw)
	_, rows := m.detailPrefix(f)
	ch, overflow := m.detailBodyHeight(rows, len(lines))
	scroll := 0
	if overflow {
		scroll = max(0, min(f.scroll, len(lines)-ch))
	}
	if f.items == nil {
		return newSurface(lines, []int{0}, bodyRow, detailGutter, cw-detailGutter, scroll, ch)
	}
	ids := rowIDs(f.items, m.frameRows(f))
	return newSurface(lines, first, ids, detailGutter+textInset, cw-detailGutter-textInset, scroll, ch)
}

// rowSource reads rows only when a copy asks for a source.
func (m tview) rowSource(es []transcript.Entry, rows func() []displayRow) func(int) string {
	rows = sync.OnceValue(rows)
	return func(i int) string {
		rs := rows()
		if i < 0 || i >= len(rs) || rs[i].isControl() {
			return ""
		}
		src, _, _ := m.entrySource(es[rs[i].entry])
		return src
	}
}

// visualTarget is false when the pane has no text to select.
func (m tview) visualTarget() (visualTarget, bool) {
	if m.historyView == histDetail {
		f := m.topFrame()
		if f == nil {
			return visualTarget{}, false
		}
		s := m.detailSurface(f)
		if len(s.lines) == 0 {
			return visualTarget{}, false
		}
		source := func(int) string {
			src, _, _ := m.entrySource(f.entry)
			return src
		}
		if f.items != nil {
			source = m.rowSource(f.items, func() []displayRow { return m.frameRows(f) })
		}
		return visualTarget{s: s, sel: &f.visual, scroll: &f.scroll, cursor: &f.cursor, source: source}, true
	}
	if m.transcript.err != nil || len(m.transcript.entries) == 0 {
		return visualTarget{}, false
	}
	s := m.streamSurface()
	if len(s.lines) == 0 {
		return visualTarget{}, false
	}
	return visualTarget{s: s, sel: &m.transcript.visual, scroll: &m.transcript.scroll,
		cursor: &m.transcript.cursor, source: m.rowSource(m.transcript.entries, m.displayRows)}, true
}

func (t transcriptComp) visualActive() bool { return t.activeSelection() != nil }

// activeSelection is the selection of the pane on screen, nil outside visual
// mode.
func (t transcriptComp) activeSelection() *selection {
	if t.historyView == histDetail {
		if n := len(t.transcript.detailStack); n > 0 {
			return t.transcript.detailStack[n-1].visual
		}
		return nil
	}
	return t.transcript.visual
}

// startVisual begins at the selected card's first text, or at the top of the
// view when that card is off screen.
func (m tview) startVisual(linewise bool) tea.Cmd {
	vt, ok := m.visualTarget()
	if !ok {
		return nil
	}
	line := vt.s.scroll
	if c := *vt.cursor; c >= 0 && c < len(vt.s.first) && vt.s.first[c] >= vt.s.scroll && vt.s.first[c] < vt.s.scroll+vt.s.height {
		line = vt.s.first[c]
	}
	a := absPos{line: line, col: vt.s.firstCol(line)}
	p := vt.s.pos(a)
	*vt.sel = &selection{anchor: p, cursor: p, want: a.col, linewise: linewise}
	return nil
}

// setVisualCursor moves the visual cursor to a, starting a charwise selection
// there unless extend, and scrolls to keep it in view.
func (m tview) setVisualCursor(vt visualTarget, a absPos, extend bool) {
	a.line = max(0, min(a.line, len(vt.s.lines)-1))
	a.col = vt.s.clampCol(a.line, a.col)
	p := vt.s.pos(a)
	if !extend || *vt.sel == nil {
		*vt.sel = &selection{anchor: p, cursor: p, want: a.col}
	} else {
		(*vt.sel).cursor, (*vt.sel).want = p, a.col
	}
	m.followVisual(vt, a)
}

// followVisual scrolls the cursor line into view and puts the card cursor on
// its row.
func (m tview) followVisual(vt visualTarget, a absPos) {
	*vt.scroll = windowScroll(len(vt.s.lines), a.line, a.line+1, vt.s.height, *vt.scroll)
	*vt.cursor = vt.s.rowOf(a.line)
}

// visualState is the active selection on its surface, with its cursor.
func (m tview) visualState() (vt visualTarget, sel *selection, cur absPos, ok bool) {
	if vt, ok = m.visualTarget(); !ok || *vt.sel == nil {
		return vt, nil, cur, false
	}
	sel = *vt.sel
	cur, ok = vt.s.abs(sel.cursor)
	return vt, sel, cur, ok
}

// visualAct is a visual-mode action on the active selection.
func visualAct(f func(m tview, vt visualTarget, sel *selection, cur absPos) tea.Cmd) keyAction {
	return func(m tview, _ tea.KeyPressMsg) tea.Cmd {
		vt, sel, cur, ok := m.visualState()
		if !ok {
			return nil
		}
		return f(m, vt, sel, cur)
	}
}

func visualMotion(mv motion) keyAction {
	return visualAct(func(m tview, vt visualTarget, sel *selection, cur absPos) tea.Cmd {
		a, want := vt.s.move(cur, sel.want, mv)
		sel.cursor, sel.want = vt.s.pos(a), want
		m.followVisual(vt, a)
		return nil
	})
}

// visualJump moves the cursor to the line that to picks, keeping the column it
// aims for.
func visualJump(to func(s textSurface, cur absPos) int) keyAction {
	return visualAct(func(m tview, vt visualTarget, sel *selection, cur absPos) tea.Cmd {
		a := absPos{line: max(0, min(to(vt.s, cur), len(vt.s.lines)-1))}
		a.col = vt.s.clampCol(a.line, sel.want)
		sel.cursor = vt.s.pos(a)
		m.followVisual(vt, a)
		return nil
	})
}

var visualTable = []keyTableEntry{
	{visualKeys.Left, visualMotion(mLeft)},
	{visualKeys.Right, visualMotion(mRight)},
	{visualKeys.Up, visualMotion(mUp)},
	{visualKeys.Down, visualMotion(mDown)},
	{visualKeys.WordNext, visualMotion(mWordNext)},
	{visualKeys.WordPrev, visualMotion(mWordPrev)},
	{visualKeys.WordEnd, visualMotion(mWordEnd)},
	{visualKeys.LineStart, visualMotion(mLineStart)},
	{visualKeys.FirstNonBlank, visualMotion(mFirstNonBlank)},
	{visualKeys.LineEnd, visualMotion(mLineEnd)},
	{visualKeys.Top, visualJump(func(textSurface, absPos) int { return 0 })},
	{visualKeys.Bottom, visualJump(func(s textSurface, _ absPos) int { return len(s.lines) - 1 })},
	{visualKeys.HalfDown, visualJump(func(s textSurface, c absPos) int { return c.line + max(1, s.height/2) })},
	{visualKeys.HalfUp, visualJump(func(s textSurface, c absPos) int { return c.line - max(1, s.height/2) })},
	{visualKeys.PageDown, visualJump(func(s textSurface, c absPos) int { return c.line + s.height })},
	{visualKeys.PageUp, visualJump(func(s textSurface, c absPos) int { return c.line - s.height })},
	{visualKeys.Swap, visualAct(func(m tview, vt visualTarget, sel *selection, _ absPos) tea.Cmd {
		sel.anchor, sel.cursor = sel.cursor, sel.anchor
		if a, ok := vt.s.abs(sel.cursor); ok {
			sel.want = a.col
			m.followVisual(vt, a)
		}
		return nil
	})},
	// Pressing the current mode's key exits; the other key switches mode.
	{visualKeys.Char, visualAct(func(_ tview, vt visualTarget, sel *selection, _ absPos) tea.Cmd {
		if sel.linewise {
			sel.linewise = false
		} else {
			*vt.sel = nil
		}
		return nil
	})},
	{visualKeys.Line, visualAct(func(_ tview, vt visualTarget, sel *selection, _ absPos) tea.Cmd {
		if sel.linewise {
			*vt.sel = nil
		} else {
			sel.linewise = true
		}
		return nil
	})},
	{visualKeys.Copy, visualAct(func(m tview, vt visualTarget, sel *selection, _ absPos) tea.Cmd {
		text := vt.s.selectedText(sel, vt.source)
		*vt.sel = nil
		return copyText(m.c, text, "copied")
	})},
	{visualKeys.Back, func(m tview, _ tea.KeyPressMsg) tea.Cmd {
		m.clearVisual()
		return nil
	}},
}

// visualKey takes every key while visual mode is on, so a normal-mode key
// cannot act under the selection.
func (m tview) visualKey(msg tea.KeyPressMsg) tea.Cmd {
	m.c.setFlash("")
	m.dropStaleVisual()
	cmd, _ := m.dispatch(msg, visualTable)
	return cmd
}

// visualRows is the identity of the active pane's rows; ok is false when the
// pane has no text to select.
func (m tview) visualRows() (ids []rowRef, ok bool) {
	if m.historyView == histDetail {
		f := m.topFrame()
		switch {
		case f == nil:
			return nil, false
		case f.items == nil:
			return bodyRow, true
		}
		return rowIDs(f.items, m.frameRows(f)), true
	}
	if m.transcript.err != nil {
		return nil, false
	}
	return rowIDs(m.transcript.entries, m.displayRows()), true
}

// dropStaleVisual ends visual mode when a selection end's row is gone.
func (m tview) dropStaleVisual() {
	sel := m.activeSelection()
	if sel == nil {
		return
	}
	ids, ok := m.visualRows()
	if !ok || !slices.Contains(ids, sel.anchor.row) || !slices.Contains(ids, sel.cursor.row) {
		m.clearVisual()
	}
}

// hitDetailText records the detail body's text, which sits under the prefix
// and is centered after it is drawn.
func (m tview) hitDetailText(rows, scroll, end int) {
	m.c.below(rows).hitText(0, centerGutter(m.c.m.containerWidth(), m.c.m.bodyWidth()), scroll, end)
}

// textSelector is a component whose text the mouse selects. line and col are
// buffer coordinates from textAt.
type textSelector interface {
	selectText(c *ctx, line, col int, extend bool) component
	visualActive() bool
	endVisual(c *ctx) component
}

// selectText takes a point past the last line as the end of the text, and one
// above the first as its start.
func (t transcriptComp) selectText(c *ctx, line, col int, extend bool) component {
	v := t.bind(c)
	vt, ok := v.visualTarget()
	if !ok {
		return t
	}
	a := absPos{line: line, col: col - vt.s.x0}
	switch n := len(vt.s.lines); {
	case line >= n:
		a = absPos{line: n - 1, col: math.MaxInt}
	case line < 0:
		a = absPos{}
	}
	v.setVisualCursor(vt, a, extend)
	return t
}

func (t transcriptComp) endVisual(c *ctx) component {
	t.bind(c).clearVisual()
	return t
}

func (m tview) clearVisual() {
	m.transcript.visual = nil
	if f := m.topFrame(); f != nil {
		f.visual = nil
	}
}

// visualModeLine is the footer in visual mode.
func (m tview) visualModeLine() string {
	sel := m.activeSelection()
	if sel == nil {
		return ""
	}
	mode := "-- VISUAL --"
	if sel.linewise {
		mode = "-- VISUAL LINE --"
	}
	return StyleAccentBold.Render(mode) + "  " + m.c.m.footer(visualKeys.Left, visualKeys.Swap, visualKeys.Copy, visualKeys.Back)
}

// visualCursor is the buffer line and column of the selection's cursor, for
// the terminal cursor to sit on.
func (m tview) visualCursor() (line, col int, ok bool) {
	sel := m.activeSelection()
	if sel == nil {
		return 0, 0, false
	}
	vt, ok := m.visualTarget()
	if !ok {
		return 0, 0, false
	}
	a, ok := vt.s.abs(sel.cursor)
	return a.line, vt.s.x0 + a.col, ok
}

// paintVisual draws the selection on the visible lines vis.
func (m tview) paintVisual(vis []string) []string {
	if m.activeSelection() == nil {
		return vis
	}
	vt, ok := m.visualTarget()
	if !ok || *vt.sel == nil {
		return vis
	}
	return vt.s.paint(*vt.sel, vis)
}
