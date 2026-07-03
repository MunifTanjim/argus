package tui

import (
	"crypto/rand"
	"encoding/hex"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// resubscribeOnClear reopens the live session transcript when the open session's
// ClaudeSessionID changes under it (a /clear swaps the transcript file in place).
// Returns nil when it doesn't apply: not viewing this session, drilled into a
// subagent, the session is new to us, or the id is unchanged. Resets to the fresh
// cache key so no pre-clear chunks survive, and evicts the superseded entry.
func (m *model) resubscribeOnClear(prev session.Session, existed bool, cur session.Session) tea.Cmd {
	if m.mode != modeSession || cur.ID != m.selectedID {
		return nil
	}
	if m.activeSub.subID == "" || m.activeSub.agentID != "" {
		return nil
	}
	if !existed || cur.ClaudeSessionID == "" || prev.ClaudeSessionID == cur.ClaudeSessionID {
		return nil
	}
	old := m.activeSub.subID
	oldKey := m.activeSub.key()
	ref := subRef{subID: newSubID(), sessionID: m.selectedID, cacheKey: m.cacheKeyFor(m.selectedID)}
	m.activeSub = ref
	delete(m.transcriptCache, oldKey) // superseded transcript; free its chunks
	m.transcript.chunks = m.transcriptCache[ref.key()].chunks // fresh key → empty
	m.transcript.err = nil                                     // drop any stale pre-clear error
	m.transcript.cursor = max(0, len(m.transcript.chunks)-1)
	m.transcript.scroll = m.maxScroll()
	return tea.Batch(m.unsubscribeCmd(old), m.subscribeCmd(ref, len(m.transcript.chunks)))
}

// cacheKeyFor returns the transcript-cache discriminator for a session: its
// ClaudeSessionID (changes on /clear), falling back to the argus id before a hook
// has set one.
func (m model) cacheKeyFor(sessionID string) string {
	if s, ok := m.sessions[sessionID]; ok && s.ClaudeSessionID != "" {
		return s.ClaudeSessionID
	}
	return sessionID
}

// newSubID returns a globally-unique subscription id (the gateway keys on it).
func newSubID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// applyDelta truncates chunks to d.FromIndex then appends d.Chunks.
func applyDelta(chunks []claudecode.Chunk, d api.TranscriptDelta) []claudecode.Chunk {
	from := d.FromIndex
	if from > len(chunks) {
		from = len(chunks)
	}
	out := make([]claudecode.Chunk, 0, from+len(d.Chunks))
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
