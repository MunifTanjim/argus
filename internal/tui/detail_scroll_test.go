package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
)

// TestDetailLineScrollThroughTallItem verifies that Down/Up scroll line-by-line
// through a focused item taller than the viewport. Without this, the single
// item leaves the cursor nowhere to move and the lower part stays unreachable
// by line navigation.
func TestDetailLineScrollThroughTallItem(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("output-line-" + string(rune('A'+i%26)) + "\n")
	}
	m := detailTestModel(claudecode.Chunk{
		ID: "a", Kind: claudecode.ChunkAI, Model: "claude-opus-4-8",
		Items: []claudecode.Item{
			{Kind: claudecode.ItemTool, ToolName: "Bash", ToolInput: `{"command":"ls -la"}`, Result: sb.String()},
		},
	})
	m.width, m.height = 80, 14 // short viewport
	m.topFrame().cursor = 0
	m.drillDetail() // focus the single Bash item

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
