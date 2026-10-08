package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func (m tview) handleTranscriptKey(msg tea.KeyPressMsg) tea.Cmd {
	cmd, _ := m.dispatch(msg, transcriptTable)
	return cmd
}

// transcriptTable maps transcript-region bindings to their actions (see keys.go).
var transcriptTable = []keyTableEntry{
	{transcriptKeys.PromptNext, tview.actPromptNext},
	{transcriptKeys.PromptPrev, tview.actPromptPrev},
	{transcriptKeys.ScrollDown, tview.actScrollDown},
	{transcriptKeys.ScrollUp, tview.actScrollUp},
	{transcriptKeys.HalfDown, tview.actHalfDown},
	{transcriptKeys.HalfUp, tview.actHalfUp},
	{transcriptKeys.Top, tview.actTop},
	{transcriptKeys.Bottom, tview.actBottom},
	{transcriptKeys.Collapse, tview.actCollapse},
	{transcriptKeys.Expand, tview.actExpand},
	{transcriptKeys.Detail, tview.actDrill},
}

func (m tview) isPromptRow(r displayRow) bool {
	return r.kind == entryRow && m.transcript.entries[r.entry].Kind == transcript.EntryUser
}

func (m tview) actPromptNext(tea.KeyPressMsg) tea.Cmd {
	rows := m.displayRows()
	for i := m.transcript.cursor + 1; i < len(rows); i++ {
		if m.isPromptRow(rows[i]) {
			m.transcript.cursor = i
			m.ensureEntryVisible()
			break
		}
	}
	return nil
}

func (m tview) actPromptPrev(tea.KeyPressMsg) tea.Cmd {
	rows := m.displayRows()
	for i := min(m.transcript.cursor, len(rows)) - 1; i >= 0; i-- {
		if m.isPromptRow(rows[i]) {
			m.transcript.cursor = i
			m.ensureEntryVisible()
			break
		}
	}
	return nil
}

// actScrollDown scrolls while the selected card runs past the viewport bottom;
// once its bottom is in view it selects the next card.
func (m tview) actScrollDown(tea.KeyPressMsg) tea.Cmd {
	if !m.cursorVisible() {
		m.keepCursorVisible()
		return nil
	}
	lines, first := m.layoutEntries()
	h := m.viewportHeight()
	if _, end := m.entrySpan(m.transcript.cursor, first, len(lines)); end > m.transcript.scroll+h {
		m.transcript.scroll += 3
		m.clampScrollNow()
		return nil
	}
	if m.transcript.cursor < len(first)-1 {
		m.transcript.cursor++
		if start, end := m.entrySpan(m.transcript.cursor, first, len(lines)); start >= m.transcript.scroll+h {
			m.transcript.scroll = min(end, start+3) - h
			m.clampScrollNow()
		}
	}
	return nil
}

func (m tview) actScrollUp(tea.KeyPressMsg) tea.Cmd {
	if !m.cursorVisible() {
		m.keepCursorVisible()
		return nil
	}
	lines, first := m.layoutEntries()
	if start, _ := m.entrySpan(m.transcript.cursor, first, len(lines)); start < m.transcript.scroll {
		m.transcript.scroll -= 3
		m.clampScrollNow()
		return nil
	}
	if m.transcript.cursor > 0 {
		m.transcript.cursor--
		if start, end := m.entrySpan(m.transcript.cursor, first, len(lines)); end <= m.transcript.scroll {
			m.transcript.scroll = max(start, end-3)
			m.clampScrollNow()
		}
	}
	return nil
}

func (m tview) actHalfDown(tea.KeyPressMsg) tea.Cmd {
	m.transcript.scroll += max(1, m.viewportHeight()/2)
	m.clampScrollNow()
	return nil
}

func (m tview) actHalfUp(tea.KeyPressMsg) tea.Cmd {
	m.transcript.scroll -= max(1, m.viewportHeight()/2)
	m.clampScrollNow()
	return nil
}

func (m tview) actTop(tea.KeyPressMsg) tea.Cmd {
	m.transcript.cursor, m.transcript.scroll = 0, 0
	return nil
}

func (m tview) actBottom(tea.KeyPressMsg) tea.Cmd {
	m.transcript.cursor = max(0, len(m.displayRows())-1)
	m.transcript.scroll = m.maxScroll()
	return nil
}

func (m tview) actCollapse(tea.KeyPressMsg) tea.Cmd {
	r, ok := m.cursorRow()
	switch {
	case !ok || r.kind == runSummary:
		return nil
	case r.isControl():
		return m.toggleRunRow(r)
	}
	m.setExpanded(r.entry, false)
	m.ensureEntryVisible()
	return nil
}

func (m tview) actExpand(tea.KeyPressMsg) tea.Cmd {
	r, ok := m.cursorRow()
	if !ok {
		return nil
	}
	if r.isControl() {
		if r.kind != runSummary {
			return nil
		}
		return m.toggleRunRow(r)
	}
	m.setExpanded(r.entry, true)
	m.ensureEntryVisible()
	return m.fetchIfExpandedTool(r.entry)
}

// fetchIfExpandedTool relies on fetchToolBodyCmd to dedupe repeat requests.
func (m tview) fetchIfExpandedTool(i int) tea.Cmd {
	if i < 0 || i >= len(m.transcript.entries) {
		return nil
	}
	e := m.transcript.entries[i]
	if !e.IsToolCall() || !m.entryExpanded(e) {
		return nil
	}
	return m.fetchToolBodyCmd(e, "")
}

func (m tview) actDrill(tea.KeyPressMsg) tea.Cmd {
	r, ok := m.cursorRow()
	if !ok {
		return nil
	}
	if r.isControl() {
		return m.toggleRunRow(r)
	}
	if !m.c.m.detailable(m.transcript.entries[r.entry]) {
		return nil
	}
	m.historyView = histDetail
	return m.enterDetail()
}

func (m tview) clickEntry(i int, focused bool) tea.Cmd {
	rows := m.displayRows()
	if i < 0 || i >= len(rows) {
		return nil
	}
	if rows[i].isControl() {
		m.transcript.cursor = i
		return m.toggleRunRow(rows[i])
	}
	if focused && i == m.transcript.cursor {
		return m.actDrill(tea.KeyPressMsg{})
	}
	m.selectEntry(i)
	return nil
}

// clickFold selects row i and toggles its expansion (a click on its fold marker).
func (m tview) clickFold(i int) tea.Cmd {
	rows := m.displayRows()
	if i < 0 || i >= len(rows) {
		return nil
	}
	m.transcript.cursor = i
	if rows[i].isControl() {
		return m.toggleRunRow(rows[i])
	}
	e := rows[i].entry
	m.setExpanded(e, !m.entryExpanded(m.transcript.entries[e]))
	m.ensureEntryVisible()
	return m.fetchIfExpandedTool(e)
}

func (m tview) selectEntry(i int) {
	if i >= 0 && i < len(m.displayRows()) {
		m.transcript.cursor = i
	}
}

func (m tview) wheelLines(d int) {
	m.transcript.scroll += d
	m.clampScrollNow()
	m.keepCursorVisible()
}
