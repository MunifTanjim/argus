package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// freshLines renders the transcript without the card cache, as the reference
// the cached layout must match.
func freshLines(m tview) []string {
	var lines []string
	for i := range m.transcript.chunks {
		if i > 0 {
			lines = append(lines, "")
		}
		block := centerBlock(m.renderChunk(i, i == m.transcript.cursor), m.c.m.containerWidth(), m.c.m.bodyWidth())
		lines = append(lines, strings.Split(block, "\n")...)
	}
	return lines
}

func assertLayoutFresh(t *testing.T, m tview, step string) {
	t.Helper()
	got, _ := m.layoutChunks()
	if want := freshLines(m); !slices.Equal(got, want) {
		t.Errorf("%s: cached layout differs from a fresh render:\n got:\n%s\nwant:\n%s",
			step, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func mixedChunks(n int) []transcript.Chunk {
	md := "## Heading\n\nSome **bold** text with `code`:\n\n- one\n- two\n"
	out := make([]transcript.Chunk, 0, n)
	for i := range n {
		if i%2 == 0 {
			out = append(out, transcript.Chunk{ID: fmt.Sprintf("u%d", i), Kind: transcript.ChunkUser, Text: fmt.Sprintf("do thing %d", i)})
			continue
		}
		out = append(out, transcript.Chunk{ID: fmt.Sprintf("a%d", i), Kind: transcript.ChunkAI, ModelName: "opus", Items: []transcript.Item{
			{ID: "t", Kind: transcript.ItemTool, ToolName: "Bash", InputPreview: "ls"},
			{ID: "x", Kind: transcript.ItemText, Text: md},
		}})
	}
	return out
}

func setAllExpanded(m tview, on bool) {
	for i := range m.transcript.chunks {
		m.setExpanded(i, on)
	}
}

// The cached layout tracks every input a card renders from.
func TestLayoutCacheTracksRenderInputs(t *testing.T) {
	mm := withFocus(withView(testModel(), viewSession), sessionDock)
	m := tvOf(&mm)
	m.transcript.chunks = mixedChunks(6)
	assertLayoutFresh(t, m, "initial")

	m.transcript.cursor = 3
	assertLayoutFresh(t, m, "cursor moved")

	m.setExpanded(3, true)
	assertLayoutFresh(t, m, "card expanded")

	mm = withFocus(mm, mainPane)
	assertLayoutFresh(t, m, "history focused")

	m.c.m.width = 120
	assertLayoutFresh(t, m, "width changed")

	setAllExpanded(m, true)
	assertLayoutFresh(t, m, "all expanded")

	m.c.m.sessions = map[string]session.Session{"": {Agent: "antigravity"}}
	assertLayoutFresh(t, m, "agent known")
}

// A delta that rewrites the last chunk in place (same id) shows the new content.
func TestLayoutCacheDropsChunksReplacedByDelta(t *testing.T) {
	m := deltaModel()
	tvOf(&m).layoutChunks()

	upd := transcript.Chunk{ID: "c19", Kind: transcript.ChunkUser, Text: "rewritten"}
	res, _ := m.Update(transcriptDeltaMsg{
		ref:   subRef{subID: "x", sessionID: "s1"},
		delta: api.TranscriptDelta{FromIndex: 19, Chunks: []transcript.Chunk{upd}},
	})
	assertLayoutFresh(t, tvIn(res.(model)), "delta")
}

// A full reload that keeps chunk ids shows the new content.
func TestLayoutCacheDropsChunksOnFullReload(t *testing.T) {
	reloaded := userChunks(20)
	reloaded[0].Text = "reloaded"
	history := testModel()
	history.width, history.height = 80, 10
	history = withChunks(withView(history, viewHistoryTranscript), userChunks(20))

	for _, tc := range []struct {
		name string
		m    model
		msg  tea.Msg
	}{
		{"live", deltaModel(), transcriptMsg{id: "s1", chunks: reloaded}},
		{"history", history, histTranscriptMsg{addr: trOf(history).history.addr(), chunks: reloaded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.m
			tvOf(&m).layoutChunks()
			res, _ := m.Update(tc.msg)
			assertLayoutFresh(t, tvIn(res.(model)), tc.name)
		})
	}
}

// Rebinding the stream to another transcript shows that transcript's cards.
func TestLayoutCacheDropsChunksOnRebind(t *testing.T) {
	mm := deltaModel()
	m := tvOf(&mm)
	m.layoutChunks()

	other := userChunks(20)
	other[5].Text = "other session"
	ref := subRef{subID: "y", sessionID: "s2", cacheKey: "s2"}
	m.c.m.transcriptCache[ref.key()] = cachedTranscript{chunks: other}
	m.bindStream(ref)
	assertLayoutFresh(t, m, "rebind")
}

func BenchmarkTranscriptScroll(b *testing.B) {
	m := testModel()
	m.width, m.height = 160, 50
	m = withView(m, viewSession)
	v := tvOf(&m)
	v.transcript.chunks = mixedChunks(200)
	v.transcript.cursor = 100
	v.layoutChunks()
	for b.Loop() {
		v.actCardNext(tea.KeyPressMsg{})
		v.put()
		_ = m.View()
	}
}
