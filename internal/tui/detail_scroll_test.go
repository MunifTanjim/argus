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
