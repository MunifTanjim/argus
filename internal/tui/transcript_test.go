package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func testModel() model {
	return model{
		transcript: transcriptState{
			expanded:    map[string]bool{},
			mdRenderers: map[int]*glamour.TermRenderer{},
			mdCache:     map[string]string{},
		},
		termKeyCh: make(chan termKey, termKeyBuf),
		width:     80,
		height:    24,
	}
}

func TestAssistantBrandResolvesAgent(t *testing.T) {
	m := testModel()

	// History transcript: brand comes from the opened history session's agent,
	// not the (absent) live session.
	m.mode = modeHistoryTranscript
	m.history.openAgent = "antigravity"
	if _, name := m.assistantBrand(); name != "Antigravity" {
		t.Errorf("history brand = %q, want Antigravity", name)
	}

	// Live transcript: brand comes from the selected live session's agent.
	m.mode = modeSession
	m.selectedID = "s1"
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Agent: "codex"}}
	if _, name := m.assistantBrand(); name != "Codex" {
		t.Errorf("live brand = %q, want Codex", name)
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
	m.transcript.chunks = sampleChunks()
	return m
}

// TestUserChunkWithSkillItemExpandableAndDrillable verifies that a user chunk
// carrying an ItemSkill is marked expandable and its item is reachable in the
// detail drill-down.
func TestUserChunkWithSkillItemExpandableAndDrillable(t *testing.T) {
	m := testModel()
	m.width = 80

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
	if !m.chunkExpandable(c) {
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

// TestSmartTurnAtLastCardStaysAtBottom verifies j on a fully-scrolled last card stays put.
func TestSmartTurnAtLastCardStaysAtBottom(t *testing.T) {
	m := testModel()
	m.height = 6 // tiny viewport so the last card overflows it
	m.transcript.chunks = []transcript.Chunk{
		{ID: "u1", Kind: transcript.ChunkUser, Text: "hi"},
		{ID: "u2", Kind: transcript.ChunkUser, Text: strings.Repeat("line\n", 40)},
	}
	m.transcript.cursor = 1 // last card

	_, end, h, overflow := m.selectedChunkOverflow()
	if !overflow {
		t.Fatal("setup: last card should overflow the viewport")
	}
	m.transcript.scroll = end - h // scroll to the bottom of the tall last card
	m.clampScrollNow()
	bottom := m.transcript.scroll

	res, _ := m.handleTranscriptKey(tea.KeyPressMsg{Code: 'j'})
	m = res.(model)
	if m.transcript.scroll != bottom {
		t.Fatalf("j at bottom of last card should stay put: %d -> %d", bottom, m.transcript.scroll)
	}
	if m.transcript.cursor != 1 {
		t.Fatalf("cursor should stay on the last card, got %d", m.transcript.cursor)
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

func TestTranscriptBrandFromTool(t *testing.T) {
	m := loaded()
	m.selectedID = "s1"
	m.sessions = map[string]session.Session{"s1": {ID: "s1", Agent: "codex"}}
	out := m.transcriptBody()
	if !strings.Contains(out, "Codex") {
		t.Errorf("expected Codex header for a codex session, got:\n%s", out)
	}
	if strings.Contains(out, "Claude") {
		t.Errorf("codex session should not render Claude brand, got:\n%s", out)
	}
}

func TestRenderShellCard(t *testing.T) {
	m := testModel()
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
	m := testModel()
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
