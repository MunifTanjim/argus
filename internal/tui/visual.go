package tui

import (
	"strings"

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
