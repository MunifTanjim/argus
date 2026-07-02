package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func testModel() model {
	return model{
		transcript: transcriptState{
			expanded:    map[string]bool{},
			mdRenderers: map[int]*glamour.TermRenderer{},
			mdCache:     map[string]string{},
		},
		width:  80,
		height: 24,
	}
}

func sampleChunks() []transcript.Chunk {
	return []transcript.Chunk{
		{ID: "u1", Kind: transcript.ChunkUser, Text: "hello"},
		{ID: "a1", Kind: transcript.ChunkAI, Model: "claude-opus-4-8",
			Thinking: 1, ToolCount: 1,
			Usage: transcript.Usage{Input: 1000, CacheRead: 500, Output: 30},
			Items: []transcript.Item{
				{ID: "a1:0", Kind: transcript.ItemThinking, Text: "reasoning"},
				{ID: "a1:1", Kind: transcript.ItemText, Text: "hi there"},
				{ID: "a1:2", Kind: transcript.ItemTool, ToolName: "Bash", InputPreview: "ls", Result: "out"},
			}},
		{ID: "s1", Kind: transcript.ChunkSystem, Summary: "turn 1.0s"},
	}
}

func loaded() model {
	m := testModel()
	m.transcript.chunks = sampleChunks()
	return m
}

func TestExpandDefaultsAndToggle(t *testing.T) {
	m := loaded()
	ai := m.transcript.chunks[1]
	if m.chunkExpanded(ai) {
		t.Errorf("AI chunk should default collapsed")
	}
	if !m.chunkExpandable(ai) {
		t.Errorf("AI chunk with items should be expandable")
	}
	m.transcript.cursor = 1
	m.toggleExpand(1)
	if !m.chunkExpanded(m.transcript.chunks[1]) {
		t.Errorf("AI chunk should be expanded after toggle")
	}
}

func TestSpaceKeyTogglesFold(t *testing.T) {
	m := loaded()
	m.transcript.cursor = 1 // the expandable AI chunk
	if m.chunkExpanded(m.transcript.chunks[1]) {
		t.Fatal("AI chunk should start collapsed")
	}

	// space (which reports msg.String() == "space" in Bubble Tea v2) folds it open.
	res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: ' '})
	m = res.(model)
	if !m.chunkExpanded(m.transcript.chunks[1]) {
		t.Error("space should expand the selected card")
	}
	res, _ = m.handleTranscriptKey(tea.KeyPressMsg{Code: ' '})
	m = res.(model)
	if m.chunkExpanded(m.transcript.chunks[1]) {
		t.Error("space should collapse the selected card")
	}
}

func TestLayoutChunks(t *testing.T) {
	m := loaded()
	lines, first := m.layoutChunks()
	if len(lines) == 0 {
		t.Fatal("layout produced no lines")
	}
	if len(first) != len(m.transcript.chunks) {
		t.Fatalf("first map mismatch: %d vs %d chunks", len(first), len(m.transcript.chunks))
	}
	// first offsets must be strictly increasing.
	for i := 1; i < len(first); i++ {
		if first[i] <= first[i-1] {
			t.Errorf("first[%d]=%d not after first[%d]=%d", i, first[i], i-1, first[i-1])
		}
	}
}

func TestCursorClamp(t *testing.T) {
	m := loaded()
	m.transcript.cursor = 99
	m.clampCursor()
	if m.transcript.cursor != len(m.transcript.chunks)-1 {
		t.Errorf("clampCursor = %d, want %d", m.transcript.cursor, len(m.transcript.chunks)-1)
	}
	m.transcript.cursor = -5
	m.clampCursor()
	if m.transcript.cursor != 0 {
		t.Errorf("clampCursor = %d, want 0", m.transcript.cursor)
	}
}

func TestRestoreChunkCursorByID(t *testing.T) {
	m := loaded()
	m.transcript.cursor = 1
	id := m.currentChunkID()

	// Simulate a refresh that prepends a chunk, shifting indices.
	m.transcript.chunks = append([]transcript.Chunk{{ID: "new", Kind: transcript.ChunkSystem, Summary: "new"}}, m.transcript.chunks...)
	m.restoreChunkCursor(id, false)

	if m.currentChunkID() != id {
		t.Errorf("cursor not preserved by id: want %q, got %q", id, m.currentChunkID())
	}
}

func TestKeyTurnNav(t *testing.T) {
	m := loaded()
	m.height = 6 // tiny viewport
	last := len(m.transcript.chunks) - 1

	// J/K move the chunk cursor between turns and clamp at the ends.
	for i := 0; i < len(m.transcript.chunks)+3; i++ {
		res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: 'J'})
		m = res.(model)
	}
	if m.transcript.cursor != last {
		t.Errorf("cursor should clamp at last chunk %d, got %d", last, m.transcript.cursor)
	}
	for i := 0; i < len(m.transcript.chunks)+3; i++ {
		res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: 'K'})
		m = res.(model)
	}
	if m.transcript.cursor != 0 {
		t.Errorf("after turn-nav up: cursor=%d, want 0", m.transcript.cursor)
	}
}

