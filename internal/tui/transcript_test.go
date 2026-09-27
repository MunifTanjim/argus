package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func testModel() model {
	return model{
		render: renderCache{
			mdRenderers: map[int]*glamour.TermRenderer{},
			mdCache:     map[string]string{},
		},
		dock:      newDock(),
		termKeyCh: make(chan termKey, termKeyBuf),
		width:     80,
		height:    24,
		// No sidebars: views get the full width under the frame header, so view
		// tests need not account for the tree or file columns.
		left:  leftSidebarState{hidden: true},
		right: rightSidebarState{hidden: true},
		main:  backStack{homeComp{}},
	}
}

func TestAssistantBrandResolvesAgent(t *testing.T) {
	m := testModel()

	// History transcript: brand comes from the opened history session's agent,
	// not the (absent) live session.
	m = withView(m, viewHistoryTranscript)
	m = withTr(m, func(t *transcriptComp) { t.history.openAgent = "antigravity" })
	if _, name := tvOf(&m).assistantBrand(); name != "Antigravity" {
		t.Errorf("history brand = %q, want Antigravity", name)
	}

	// Live transcript: brand comes from the selected live session's agent.
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Agent: "codex"}}
	m = openLive(m, "s1")
	if _, name := tvOf(&m).assistantBrand(); name != "Codex" {
		t.Errorf("live brand = %q, want Codex", name)
	}
}

func TestAssistantBrandOpenCode(t *testing.T) {
	m := testModel()
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Agent: "opencode"}}
	m = openLive(m, "s1")
	if _, name := tvOf(&m).assistantBrand(); name != "OpenCode" {
		t.Errorf("opencode brand = %q, want OpenCode", name)
	}
}

func TestAssistantBrandDefaultClaude(t *testing.T) {
	m := testModel()
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Agent: ""}}
	m = openLive(m, "s1")
	if _, name := tvOf(&m).assistantBrand(); name != "Claude" {
		t.Errorf("empty-agent brand = %q, want Claude", name)
	}
}

