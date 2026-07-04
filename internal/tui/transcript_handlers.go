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
	{transcriptKeys.TurnNext, model.actSmartNext},
	{transcriptKeys.TurnPrev, model.actSmartPrev},
	{transcriptKeys.CardNext, model.actTurnNext},
	{transcriptKeys.CardPrev, model.actTurnPrev},
	{transcriptKeys.ScrollDown, model.actScrollDown},
	{transcriptKeys.ScrollUp, model.actScrollUp},
	{transcriptKeys.HalfDown, model.actHalfDown},
	{transcriptKeys.HalfUp, model.actHalfUp},
	{transcriptKeys.Top, model.actTop},
	{transcriptKeys.Bottom, model.actBottom},
	{transcriptKeys.Fold, model.actFold},
	{transcriptKeys.Detail, model.actDrillChunk},
	{transcriptKeys.ExpandAll, model.actExpandAll},
	{transcriptKeys.CollapseAll, model.actCollapseAll},
}

// actSmartNext (j) selects the next turn, first scrolling through an overflowing
// selected card so nothing below the fold is skipped unseen.
func (m model) actSmartNext(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.cursorVisible() {
		m.transcript.cursor = m.firstVisibleChunk() // re-anchor; viewport stays put
		m.clampCursor()
		return m, nil
	}
	if _, end, h, overflow := m.selectedChunkOverflow(); overflow && m.transcript.scroll < end-h {
		m.transcript.scroll += 3
		m.clampScrollNow()
		return m, nil
	}
	// At the last card, once it's fully scrolled, stay put: there's no card to
	// advance to, and ensureChunkVisible would snap back to the card's top.
	if m.transcript.cursor < len(m.transcript.chunks)-1 {
		m.transcript.cursor++
		m.ensureChunkVisible()
	}
	return m, nil
}

// actSmartPrev (k) is the mirror of actSmartNext: scroll up through an oversized
// selected card before moving the selection to the previous turn.
func (m model) actSmartPrev(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.cursorVisible() {
		m.transcript.cursor = m.lastVisibleChunk() // re-anchor; viewport stays put
		m.clampCursor()
		return m, nil
	}
	if start, _, _, overflow := m.selectedChunkOverflow(); overflow && m.transcript.scroll > start {
		m.transcript.scroll -= 3
		m.clampScrollNow()
		return m, nil
	}
	if m.transcript.cursor > 0 {
		m.transcript.cursor--
		m.ensureChunkVisible()
	}
	return m, nil
}

func (m model) actTurnNext(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m model) actTurnPrev(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m model) actScrollDown(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.transcript.scroll += 3
	m.clampScrollNow()
	return m, nil
}

func (m model) actScrollUp(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.transcript.scroll -= 3
	m.clampScrollNow()
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

func (m model) actFold(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.toggleExpand(m.transcript.cursor)
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

func (m model) actExpandAll(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.setAllExpanded(true)
	m.ensureChunkVisible()
	return m, nil
}

func (m model) actCollapseAll(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.setAllExpanded(false)
	m.ensureChunkVisible()
	return m, nil
}
