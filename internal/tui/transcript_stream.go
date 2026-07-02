package tui

import (
	"crypto/rand"
	"encoding/hex"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// newSubID returns a globally-unique subscription id (the gateway keys on it).
func newSubID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// applyDelta truncates chunks to d.FromIndex then appends d.Chunks.
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