func sampleChunks() []transcript.Chunk {
	return []transcript.Chunk{
		{ID: "u1", Kind: transcript.ChunkUser, Text: "hello"},
		{ID: "a1", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
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
	m = withView(m, viewSession)
	return withChunks(m, sampleChunks())
}

// TestUserChunkWithSkillItemExpandableAndDrillable verifies that a user chunk
// carrying an ItemSkill is marked expandable and its item is reachable in the
// detail drill-down.
func TestUserChunkWithSkillItemExpandableAndDrillable(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80

	skillItem := transcript.Item{
		Kind:         transcript.ItemSkill,
		ToolName:     "Skill",
		ToolID:       "sk-001",
		InputPreview: "superpowers:brainstorming",
	}
	c := transcript.Chunk{
		ID:    "u1",
		Kind:  transcript.ChunkUser,
		Text:  "one line",
		Items: []transcript.Item{skillItem},
	}

	// The chunk must be expandable even though the text is short.
	if !m.c.m.chunkExpandable(c) {
		t.Error("user chunk with skill item should be expandable")
	}

	// When expanded, the rendered card must surface a skill affordance.
	m.transcript.chunks = []transcript.Chunk{c}
	m.transcript.expanded[c.ID] = true
	out := m.renderChunk(0, false)
	if !strings.Contains(out, "Skill") {
		t.Errorf("rendered user card should show a Skill affordance:\n%s", out)
	}
	if !strings.Contains(out, "superpowers:brainstorming") {
		t.Errorf("rendered user card should show the skill name:\n%s", out)
	}

	// The detail frame surfaces the original message as a leading prompt, then the
	// skill item so it can be drilled by ToolID.
	dm := detailTestModel(c)
	f := dm.topFrame()
	if f == nil || len(f.items) != 2 {
		t.Fatalf("detail frame should have 2 items, got %d", len(f.items))
	}
	if f.items[0].Kind != transcript.ItemPrompt || f.items[0].Text != "one line" {
		t.Errorf("first detail item should be the original message prompt, got %+v", f.items[0])
	}
	if f.items[1].ToolID != "sk-001" {
		t.Errorf("detail item ToolID = %q, want %q", f.items[1].ToolID, "sk-001")
	}

	// Drilling into the skill item should focus it (non-drillable leaf).
	f.cursor = 1
	dm.drillDetail()
	if len(dm.transcript.detailStack) != 2 || !dm.topFrame().focused {
		t.Fatalf("drill should focus the skill item: frames=%d focused=%v",
			len(dm.transcript.detailStack), dm.topFrame().focused)
	}
}

// TestTextlessUserChunkWithSkillItemDrillable verifies that a user chunk carrying an
// ItemSkill but no Text (codex-style) is detailable and drillable end-to-end.
func TestTextlessUserChunkWithSkillItemDrillable(t *testing.T) {
	m := testModel()
	m.width = 80

	skillItem := transcript.Item{
		Kind:         transcript.ItemSkill,
		ToolName:     "Skill",
		ToolID:       "sk-002",
		InputPreview: "superpowers:brainstorming",
	}
	c := transcript.Chunk{
		ID:    "u2",
		Kind:  transcript.ChunkUser,
		Text:  "", // codex style: no text
		Items: []transcript.Item{skillItem},
	}

	// detailable must be true even with empty Text.
	if !m.detailable(c) {
		t.Error("text-less user chunk with skill item should be detailable")
	}

	// The detail frame must expose the item.
	dm := detailTestModel(c)
	f := dm.topFrame()
	if f == nil || len(f.items) != 1 {
		t.Fatalf("detail frame should have 1 item, got %d", len(f.items))
	}
	if f.items[0].ToolID != "sk-002" {
		t.Errorf("detail item ToolID = %q, want %q", f.items[0].ToolID, "sk-002")
	}

	// Drilling into the skill item should focus it (non-drillable leaf).
	dm.drillDetail()
	if len(dm.transcript.detailStack) != 2 || !dm.topFrame().focused {
		t.Fatalf("drill should focus the skill item: frames=%d focused=%v",
			len(dm.transcript.detailStack), dm.topFrame().focused)
	}
}

// TestUserChunkExpandableByWrappedLines verifies long-wrapping user chunks are collapsible.
func TestUserChunkExpandableByWrappedLines(t *testing.T) {
	m := testModel()
	m.width = 80

	short := transcript.Chunk{ID: "u1", Kind: transcript.ChunkUser, Text: "one line"}
	if m.chunkExpandable(short) {
		t.Error("short user chunk should not be expandable")
	}

	// One newline, but the line is far longer than the bubble width.
	long := transcript.Chunk{ID: "u2", Kind: transcript.ChunkUser,
		Text: strings.Repeat("word ", 400)}
	if strings.Count(long.Text, "\n") >= maxCollapsedLines {
		t.Fatal("fixture should have few source newlines")
	}
	if !m.chunkExpandable(long) {
		t.Error("long-wrapping user chunk should be expandable")
	}
}

func TestExpandDefaultsAndToggle(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	ai := m.transcript.chunks[1]
	if m.chunkExpanded(ai) {
		t.Errorf("AI chunk should default collapsed")
	}
	if !m.c.m.chunkExpandable(ai) {
		t.Errorf("AI chunk with items should be expandable")
	}
	m.transcript.cursor = 1
	m.setExpanded(1, true)
	if !m.chunkExpanded(m.transcript.chunks[1]) {
		t.Errorf("AI chunk should be expanded after toggle")
	}
}

func TestFoldKeysExpandAndCollapse(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.transcript.cursor = 1 // the expandable AI chunk
	if m.chunkExpanded(m.transcript.chunks[1]) {
		t.Fatal("AI chunk should start collapsed")
	}

	m.handleTranscriptKey(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if !m.chunkExpanded(m.transcript.chunks[1]) {
		t.Error("l should expand the selected card")
	}
	m.handleTranscriptKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	if m.chunkExpanded(m.transcript.chunks[1]) {
		t.Error("h should collapse the selected card")
	}
}

func TestLayoutChunks(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
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
	mm := loaded()
	m := tvOf(&mm)
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
	mm := loaded()
	m := tvOf(&mm)
	m.transcript.cursor = 1
	id := m.currentChunkID()

	// Simulate a refresh that prepends a chunk, shifting indices.
	m.transcript.chunks = append([]transcript.Chunk{{ID: "new", Kind: transcript.ChunkSystem, Summary: "new"}}, m.transcript.chunks...)
	m.restoreChunkCursor(id, false)

	if m.currentChunkID() != id {
		t.Errorf("cursor not preserved by id: want %q, got %q", id, m.currentChunkID())
	}
}

func TestKeyCardNav(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.c.m.height = 6
	last := len(m.transcript.chunks) - 1

	// }/{ move the chunk cursor between cards and clamp at the ends.
	for i := 0; i < len(m.transcript.chunks)+3; i++ {
		m.handleTranscriptKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	}
	if m.transcript.cursor != last {
		t.Errorf("cursor should clamp at last chunk %d, got %d", last, m.transcript.cursor)
	}
	for i := 0; i < len(m.transcript.chunks)+3; i++ {
		m.handleTranscriptKey(tea.KeyPressMsg{Code: '{', Text: "{"})
	}
	if m.transcript.cursor != 0 {
		t.Errorf("after card-nav up: cursor=%d, want 0", m.transcript.cursor)
	}
}

func TestKeyCardNavReanchorsToVisible(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.c.m.height = 6 // tiny viewport so the cursor can scroll out of view
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

	// } with an off-screen cursor selects the first visible card without moving
	// the viewport (instead of yanking back up to cursor+1).
	m.handleTranscriptKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if m.transcript.cursor != wantFirst {
		t.Errorf("} off-screen: tcursor=%d, want first-visible %d", m.transcript.cursor, wantFirst)
	}
	if m.transcript.scroll != scrollBefore {
		t.Errorf("} off-screen should not move the viewport: %d -> %d", scrollBefore, m.transcript.scroll)
	}

	// With the cursor now visible, } advances by one as before.
	if m.cursorVisible() {
		prev := m.transcript.cursor
		m.handleTranscriptKey(tea.KeyPressMsg{Code: '}', Text: "}"})
		if m.transcript.cursor != min(prev+1, len(m.transcript.chunks)-1) {
			t.Errorf("} visible: tcursor=%d, want %d", m.transcript.cursor, min(prev+1, len(m.transcript.chunks)-1))
		}
	}
}

func scrollTestView(height int, texts ...string) tview {
	mm := withView(testModel(), viewSession)
	m := tvOf(&mm)
	m.c.m.height = height
	for i, text := range texts {
		m.transcript.chunks = append(m.transcript.chunks,
			transcript.Chunk{ID: fmt.Sprintf("u%d", i), Kind: transcript.ChunkUser, Text: text})
	}
	return m
}

func pressScrollKey(m tview, code rune) tview {
	m.handleTranscriptKey(tea.KeyPressMsg{Code: code, Text: string(code)})
	return m
}

func lineScrollTestView() tview {
	m := scrollTestView(14, "a", "b", "c", "d", "e", "f", "g")
	m.transcript.chunks[3] = transcript.Chunk{ID: "a3", Kind: transcript.ChunkAI, Text: strings.Repeat("line\n\n", 20)}
	return m
}

func TestLineScrollDownNeverSkipsACard(t *testing.T) {
	m := lineScrollTestView()
	lines, first := m.layoutChunks()
	last := len(first) - 1
	for range 200 {
		cursor, scroll := m.transcript.cursor, m.transcript.scroll
		_, end := chunkSpan(cursor, first, len(lines))
		bottomHidden := end > scroll+m.viewportHeight()
		m = pressScrollKey(m, 'j')
		switch {
		case bottomHidden:
			if m.transcript.cursor != cursor || m.transcript.scroll <= scroll {
				t.Fatalf("j with card %d cut off at the bottom should scroll: cursor %d->%d scroll %d->%d", cursor, cursor, m.transcript.cursor, scroll, m.transcript.scroll)
			}
		case cursor < last:
			if m.transcript.cursor != cursor+1 {
				t.Fatalf("j with card %d fully visible should select the next card, cursor=%d", cursor, m.transcript.cursor)
			}
		default:
			if m.transcript.cursor != last || m.transcript.scroll != scroll {
				t.Fatalf("j at the fully visible last card should stay put: cursor=%d scroll %d->%d", m.transcript.cursor, scroll, m.transcript.scroll)
			}
			return
		}
		if !m.cursorVisible() {
			t.Fatalf("j left card %d off screen at scroll %d", m.transcript.cursor, m.transcript.scroll)
		}
	}
	t.Fatal("j never reached the last card")
}

func TestLineScrollUpNeverSkipsACard(t *testing.T) {
	m := lineScrollTestView()
	lines, first := m.layoutChunks()
	m.transcript.cursor = len(first) - 1
	m.transcript.scroll = m.maxScroll()
	for range 200 {
		cursor, scroll := m.transcript.cursor, m.transcript.scroll
		start, _ := chunkSpan(cursor, first, len(lines))
		topHidden := start < scroll
		m = pressScrollKey(m, 'k')
		switch {
		case topHidden:
			if m.transcript.cursor != cursor || m.transcript.scroll >= scroll {
				t.Fatalf("k with card %d cut off at the top should scroll: cursor %d->%d scroll %d->%d", cursor, cursor, m.transcript.cursor, scroll, m.transcript.scroll)
			}
		case cursor > 0:
			if m.transcript.cursor != cursor-1 {
				t.Fatalf("k with card %d fully visible should select the previous card, cursor=%d", cursor, m.transcript.cursor)
			}
		default:
			if m.transcript.cursor != 0 || m.transcript.scroll != 0 {
				t.Fatalf("k at the first card should stay put: cursor=%d scroll=%d", m.transcript.cursor, m.transcript.scroll)
			}
			return
		}
		if !m.cursorVisible() {
			t.Fatalf("k left card %d off screen at scroll %d", m.transcript.cursor, m.transcript.scroll)
		}
	}
	t.Fatal("k never reached the first card")
}

func TestLineScrollRevealsNextCardBelowViewport(t *testing.T) {
	m := scrollTestView(14, "a", "b", "c", "d", "e", "f", "g", "h")
	lines, first := m.layoutChunks()
	h := m.viewportHeight()
	cursor := 1
	_, end := chunkSpan(cursor, first, len(lines))
	m.transcript.cursor, m.transcript.scroll = cursor, end-h
	if m.transcript.scroll <= 0 || first[cursor+1] < end {
		t.Fatalf("setup: card %d should end at the bottom of a scrolled viewport (h=%d, first=%v)", cursor, h, first)
	}

	m = pressScrollKey(m, 'j')
	if m.transcript.cursor != cursor+1 {
		t.Fatalf("j should select the next card, cursor=%d", m.transcript.cursor)
	}
	if first[cursor+1] >= m.transcript.scroll+h {
		t.Errorf("j should scroll the newly selected card into view, scroll=%d first=%d h=%d", m.transcript.scroll, first[cursor+1], h)
	}
}

func TestLineScrollInsideLongCardKeepsSelection(t *testing.T) {
	m := scrollTestView(14, "a", "b", "c")
	m.transcript.chunks[1] = transcript.Chunk{ID: "a1", Kind: transcript.ChunkAI, Text: strings.Repeat("line\n\n", 40)}
	lines, first := m.layoutChunks()
	if start, end := chunkSpan(1, first, len(lines)); end-start < m.viewportHeight()+6 {
		t.Fatalf("setup: card 1 should be taller than the viewport plus two scroll steps (span=%d, h=%d)", end-start, m.viewportHeight())
	}
	m.transcript.cursor = 1
	m.transcript.scroll = first[1] + 3

	m = pressScrollKey(m, 'j')
	if m.transcript.cursor != 1 {
		t.Errorf("j inside a long card should keep it selected, cursor=%d", m.transcript.cursor)
	}
	m = pressScrollKey(m, 'k')
	m = pressScrollKey(m, 'k')
	if m.transcript.cursor != 1 {
		t.Errorf("k inside a long card should keep it selected, cursor=%d", m.transcript.cursor)
	}
}

func TestLineScrollAtEdgeMovesCursor(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.c.m.height = 200 // every card fits: nothing to scroll
	m.transcript.cursor = 0
	last := len(m.transcript.chunks) - 1

	for i := 1; i <= last; i++ {
		m.handleTranscriptKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if m.transcript.cursor != i || m.transcript.scroll != 0 {
			t.Fatalf("j #%d: cursor=%d scroll=%d, want cursor=%d scroll=0", i, m.transcript.cursor, m.transcript.scroll, i)
		}
	}
	m.handleTranscriptKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.transcript.cursor != last {
		t.Fatalf("j at the last card should stay put, cursor=%d", m.transcript.cursor)
	}
	m.handleTranscriptKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.transcript.cursor != last-1 {
		t.Fatalf("up should select the previous card, cursor=%d", m.transcript.cursor)
	}
}

func TestCardNavWhenCardFits(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.c.m.height = 40 // tall viewport: every card fits
	m.transcript.cursor = 0

	m.handleTranscriptKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if m.transcript.cursor != 1 {
		t.Errorf("} should select the next card, cursor=%d", m.transcript.cursor)
	}
	if m.transcript.scroll != 0 {
		t.Errorf("} should not scroll when the card fits, scroll=%d", m.transcript.scroll)
	}
	m.handleTranscriptKey(tea.KeyPressMsg{Code: '{', Text: "{"})
	if m.transcript.cursor != 0 {
		t.Errorf("{ should select the previous card, cursor=%d", m.transcript.cursor)
	}
}

func TestCardNavSkipsOversizedCard(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.c.m.height = 6 // tiny viewport so the selected card overflows it
	m.transcript.cursor = 0

	lines, first := m.layoutChunks()
	if start, end := chunkSpan(0, first, len(lines)); end-start <= m.viewportHeight() {
		t.Fatal("setup: selected card should overflow the viewport")
	}

	m.handleTranscriptKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if m.transcript.cursor != 1 {
		t.Errorf("} should jump past an oversized card, cursor=%d", m.transcript.cursor)
	}
	if start, _ := chunkSpan(1, first, len(lines)); m.transcript.scroll != start {
		t.Errorf("} should pin the next card to the top, scroll=%d, want %d", m.transcript.scroll, start)
	}
}

func TestTranscriptViewRenders(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	out := m.transcriptBody()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected user text in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Claude") {
		t.Errorf("expected Claude header in output, got:\n%s", out)
	}
}

func TestTranscriptBrandFromTool(t *testing.T) {
	mm := testModel()
	mm.sessions = map[string]session.Session{"s1": {ID: "s1", Agent: "codex"}}
	mm = withChunks(openLive(mm, "s1"), sampleChunks())
	m := tvOf(&mm)
	out := m.transcriptBody()
	if !strings.Contains(out, "Codex") {
		t.Errorf("expected Codex header for a codex session, got:\n%s", out)
	}
	if strings.Contains(out, "Claude") {
		t.Errorf("codex session should not render Claude brand, got:\n%s", out)
	}
}

func TestRenderShellCard(t *testing.T) {
	m := bareTv()
	c := transcript.Chunk{
		ID: "sh1", Kind: transcript.ChunkShell,
		Text:   "echo hi",
		Detail: "Exit code: 0\nDuration: 0.0417 seconds\nOutput:\nworld\n",
	}
	m.transcript.chunks = []transcript.Chunk{c}
	m.transcript.expanded = map[string]bool{}

	// AI-card style: the "Shell" header sits outside/above the card border; the
	// command lives inside the card.
	out := m.renderChunk(0, false)
	headerIdx := strings.Index(out, "Shell")
	borderIdx := strings.Index(out, "╭")
	if headerIdx < 0 || borderIdx < 0 || headerIdx > borderIdx {
		t.Fatalf("Shell header should sit above the card border:\n%s", out)
	}
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("collapsed card should show the command:\n%s", out)
	}
	if strings.Contains(out, "world") {
		t.Fatalf("collapsed should not show the output:\n%s", out)
	}

	m.transcript.expanded["sh1"] = true
	out = m.renderChunk(0, false)
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("expanded should still show the command:\n%s", out)
	}
	if !strings.Contains(out, "world") {
		t.Fatalf("expanded render missing output:\n%s", out)
	}
	if !strings.Contains(out, "Result") {
		t.Fatalf("expanded render missing Result label:\n%s", out)
	}
}

