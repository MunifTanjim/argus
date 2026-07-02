package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// userChunks builds n short user chunks — enough to overflow a small viewport.
func userChunks(n int) []transcript.Chunk {
	out := make([]transcript.Chunk, n)
	for i := range out {
		out[i] = transcript.Chunk{ID: fmt.Sprintf("c%d", i), Kind: transcript.ChunkUser, Text: fmt.Sprintf("message %d", i)}
	}
	return out
}

// Opening a session pins the transcript to the bottom (newest content), not the top.
func TestOpenSessionStartsAtBottom(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 10
	sid := "s1"
	m.order = []string{sid}
	m.cursor = 0
	m.sessions = map[string]session.Session{sid: {ID: sid}}
	m.transcriptCache = map[string]cachedTranscript{sid: {chunks: userChunks(20)}}

	res, _ := m.actListOpen(tea.KeyPressMsg{})
	m = res.(model)

	if m.maxScroll() == 0 {
		t.Fatal("setup: cached chunks should overflow the viewport")
	}
	if m.transcript.scroll != m.maxScroll() {
		t.Errorf("open should pin to bottom: scroll=%d, maxScroll=%d", m.transcript.scroll, m.maxScroll())
	}
}

// deltaModel returns a session-mode model holding chunks, with an active parent
// subscription matching subID "x".
func deltaModel() model {
	m := testModel()
	m.width, m.height = 80, 10
	m.mode = modeSession
	m.activeSub = subRef{subID: "x", sessionID: "s1"}
	m.transcriptCache = map[string]cachedTranscript{}
	m.transcript.chunks = userChunks(20)
	return m
}

func appendDelta() transcriptDeltaMsg {
	return transcriptDeltaMsg{
		ref:   subRef{subID: "x", sessionID: "s1"},
		delta: api.TranscriptDelta{FromIndex: 20, Chunks: userChunks(1)},
	}
}

// A delta arriving while pinned to the bottom keeps following the newest content.
func TestDeltaFollowsWhenAtBottom(t *testing.T) {
	m := deltaModel()
	m.transcript.scroll = m.maxScroll() // at bottom

	res, _ := m.Update(appendDelta())
	m = res.(model)

	if got := len(m.transcript.chunks); got != 21 {
		t.Fatalf("delta should append a chunk, len=%d", got)
	}
	if m.transcript.scroll != m.maxScroll() {
		t.Errorf("should tail to bottom: scroll=%d, maxScroll=%d", m.transcript.scroll, m.maxScroll())
	}
}

// A delta arriving while scrolled up must not yank the viewport to the bottom.
func TestDeltaDoesNotFollowWhenScrolledUp(t *testing.T) {
	m := deltaModel()
	m.transcript.scroll = 0 // scrolled to top
	if m.maxScroll() == 0 {
		t.Fatal("setup: chunks should overflow so 'scrolled up' is meaningful")
	}

	res, _ := m.Update(appendDelta())
	m = res.(model)

	if m.transcript.scroll != 0 {
		t.Errorf("scrolled-up view should stay put, scroll=%d", m.transcript.scroll)
	}
}
