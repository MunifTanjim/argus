package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func testModel() model {
	return model{
		render: renderCache{
			mdRenderers: map[int]*glamour.TermRenderer{},
			mdCache:     map[string]string{},
		},
		dock:       newDock(),
		termKeyCh:  make(chan termKey, termKeyBuf),
		listScroll: map[string]int{},
		width:      80,
		height:     24,
		// No sidebars: views get the full width under the frame header, so view
		// tests need not account for the tree or file columns.
		left:  leftSidebarState{hidden: true},
		right: rightSidebarState{hidden: true},
		main:  backStack{homeComp{}},
	}
}

func loaded() model {
	m := testModel()
	m = withView(m, viewSession)
	return withEntries(m, sampleEntries())
}

func TestUserEntryExpandableByWrappedLines(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80

	short := transcript.Entry{ID: "u1", Kind: transcript.EntryUser, Text: "one line"}
	if m.entryExpandable(short) {
		t.Error("short user entry should not be expandable")
	}

	// One newline, but the line is far longer than the bubble width.
	long := transcript.Entry{ID: "u2", Kind: transcript.EntryUser,
		Text: strings.Repeat("word ", 400)}
	if strings.Count(long.Text, "\n") >= maxCollapsedLines {
		t.Fatal("fixture should have few source newlines")
	}
	if !m.entryExpandable(long) {
		t.Error("long-wrapping user entry should be expandable")
	}
}

func TestExpandDefaultsAndToggle(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	think := m.transcript.entries[1]
	if m.entryExpanded(think) {
		t.Errorf("thinking entry should default collapsed")
	}
	if !m.entryExpandable(think) {
		t.Errorf("thinking entry with text should be expandable")
	}
	m.transcript.cursor = 1
	m.setExpanded(1, true)
	if !m.entryExpanded(m.transcript.entries[1]) {
		t.Errorf("thinking entry should be expanded after toggle")
	}
}

func TestFoldKeysExpandAndCollapse(t *testing.T) {
	mm := withVerbose(loaded())
	m := tvOf(&mm)
	m.transcript.cursor = 2 // the expandable thinking entry, inside its run
	if m.entryExpanded(m.transcript.entries[1]) {
		t.Fatal("thinking entry should start collapsed")
	}

	m.handleTranscriptKey(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if !m.entryExpanded(m.transcript.entries[1]) {
		t.Error("l should expand the selected entry")
	}
	m.handleTranscriptKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	if m.entryExpanded(m.transcript.entries[1]) {
		t.Error("h should collapse the selected entry")
	}
}

func TestLayoutEntries(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	lines, first := m.layoutEntries()
	if len(lines) == 0 {
		t.Fatal("layout produced no lines")
	}
	if len(first) != len(m.transcript.entries) {
		t.Fatalf("first map mismatch: %d vs %d entries", len(first), len(m.transcript.entries))
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
	if m.transcript.cursor != len(m.transcript.entries)-1 {
		t.Errorf("clampCursor = %d, want %d", m.transcript.cursor, len(m.transcript.entries)-1)
	}
	m.transcript.cursor = -5
	m.clampCursor()
	if m.transcript.cursor != 0 {
		t.Errorf("clampCursor = %d, want 0", m.transcript.cursor)
	}
}

func TestRestoreEntryCursorByID(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.transcript.cursor = 1
	id, _ := m.currentRowID()

	// Simulate a refresh that prepends an entry, shifting indices.
	m.transcript.entries = append([]transcript.Entry{{ID: "new", Kind: transcript.EntrySystem, Summary: "new"}}, m.transcript.entries...)
	m.restoreEntryCursor(id, false, false)

	if got, _ := m.currentRowID(); got != id {
		t.Errorf("cursor not preserved by id: want %v, got %v", id, got)
	}
}

func TestRestoreEntryCursorKeepsIndexWhenRowGone(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	m.transcript.cursor = 2
	id, _ := m.currentRowID()

	m.transcript.entries = slices.Delete(slices.Clone(m.transcript.entries), 2, 3)
	m.restoreEntryCursor(id, false, false)

	if m.transcript.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (old index)", m.transcript.cursor)
	}
}

func scrollTestView(height int, texts ...string) tview {
	mm := withView(testModel(), viewSession)
	m := tvOf(&mm)
	m.c.m.height = height
	for i, text := range texts {
		m.transcript.entries = append(m.transcript.entries,
			transcript.Entry{ID: fmt.Sprintf("u%d", i), Kind: transcript.EntryUser, Text: text})
	}
	return m
}

func pressScrollKey(m tview, code rune) tview {
	m.handleTranscriptKey(tea.KeyPressMsg{Code: code, Text: string(code)})
	return m
}

func lineScrollTestView() tview {
	m := scrollTestView(14, "a", "b", "c", "d", "e", "f", "g")
	m.transcript.entries[3] = transcript.Entry{ID: "a3", Kind: transcript.EntryText, Text: strings.Repeat("line\n\n", 20)}
	return m
}

func TestLineScrollDownNeverSkipsACard(t *testing.T) {
	m := lineScrollTestView()
	lines, first := m.layoutEntries()
	last := len(first) - 1
	for range 200 {
		cursor, scroll := m.transcript.cursor, m.transcript.scroll
		_, end := itemSpan(cursor, first, len(lines))
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
	lines, first := m.layoutEntries()
	m.transcript.cursor = len(first) - 1
	m.transcript.scroll = m.maxScroll()
	for range 200 {
		cursor, scroll := m.transcript.cursor, m.transcript.scroll
		start, _ := itemSpan(cursor, first, len(lines))
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
	lines, first := m.layoutEntries()
	h := m.viewportHeight()
	cursor := 3
	_, end := itemSpan(cursor, first, len(lines))
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
	m.transcript.entries[1] = transcript.Entry{ID: "a1", Kind: transcript.EntryText, Text: strings.Repeat("line\n\n", 40)}
	lines, first := m.layoutEntries()
	if start, end := itemSpan(1, first, len(lines)); end-start < m.viewportHeight()+6 {
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
	last := len(m.transcript.entries) - 1

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

func TestTranscriptViewRenders(t *testing.T) {
	mm := loaded()
	m := tvOf(&mm)
	out := m.transcriptBody()
	if !strings.Contains(out, "fix the bug") {
		t.Errorf("expected user text in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Opus 4.8") {
		t.Errorf("expected the turn footer's model in output, got:\n%s", out)
	}
}

func TestRenderDetailShell(t *testing.T) {
	m := testModel()
	c := transcript.Entry{
		ID: "sh1", Kind: transcript.EntryShell,
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
	it := transcript.Entry{
		Kind: transcript.EntrySubagent,
		Subagents: []transcript.Subagent{{
			Type:   "default",
			Name:   "Volta",
			Status: "closed",
			Desc:   "the full task message",
		}},
	}
	out := itemRow("", it)
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
	wait := transcript.Entry{
		Kind: transcript.EntrySubagent, ToolName: "wait_agent",
		Subagents: []transcript.Subagent{{ID: "a1", Name: "Volta"}},
	}
	if got := itemRow("", wait); !strings.Contains(got, "Wait Agent: Volta") {
		t.Errorf("wait_agent row = %q, want label 'Wait Agent: Volta'", got)
	}
	closeIt := transcript.Entry{
		Kind: transcript.EntrySubagent, ToolName: "close_agent",
		Subagents: []transcript.Subagent{{ID: "a1", Name: "Volta"}},
	}
	if got := itemRow("", closeIt); !strings.Contains(got, "Close Agent: Volta") {
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
