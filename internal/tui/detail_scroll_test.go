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
