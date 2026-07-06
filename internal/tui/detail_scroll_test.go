package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// TestDetailLineScrollThroughTallItem verifies Down/Up scroll line-by-line
// through a focused item taller than the viewport (else its lower part is
// unreachable, since a lone item gives the cursor nowhere to move).
func TestDetailLineScrollThroughTallItem(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("output-line-" + string(rune('A'+i%26)) + "\n")
	}
	m := detailTestModel(transcript.Chunk{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{
			{Kind: transcript.ItemTool, ToolName: "Bash", ToolInput: `{"command":"ls -la"}`, Result: sb.String()},
		},
	})
	m.width, m.height = 80, 14 // short viewport
	m.topFrame().cursor = 0
	m.drillDetail()

	if _, s, e, ok := m.cursorOverflow(m.topFrame()); !ok {
		t.Fatalf("fixture not tall enough to overflow: lines=%d", e-s)
	}

	k := tea.KeyPressMsg{}
	mm, _ := m.actDetailDown(k)
	m = mm.(model)
	if got := m.topFrame().scroll; got != 1 {
		t.Fatalf("Down should scroll a tall item one line, scroll=%d", got)
	}
	mm, _ = m.actDetailUp(k)
	m = mm.(model)
	if got := m.topFrame().scroll; got != 0 {
		t.Fatalf("Up should scroll back up, scroll=%d", got)
	}
}

// tallFocusedDetailModel drills into a lone item taller than the viewport.
func tallFocusedDetailModel(t *testing.T) model {
	t.Helper()
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("output-line-" + string(rune('A'+i%26)) + "\n")
	}
	m := detailTestModel(transcript.Chunk{
		ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8",
		Items: []transcript.Item{
			{Kind: transcript.ItemTool, ToolName: "Bash", ToolInput: `{"command":"ls -la"}`, Result: sb.String()},
		},
	})
	m.width, m.height = 80, 14
	m.topFrame().cursor = 0
	m.drillDetail()
	if m.frameMaxScroll(m.topFrame()) == 0 {
		t.Fatal("fixture not tall enough to overflow")
	}
	return m
}

// TestDetailDownAtBottomStaysPut verifies Down on a fully-scrolled last item stays put.
func TestDetailDownAtBottomStaysPut(t *testing.T) {
	m := tallFocusedDetailModel(t)
	maxS := m.frameMaxScroll(m.topFrame())
	m.topFrame().scroll = maxS

	mm, _ := m.actDetailDown(tea.KeyPressMsg{})
	m = mm.(model)
	if got := m.topFrame().scroll; got != maxS {
		t.Fatalf("Down at bottom should stay at %d, got %d", maxS, got)
	}
}

// TestDetailBottomReachesTrueBottom verifies G reaches the last line of tall items.
func TestDetailBottomReachesTrueBottom(t *testing.T) {
	k := tea.KeyPressMsg{}

	// Tall focused item.
	m := tallFocusedDetailModel(t)
	mm, _ := m.actDetailBottom(k)
	m = mm.(model)
	if want := m.frameMaxScroll(m.topFrame()); m.topFrame().scroll != want || want == 0 {
		t.Fatalf("G on tall item: scroll=%d, want %d (>0)", m.topFrame().scroll, want)
	}

	// Body frame (non-AI chunk): no items, scrolls the pre-rendered body.
	m = detailTestModel(transcript.Chunk{
		ID: "s", Kind: transcript.ChunkSystem, Detail: strings.Repeat("detail-line\n", 60),
	})
	m.width, m.height = 80, 14
	if m.topFrame().items != nil {
		t.Fatal("system chunk should render as a body frame")
	}
	mm, _ = m.actDetailBottom(k)
	m = mm.(model)
	if want := m.frameMaxScroll(m.topFrame()); m.topFrame().scroll != want || want == 0 {
		t.Fatalf("G on body frame: scroll=%d, want %d (>0)", m.topFrame().scroll, want)
	}
}

