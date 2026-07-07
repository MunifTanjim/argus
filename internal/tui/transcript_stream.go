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
// AgentSessionID, so pre-clear chunks don't survive into the new transcript.
func (m *model) resubscribeOnClear(prev session.Session, existed bool, cur session.Session) tea.Cmd {
	if m.mode != modeSession || cur.ID != m.selectedID {
		return nil
	}
	if m.activeSub.subID == "" || m.activeSub.agentID != "" {
		return nil
	}
	if !existed || cur.AgentSessionID == "" || prev.AgentSessionID == cur.AgentSessionID {
		return nil
	}
	old := m.activeSub.subID
	delete(m.transcriptCache, m.activeSub.key()) // superseded transcript; free its chunks
	m.transcript.err = nil                       // drop any stale pre-clear error
	ref := subRef{subID: newSubID(), sessionID: m.selectedID, cacheKey: m.cacheKeyFor(m.selectedID)}
	return tea.Batch(m.unsubscribeCmd(old), m.bindStream(ref))
}

// bindStream points the active subscription at ref, shows its cached chunks
// immediately (empty for a fresh key), pins the view to the bottom so the catch-up
// delta keeps tailing (see restoreChunkCursor), and returns the subscribe command.
func (m *model) bindStream(ref subRef) tea.Cmd {
	m.activeSub = ref
	m.transcript.chunks = m.transcriptCache[ref.key()].chunks
	m.transcript.cursor = max(0, len(m.transcript.chunks)-1)
	m.transcript.scroll = m.maxScroll()
	return m.subscribeCmd(ref, len(m.transcript.chunks))
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

func applyDelta(chunks []transcript.Chunk, d api.TranscriptDelta) []transcript.Chunk {
	from := d.FromIndex
	if from > len(chunks) {
		from = len(chunks)
	}
	out := make([]transcript.Chunk, 0, from+len(d.Chunks))
	out = append(out, chunks[:from]...)
	out = append(out, d.Chunks...)
	return out
}

// subscribeCmd opens a subscription and delivers the catch-up as a transcriptDeltaMsg.
func (m model) subscribeCmd(ref subRef, haveChunks int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var d api.TranscriptDelta
		err := client.Call(api.MethodTranscriptSubscribe, api.TranscriptSubscribeParams{
			SubID: ref.subID, SessionID: ref.sessionID, AgentID: ref.agentID, HaveChunks: haveChunks,
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
