package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m model) handleTranscriptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if mm, cmd, ok := m.dispatch(msg, transcriptTable); ok {
		return mm, cmd
	}
	return m, nil
}

// transcriptTable maps transcript-region bindings to their actions (see keys.go).
var transcriptTable = []keyTableEntry{
	{transcriptKeys.CardNext, model.actCardNext},
	{transcriptKeys.CardPrev, model.actCardPrev},
	{transcriptKeys.ScrollDown, model.actScrollDown},
	{transcriptKeys.ScrollUp, model.actScrollUp},
	{transcriptKeys.HalfDown, model.actHalfDown},
	{transcriptKeys.HalfUp, model.actHalfUp},
	{transcriptKeys.Top, model.actTop},
	{transcriptKeys.Bottom, model.actBottom},
	{transcriptKeys.Collapse, model.actCollapse},
	{transcriptKeys.Expand, model.actExpand},
	{transcriptKeys.Detail, model.actDrillChunk},
}

func (m model) actCardNext(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.cursorVisible() {
		m.transcript.cursor++
		m.clampCursor()
		m.ensureChunkVisible()
	} else {
		m.transcript.cursor = m.firstVisibleChunk() // re-anchor; viewport stays put
		m.clampCursor()
	}
	return m, nil
}

func (m model) actCardPrev(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.cursorVisible() {
		m.transcript.cursor--
		m.clampCursor()
		m.ensureChunkVisible()
	} else {
		m.transcript.cursor = m.lastVisibleChunk() // re-anchor; viewport stays put
		m.clampCursor()
	}
	return m, nil
}

// actScrollDown scrolls while the selected card runs past the viewport bottom;
// once its bottom is in view it selects the next card.
func (m model) actScrollDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.cursorVisible() {
		m.keepCursorVisible()
		return m, nil
	}
	lines, first := m.layoutChunks()
	h := m.viewportHeight()
	if _, end := chunkSpan(m.transcript.cursor, first, len(lines)); end > m.transcript.scroll+h {
		m.transcript.scroll += 3
		m.clampScrollNow()
		return m, nil
	}
	if m.transcript.cursor < len(first)-1 {
		m.transcript.cursor++
		if start, end := chunkSpan(m.transcript.cursor, first, len(lines)); start >= m.transcript.scroll+h {
			m.transcript.scroll = min(end, start+3) - h
			m.clampScrollNow()
		}
	}
	return m, nil
}

func (m model) actScrollUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.cursorVisible() {
		m.keepCursorVisible()
		return m, nil
	}
	lines, first := m.layoutChunks()
	if start, _ := chunkSpan(m.transcript.cursor, first, len(lines)); start < m.transcript.scroll {
		m.transcript.scroll -= 3
		m.clampScrollNow()
		return m, nil
	}
	if m.transcript.cursor > 0 {
		m.transcript.cursor--
		if start, end := chunkSpan(m.transcript.cursor, first, len(lines)); end <= m.transcript.scroll {
			m.transcript.scroll = max(start, end-3)
			m.clampScrollNow()
		}
	}
	return m, nil
}

func (m model) actHalfDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.transcript.scroll += max(1, m.viewportHeight()/2)
	m.clampScrollNow()
	return m, nil
}

func (m model) actHalfUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.transcript.scroll -= max(1, m.viewportHeight()/2)
	m.clampScrollNow()
	return m, nil
}

func (m model) actTop(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.transcript.cursor, m.transcript.scroll = 0, 0
	return m, nil
}

func (m model) actBottom(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.transcript.cursor = max(0, len(m.transcript.chunks)-1)
	m.transcript.scroll = m.maxScroll()
	return m, nil
}

func (m model) actCollapse(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.setExpanded(m.transcript.cursor, false)
	m.ensureChunkVisible()
	return m, nil
}

func (m model) actExpand(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.setExpanded(m.transcript.cursor, true)
	m.ensureChunkVisible()
	return m, nil
}

func (m model) actDrillChunk(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Drill into the selected chunk's full detail sub-view.
	if m.transcript.cursor >= 0 && m.transcript.cursor < len(m.transcript.chunks) && m.detailable(m.transcript.chunks[m.transcript.cursor]) {
		m.historyView = histDetail
		m.enterDetail()
	}
	return m, nil
}
