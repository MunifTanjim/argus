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

func (m tview) actPromptNext(tea.KeyPressMsg) tea.Cmd {
	for i := m.transcript.cursor + 1; i < len(m.transcript.entries); i++ {
		if m.transcript.entries[i].Kind == transcript.EntryUser {
			m.transcript.cursor = i
			m.ensureEntryVisible()
			break
		}
	}
	return nil
}

func (m tview) actPromptPrev(tea.KeyPressMsg) tea.Cmd {
	for i := m.transcript.cursor - 1; i >= 0; i-- {
		if m.transcript.entries[i].Kind == transcript.EntryUser {
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
	m.transcript.cursor = max(0, len(m.transcript.entries)-1)
	m.transcript.scroll = m.maxScroll()
	return nil
}

func (m tview) actCollapse(tea.KeyPressMsg) tea.Cmd {
	m.setExpanded(m.transcript.cursor, false)
	m.ensureEntryVisible()
	return nil
}

func (m tview) actExpand(tea.KeyPressMsg) tea.Cmd {
	i := m.transcript.cursor
	m.setExpanded(i, true)
	m.ensureEntryVisible()
	return m.fetchIfExpandedTool(i)
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
	if m.transcript.cursor < 0 || m.transcript.cursor >= len(m.transcript.entries) ||
		!m.c.m.detailable(m.transcript.entries[m.transcript.cursor]) {
		return nil
	}
	m.historyView = histDetail
	return m.enterDetail()
}

func (m tview) clickEntry(i int, focused bool) tea.Cmd {
	if focused && i == m.transcript.cursor {
		return m.actDrill(tea.KeyPressMsg{})
	}
	m.selectEntry(i)
	return nil
}

// clickFold selects entry i and toggles its expansion (a click on its fold marker).
func (m tview) clickFold(i int) tea.Cmd {
	if i < 0 || i >= len(m.transcript.entries) {
		return nil
	}
	m.transcript.cursor = i
	m.setExpanded(i, !m.entryExpanded(m.transcript.entries[i]))
	m.ensureEntryVisible()
	return m.fetchIfExpandedTool(i)
}

func (m tview) selectEntry(i int) {
	if i >= 0 && i < len(m.transcript.entries) {
		m.transcript.cursor = i
	}
}

func (m tview) wheelLines(d int) {
	m.transcript.scroll += d
	m.clampScrollNow()
	m.keepCursorVisible()
}
