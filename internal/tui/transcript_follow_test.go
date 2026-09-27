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
	m.sessions = map[string]session.Session{sid: {ID: sid}}
	m.transcriptCache = map[string]cachedTranscript{sid: {chunks: userChunks(20)}}

	res, _ := m.baseKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)

	if tvOf(&m).maxScroll() == 0 {
		t.Fatal("setup: cached chunks should overflow the viewport")
	}
	if trOf(m).transcript.scroll != tvOf(&m).maxScroll() {
		t.Errorf("open should pin to bottom: scroll=%d, maxScroll=%d", trOf(m).transcript.scroll, tvOf(&m).maxScroll())
	}
}

// deltaModel returns a live transcript holding chunks, with an active parent
// subscription matching subID "x".
func deltaModel() model {
	m := testModel()
	m.width, m.height = 80, 10
	m = withLive(m, "s1")
	m = withTr(m, func(t *transcriptComp) { t.activeSub = subRef{subID: "x", sessionID: "s1"} })
	m.transcriptCache = map[string]cachedTranscript{}
	m = withTr(m, func(t *transcriptComp) { t.transcript.chunks = userChunks(20) })
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
	m = withTr(m, func(t *transcriptComp) { t.transcript.scroll = tvOf(&m).maxScroll() }) // at bottom

	res, _ := m.Update(appendDelta())
	m = res.(model)

	if got := len(trOf(m).transcript.chunks); got != 21 {
		t.Fatalf("delta should append a chunk, len=%d", got)
	}
	if trOf(m).transcript.scroll != tvOf(&m).maxScroll() {
		t.Errorf("should tail to bottom: scroll=%d, maxScroll=%d", trOf(m).transcript.scroll, tvOf(&m).maxScroll())
	}
}

// A delta arriving while scrolled up must not yank the viewport to the bottom.
func TestDeltaDoesNotFollowWhenScrolledUp(t *testing.T) {
	m := deltaModel()
	m = withTr(m, func(t *transcriptComp) { t.transcript.scroll = 0 }) // scrolled to top
	if tvOf(&m).maxScroll() == 0 {
		t.Fatal("setup: chunks should overflow so 'scrolled up' is meaningful")
	}

	res, _ := m.Update(appendDelta())
	m = res.(model)

	if trOf(m).transcript.scroll != 0 {
		t.Errorf("scrolled-up view should stay put, scroll=%d", trOf(m).transcript.scroll)
	}
}