func TestKeyTurnNavReanchorsToVisible(t *testing.T) {
	m := loaded()
	m.height = 6 // tiny viewport so the cursor can scroll out of view
	m.transcript.cursor = 0

	// Scroll down so chunk 0 (the cursor) leaves the top of the viewport.
	_, first := m.layoutChunks()
	m.transcript.scroll = first[len(first)-1]
	m.clampScrollNow()
	if m.cursorVisible() {
		t.Fatal("setup: cursor should be off-screen after scrolling")
	}
	wantFirst := m.firstVisibleChunk()
	scrollBefore := m.transcript.scroll

	// J with an off-screen cursor selects the first visible card without moving
	// the viewport (instead of yanking back up to cursor+1).
	res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: 'J'})
	m = res.(model)
	if m.transcript.cursor != wantFirst {
		t.Errorf("J off-screen: tcursor=%d, want first-visible %d", m.transcript.cursor, wantFirst)
	}
	if m.transcript.scroll != scrollBefore {
		t.Errorf("J off-screen should not move the viewport: %d -> %d", scrollBefore, m.transcript.scroll)
	}

	// With the cursor now visible, J advances by one as before.
	if m.cursorVisible() {
		prev := m.transcript.cursor
		res, _ = m.handleTranscriptKey(tea.KeyPressMsg{Code: 'J'})
		m = res.(model)
		if m.transcript.cursor != min(prev+1, len(m.transcript.chunks)-1) {
			t.Errorf("J visible: tcursor=%d, want %d", m.transcript.cursor, min(prev+1, len(m.transcript.chunks)-1))
		}
	}
}

func TestArrowLineScroll(t *testing.T) {
	m := loaded()
	m.height = 6 // tiny viewport so content exceeds it
	cursor0 := m.transcript.cursor

	// Arrows scroll the viewport by lines without moving the chunk cursor.
	res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	if m.transcript.scroll == 0 {
		t.Errorf("down arrow should advance scroll, got %d", m.transcript.scroll)
	}
	if m.transcript.cursor != cursor0 {
		t.Errorf("down arrow should not move the chunk cursor: %d -> %d", cursor0, m.transcript.cursor)
	}
	res, _ = m.handleTranscriptKey(tea.KeyPressMsg{Code: tea.KeyUp})
	m = res.(model)
	if m.transcript.scroll != 0 {
		t.Errorf("up arrow should return scroll toward 0, got %d", m.transcript.scroll)
	}
}

func TestSmartTurnAdvancesWhenCardFits(t *testing.T) {
	m := loaded()
	m.height = 40 // tall viewport: every card fits
	m.transcript.cursor = 0

	// j selects the next turn (no scrolling) when the current card fits.
	res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: 'j'})
	m = res.(model)
	if m.transcript.cursor != 1 {
		t.Errorf("j should select the next turn, cursor=%d", m.transcript.cursor)
	}
	if m.transcript.scroll != 0 {
		t.Errorf("j should not scroll when the card fits, scroll=%d", m.transcript.scroll)
	}
	res, _ = m.handleTranscriptKey(tea.KeyPressMsg{Code: 'k'})
	m = res.(model)
	if m.transcript.cursor != 0 {
		t.Errorf("k should select the previous turn, cursor=%d", m.transcript.cursor)
	}
}

func TestSmartTurnScrollsOversizedCard(t *testing.T) {
	m := loaded()
	m.height = 6 // tiny viewport so the selected card overflows it
	m.transcript.cursor = 0

	_, _, _, overflow := m.selectedChunkOverflow()
	if !overflow {
		t.Fatal("setup: selected card should overflow the viewport")
	}

	// j scrolls through the oversized card instead of skipping its body.
	res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: 'j'})
	m = res.(model)
	if m.transcript.scroll == 0 {
		t.Errorf("j should scroll an oversized card, scroll=%d", m.transcript.scroll)
	}
	if m.transcript.cursor != 0 {
		t.Errorf("j should not move the cursor while scrolling, cursor=%d", m.transcript.cursor)
	}
	res, _ = m.handleTranscriptKey(tea.KeyPressMsg{Code: 'k'})
	m = res.(model)
	if m.transcript.scroll != 0 {
		t.Errorf("k should scroll back up, scroll=%d", m.transcript.scroll)
	}
}

func TestTranscriptViewRenders(t *testing.T) {
	m := loaded()
	out := m.transcriptBody()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected user text in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Claude") {
		t.Errorf("expected Claude header in output, got:\n%s", out)
	}
}

func TestEditDiffBody(t *testing.T) {
	body, ok := editDiff("Edit", `{"file_path":"/x.go","old_string":"a\nb\nc","new_string":"a\nB\nc"}`)
	if !ok {
		t.Fatal("expected a diff for Edit")
	}
	for _, want := range []string{"/x.go", "- b", "+ B", "  a", "  c"} {
		if !strings.Contains(body, want) {
			t.Errorf("diff missing %q in:\n%s", want, body)
		}
	}
}

func TestEditDiffReplaceAll(t *testing.T) {
	body, ok := editDiff("Edit", `{"file_path":"/x.go","old_string":"x","new_string":"y","replace_all":true}`)
	if !ok || !strings.Contains(body, "replace all") {
		t.Errorf("expected replace-all marker, got ok=%v body=%q", ok, body)
	}
}

func TestWriteDiffAllAdded(t *testing.T) {
	body, ok := editDiff("Write", `{"file_path":"/n.go","content":"line1\nline2"}`)
	if !ok {
		t.Fatal("expected a diff for Write")
	}
	if !strings.Contains(body, "+ line1") || !strings.Contains(body, "+ line2") {
		t.Errorf("write diff should mark all lines added:\n%s", body)
	}
}

func TestEditDiffSkipsNonEditTools(t *testing.T) {
	if _, ok := editDiff("Bash", `{"command":"ls"}`); ok {
		t.Error("Bash should not produce a diff")
	}
}
