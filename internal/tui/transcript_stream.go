package tui

import (
	"crypto/rand"
	"encoding/hex"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// resubscribeOnClear re-subscribes when a /clear changes the open session's
// AgentSessionID, so pre-clear entries don't survive into the new transcript.
func (m *model) resubscribeOnClear(prev session.Session, existed bool, cur session.Session) tea.Cmd {
	t, ok := m.baseComp().(transcriptComp)
	if !ok || !t.live || cur.ID != t.sessionID {
		return nil
	}
	if t.activeSub.subID == "" || t.activeSub.agentID != "" {
		return nil
	}
	if !existed || cur.AgentSessionID == "" || prev.AgentSessionID == cur.AgentSessionID {
		return nil
	}
	old := t.activeSub.subID
	delete(m.transcriptCache, t.activeSub.key()) // superseded transcript; free its entries
	ref := subRef{subID: newSubID(), sessionID: t.sessionID, cacheKey: m.cacheKeyFor(t.sessionID)}
	bind := m.editTranscript(m.baseTop()-1, func(v tview) tea.Cmd {
		v.transcript.err = nil // drop any stale pre-clear error
		// Entry ids are positional, so pre-clear expansion would land on
		// unrelated entries of the new transcript.
		clear(v.transcript.expanded)
		clear(v.transcript.runs)
		return v.bindStream(ref)
	})
	return tea.Batch(m.unsubscribeCmd(old), bind)
}

// bindStream shows ref's cached entries at once (empty for a fresh key) and pins
// the view to the bottom so the catch-up delta keeps tailing (see
// restoreEntryCursor).
func (m tview) bindStream(ref subRef) tea.Cmd {
	m.activeSub = ref
	m.setEntries(m.c.m.transcriptCache[ref.key()].entries)
	m.transcript.cursor = max(0, len(m.displayRows())-1)
	m.transcript.scroll = m.maxScroll()
	return m.c.m.subscribeCmd(ref, len(m.transcript.entries))
}

func (m model) cacheKeyFor(sessionID string) string {
	if s, ok := m.sessions[sessionID]; ok && s.AgentSessionID != "" {
		return s.AgentSessionID
	}
	return sessionID
}

// newSubID returns a globally-unique subscription id (the gateway keys on it).
func newSubID() string { return randID() }

// newTermID returns a globally-unique terminal-attach id (the gateway/node key on it).
func newTermID() string { return randID() }

func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func applyDelta(entries []transcript.Entry, d api.TranscriptDelta) []transcript.Entry {
	from := d.FromIndex
	if from > len(entries) {
		from = len(entries)
	}
	out := make([]transcript.Entry, 0, from+len(d.Entries))
	out = append(out, entries[:from]...)
	out = append(out, d.Entries...)
	return out
}

func (m tview) setEntries(entries []transcript.Entry) {
	m.transcript.entries = entries
	clear(m.transcript.rows)
}

func (m tview) applyEntryDelta(d api.TranscriptDelta) tea.Cmd {
	for _, e := range m.transcript.entries[min(d.FromIndex, len(m.transcript.entries)):] {
		delete(m.transcript.rows, rowRef{id: e.ID})
	}
	m.transcript.entries = applyDelta(m.transcript.entries, d)
	return m.refetchStaleToolBodies(d.Entries, "")
}

// refetchStaleToolBodies drops the cached body of each resent tool call that was
// fetched while still running (done, empty, not an error), so its result is
// picked up, and re-fetches the ones on screen: expanded in the main stream, or
// shown in a detail frame of the same trace (agentID, "" = main transcript).
func (m tview) refetchStaleToolBodies(resent []transcript.Entry, agentID string) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range resent {
		if !e.IsToolCall() || e.ToolID == "" {
			continue
		}
		b, ok := m.toolBodies[e.ToolID]
		if !ok || !b.done || b.result != "" || b.resultIsError {
			continue
		}
		delete(m.toolBodies, e.ToolID)
		if m.toolOnScreen(e, agentID) {
			cmds = append(cmds, m.fetchToolBodyCmd(e, agentID))
		}
	}
	return tea.Batch(cmds...)
}

func (m tview) toolOnScreen(e transcript.Entry, agentID string) bool {
	if agentID == "" && m.entryExpanded(e) {
		return true
	}
	for i := range m.transcript.detailStack {
		f := &m.transcript.detailStack[i]
		if f.agentID != agentID {
			continue
		}
		for j, it := range f.items {
			if it.ToolID == e.ToolID && (f.focused || f.isExpanded(j)) {
				return true
			}
		}
	}
	return false
}

// subscribeCmd opens a subscription and delivers the catch-up as a transcriptDeltaMsg.
func (m model) subscribeCmd(ref subRef, haveEntries int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var d api.TranscriptDelta
		err := client.Call(api.MethodTranscriptSubscribe, api.TranscriptSubscribeParams{
			SubID: ref.subID, SessionID: ref.sessionID, AgentID: ref.agentID, HaveEntries: haveEntries,
		}, &d)
		if err != nil {
			return transcriptMsg{id: ref.sessionID, err: err}
		}
		return transcriptDeltaMsg{ref: ref, delta: d, initial: true}
	}
}

// unsubscribeCmd closes a subscription (fire-and-forget).
func (m model) unsubscribeCmd(subID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodTranscriptUnsubscribe, api.TranscriptUnsubscribeParams{SubID: subID}, nil)
		return nil
	}
}
