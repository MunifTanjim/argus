package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m tview) handleTranscriptKey(msg tea.KeyPressMsg) tea.Cmd {
	cmd, _ := m.dispatch(msg, transcriptTable)
	return cmd
}

// transcriptTable maps transcript-region bindings to their actions (see keys.go).
var transcriptTable = []keyTableEntry{
	{transcriptKeys.CardNext, tview.actCardNext},
	{transcriptKeys.CardPrev, tview.actCardPrev},
	{transcriptKeys.ScrollDown, tview.actScrollDown},
	{transcriptKeys.ScrollUp, tview.actScrollUp},
	{transcriptKeys.HalfDown, tview.actHalfDown},
	{transcriptKeys.HalfUp, tview.actHalfUp},
	{transcriptKeys.Top, tview.actTop},
	{transcriptKeys.Bottom, tview.actBottom},
	{transcriptKeys.Collapse, tview.actCollapse},
	{transcriptKeys.Expand, tview.actExpand},
	{transcriptKeys.Detail, tview.actDrillChunk},
}

func (m tview) actCardNext(tea.KeyPressMsg) tea.Cmd {
	if m.cursorVisible() {
		m.transcript.cursor++
		m.clampCursor()
		m.ensureChunkVisible()
	} else {
		m.transcript.cursor = m.firstVisibleChunk() // re-anchor; viewport stays put
		m.clampCursor()
	}
	return nil
}

func (m tview) actCardPrev(tea.KeyPressMsg) tea.Cmd {
	if m.cursorVisible() {
		m.transcript.cursor--
		m.clampCursor()
		m.ensureChunkVisible()
	} else {
		m.transcript.cursor = m.lastVisibleChunk() // re-anchor; viewport stays put
		m.clampCursor()
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
	lines, first := m.layoutChunks()
	h := m.viewportHeight()
	if _, end := chunkSpan(m.transcript.cursor, first, len(lines)); end > m.transcript.scroll+h {
		m.transcript.scroll += 3
		m.clampScrollNow()
		return nil
	}
	if m.transcript.cursor < len(first)-1 {
		m.transcript.cursor++
		if start, end := chunkSpan(m.transcript.cursor, first, len(lines)); start >= m.transcript.scroll+h {
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
	lines, first := m.layoutChunks()
	if start, _ := chunkSpan(m.transcript.cursor, first, len(lines)); start < m.transcript.scroll {
		m.transcript.scroll -= 3
		m.clampScrollNow()
		return nil
	}
	if m.transcript.cursor > 0 {
		m.transcript.cursor--
		if start, end := chunkSpan(m.transcript.cursor, first, len(lines)); end <= m.transcript.scroll {
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
	m.transcript.cursor = max(0, len(m.transcript.chunks)-1)
	m.transcript.scroll = m.maxScroll()
	return nil
}

func (m tview) actCollapse(tea.KeyPressMsg) tea.Cmd {
	m.setExpanded(m.transcript.cursor, false)
	m.ensureChunkVisible()
	return nil
}

func (m tview) actExpand(tea.KeyPressMsg) tea.Cmd {
	m.setExpanded(m.transcript.cursor, true)
	m.ensureChunkVisible()
	return nil
}

func (m tview) actDrillChunk(tea.KeyPressMsg) tea.Cmd {
	// Drill into the selected chunk's full detail sub-view.
	if m.transcript.cursor >= 0 && m.transcript.cursor < len(m.transcript.chunks) && m.c.m.detailable(m.transcript.chunks[m.transcript.cursor]) {
		m.historyView = histDetail
		m.enterDetail()
	}
	return nil
}

func (m tview) clickChunk(i int, focused bool) tea.Cmd {
	if i < 0 || i >= len(m.transcript.chunks) {
		return nil
	}
	if focused && i == m.transcript.cursor {
		return m.actDrillChunk(tea.KeyPressMsg{})
	}
	m.transcript.cursor = i
	return nil
}

func (m tview) wheelLines(d int) {
	m.transcript.scroll += d
	m.clampScrollNow()
	m.keepCursorVisible()
}