func TestRenderShellCardError(t *testing.T) {
	m := bareTv()
	c := transcript.Chunk{
		ID: "sh1", Kind: transcript.ChunkShell,
		Text: "false", Detail: "Exit code: 1\nOutput:\n", IsError: true,
	}
	m.transcript.chunks = []transcript.Chunk{c}
	m.transcript.expanded = map[string]bool{"sh1": true}
	if out := m.renderChunk(0, false); !strings.Contains(out, "Error") {
		t.Fatalf("expected Error label for nonzero exit, got:\n%s", out)
	}
}

func TestRenderDetailShell(t *testing.T) {
	m := testModel()
	c := transcript.Chunk{
		ID: "sh1", Kind: transcript.ChunkShell,
		Text:   "echo hi",
		Detail: "Exit code: 0\nOutput:\nworld\n",
	}
	out := m.renderDetail(c)
	if !strings.Contains(out, "Shell") {
		t.Fatalf("detail should show the Shell header:\n%s", out)
	}
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("detail should show the command:\n%s", out)
	}
	if !strings.Contains(out, "world") || !strings.Contains(out, "Result") {
		t.Fatalf("detail should show the labeled output:\n%s", out)
	}
}

func TestItemRowSubagentLabelHidesStatusAndDesc(t *testing.T) {
	it := transcript.Item{
		Kind: transcript.ItemSubagent,
		Subagents: []transcript.Subagent{{
			Type:   "default",
			Name:   "Volta",
			Status: "closed",
			Desc:   "the full task message",
		}},
	}
	out := itemRow(it)
	if !strings.Contains(out, "Spawn Agent: Volta (default)") {
		t.Errorf("expected new label format, got:\n%s", out)
	}
	if strings.Contains(out, "closed") {
		t.Errorf("collapsed row should not show status (visible only when expanded), got:\n%s", out)
	}
	if strings.Contains(out, "full task message") {
		t.Errorf("collapsed row should not show desc (moved to expanded Input), got:\n%s", out)
	}
}

func TestItemRowAgentToolLabel(t *testing.T) {
	wait := transcript.Item{
		Kind: transcript.ItemSubagent, ToolName: "wait_agent",
		Subagents: []transcript.Subagent{{ID: "a1", Name: "Volta"}},
	}
	if got := itemRow(wait); !strings.Contains(got, "Wait Agent: Volta") {
		t.Errorf("wait_agent row = %q, want label 'Wait Agent: Volta'", got)
	}
	closeIt := transcript.Item{
		Kind: transcript.ItemSubagent, ToolName: "close_agent",
		Subagents: []transcript.Subagent{{ID: "a1", Name: "Volta"}},
	}
	if got := itemRow(closeIt); !strings.Contains(got, "Close Agent: Volta") {
		t.Errorf("close_agent row = %q, want label 'Close Agent: Volta'", got)
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