// manyItemDetailModel builds a detail frame whose collapsed item list is far
// taller than the viewport, so scrolling can push the cursor item off-screen.
func manyItemDetailModel(t *testing.T) model {
	t.Helper()
	var items []transcript.Item
	for i := 0; i < 30; i++ {
		items = append(items, transcript.Item{Kind: transcript.ItemTool, ToolName: "Bash",
			ToolInput: `{"command":"ls"}`, Result: "out"})
	}
	m := detailTestModel(transcript.Chunk{ID: "a", Kind: transcript.ChunkAI, ModelName: "Opus 4.8", Items: items})
	m.width, m.height = 80, 14
	if m.frameMaxScroll(m.topFrame()) == 0 {
		t.Fatal("fixture not tall enough to overflow")
	}
	return m
}

func (m model) cursorLineStart() int {
	_, start, _ := m.frameLines(m.topFrame(), m.containerWidth())
	return start
}

// TestDetailDownReanchorsToViewport verifies that after scrolling the cursor item
// off the top with ctrl+d, Down re-anchors the cursor to a visible item instead
// of moving from the stale off-screen cursor (which would snap the viewport back).
func TestDetailDownReanchorsToViewport(t *testing.T) {
	m := manyItemDetailModel(t)
	k := tea.KeyPressMsg{}
	for i := 0; i < 3; i++ { // scroll item 0 well off the top
		mm, _ := m.actDetailHalfDown(k)
		m = mm.(model)
	}
	s0 := m.topFrame().scroll
	if s0 == 0 || m.cursorLineStart() >= s0 {
		t.Fatalf("setup: cursor should be scrolled off-screen (scroll=%d, cursorStart=%d)", s0, m.cursorLineStart())
	}

	mm, _ := m.actDetailDown(k)
	m = mm.(model)

	if got := m.topFrame().scroll; got != s0 {
		t.Fatalf("Down should not move the viewport when re-anchoring: scroll %d -> %d", s0, got)
	}
	if start := m.cursorLineStart(); start < s0 {
		t.Fatalf("Down should select a visible item, cursor start=%d < scroll=%d", start, s0)
	}
}

// TestDetailUpReanchorsToViewport is the mirror: with the cursor scrolled off the
// bottom, Up re-anchors to a visible item rather than jumping to cursor-1.
func TestDetailUpReanchorsToViewport(t *testing.T) {
	m := manyItemDetailModel(t)
	m.topFrame().cursor = 29 // last item, far below the top
	m.topFrame().scroll = 0

	mm, _ := m.actDetailUp(tea.KeyPressMsg{})
	m = mm.(model)

	if got := m.topFrame().scroll; got != 0 {
		t.Fatalf("Up should not move the viewport when re-anchoring: scroll 0 -> %d", got)
	}
	if start := m.cursorLineStart(); start >= m.detailBodyHeight(m.topFrame()) {
		t.Fatalf("Up should select a visible item, cursor start=%d below viewport", start)
	}
}

// TestDetailHalfDownClampsScroll verifies ctrl+d clamps the scroll offset to the content bounds.
func TestDetailHalfDownClampsScroll(t *testing.T) {
	m := tallFocusedDetailModel(t)
	k := tea.KeyPressMsg{}
	for i := 0; i < 10; i++ {
		mm, _ := m.actDetailHalfDown(k)
		m = mm.(model)
	}
	maxS := m.frameMaxScroll(m.topFrame())
	if got := m.topFrame().scroll; got != maxS {
		t.Fatalf("half-down should clamp to %d, got %d", maxS, got)
	}
	mm, _ := m.actDetailHalfUp(k)
	m = mm.(model)
	if got := m.topFrame().scroll; got >= maxS {
		t.Fatalf("half-up after clamped half-down should move up immediately: %d -> %d", maxS, got)
	}
}
