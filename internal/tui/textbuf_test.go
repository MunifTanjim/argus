package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// kp builds a keypress from the name editText matches on.
func kp(name string) tea.KeyPressMsg {
	switch name {
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "delete":
		return tea.KeyPressMsg{Code: tea.KeyDelete}
	}
	r := []rune(name)[0]
	return tea.KeyPressMsg{Code: r, Text: name}
}

func TestEditTextMotionAndEditing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		pos      int
		keys     []string
		wantText string
		wantPos  int
	}{
		{"left moves without editing", "abc", 3, []string{"left"}, "abc", 2},
		{"left stops at the start", "abc", 0, []string{"left"}, "abc", 0},
		{"right stops at the end", "abc", 3, []string{"right"}, "abc", 3},
		{"insert at the cursor", "ac", 1, []string{"b"}, "abc", 2},
		{"backspace deletes before the cursor", "abc", 2, []string{"backspace"}, "ac", 1},
		{"backspace at the start is a no-op", "abc", 0, []string{"backspace"}, "abc", 0},
		{"delete removes under the cursor", "abc", 1, []string{"delete"}, "ac", 1},
		{"delete at the end is a no-op", "abc", 3, []string{"delete"}, "abc", 3},
		{"home then insert", "bc", 2, []string{"home", "a"}, "abc", 1},
		{"end then insert", "ab", 0, []string{"end", "c"}, "abc", 3},
		// A rune is one step, not one byte.
		{"left over a 4-byte rune", "a🚀", 2, []string{"left"}, "a🚀", 1},
		{"backspace over a 4-byte rune", "a🚀", 2, []string{"backspace"}, "a", 1},
		{"insert before a 4-byte rune", "🚀", 0, []string{"x"}, "x🚀", 1},
		// home and end bound the current line, not the whole buffer.
		{"home on line two", "ab\ncd", 5, []string{"home"}, "ab\ncd", 3},
		{"end on line one", "ab\ncd", 0, []string{"end"}, "ab\ncd", 2},
		// A position left behind by a cleared buffer must not panic.
		{"stale position clamps", "ab", 99, []string{"backspace"}, "a", 1},
		{"negative position clamps", "ab", -5, []string{"x"}, "xab", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, pos := tc.in, tc.pos
			for _, k := range tc.keys {
				text, pos, _ = editText(text, pos, kp(k))
			}
			if text != tc.wantText || pos != tc.wantPos {
				t.Fatalf("got (%q, %d), want (%q, %d)", text, pos, tc.wantText, tc.wantPos)
			}
		})
	}
}

func TestEditTextEnterSubmits(t *testing.T) {
	text, pos, submit := editText("abc", 1, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !submit || text != "abc" || pos != 1 {
		t.Fatalf("enter: text=%q pos=%d submit=%v, want unchanged and submit", text, pos, submit)
	}
}

func TestInsertTextAtCursor(t *testing.T) {
	got, pos := insertText("ac", 1, "XY")
	if got != "aXYc" || pos != 3 {
		t.Fatalf("insertText = (%q, %d), want (%q, %d)", got, pos, "aXYc", 3)
	}
}

func TestWithCursorSplices(t *testing.T) {
	if got := withCursor("ab", 1, "|"); got != "a|b" {
		t.Fatalf("withCursor = %q, want %q", got, "a|b")
	}
	// A multi-byte rune must not be split down the middle.
	if got := withCursor("🚀🚀", 1, "|"); got != "🚀|🚀" {
		t.Fatalf("withCursor over runes = %q", got)
	}
}

// The cursor must stay on screen in a value longer than the box.
func TestScrollWindowKeepsCursorVisible(t *testing.T) {
	for _, tc := range []struct {
		name       string
		s          string
		pos, width int
		wantVis    string
		wantPos    int
	}{
		{"short value is untouched", "abc", 3, 10, "abc", 3},
		{"cursor at the head", "abcdefgh", 0, 4, "abcd", 0},
		{"cursor past the window", "abcdefgh", 8, 4, "efgh", 4},
		{"cursor mid value", "abcdefgh", 6, 4, "cdef", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vis, pos := scrollWindow(tc.s, tc.pos, tc.width)
			if vis != tc.wantVis || pos != tc.wantPos {
				t.Fatalf("got (%q, %d), want (%q, %d)", vis, pos, tc.wantVis, tc.wantPos)
			}
			if pos > len([]rune(vis)) {
				t.Fatalf("cursor %d fell outside the window %q", pos, vis)
			}
		})
	}
}

func TestCursorRowCol(t *testing.T) {
	for _, tc := range []struct {
		pos, row, col int
	}{
		{0, 0, 0}, {2, 0, 2}, {3, 1, 0}, {5, 1, 2},
	} {
		row, col := cursorRowCol("ab\ncd", tc.pos)
		if row != tc.row || col != tc.col {
			t.Fatalf("pos %d: got (%d, %d), want (%d, %d)", tc.pos, row, col, tc.row, tc.col)
		}
	}
}

// -- Live buffers --------------------------------------------------------------

// The spawn prompt must insert at the cursor, not always at the end.
func TestSpawnPromptCursorInsert(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)

	for _, k := range []string{"a", "c"} {
		mm, _ = m.handleKey(kp(k))
		m = mm.(model)
	}
	mm, _ = m.handleKey(kp("left"))
	m = mm.(model)
	mm, _ = m.handleKey(kp("b"))
	m = mm.(model)

	if m.spawn.prompt != "abc" || m.spawn.pos != 2 {
		t.Fatalf("prompt=%q pos=%d, want %q at 2", m.spawn.prompt, m.spawn.pos, "abc")
	}
}

// A paste must land at the cursor too, not get appended to the tail.
func TestSpawnPromptPasteAtCursor(t *testing.T) {
	c := &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m := openSpawn(t, c)
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // dir → prompt
	m = mm.(model)

	m.spawn.prompt, m.spawn.pos = "review ", 7
	mm, _ = m.handleKey(kp("!"))
	m = mm.(model)
	mm, _ = m.handleKey(kp("left"))
	m = mm.(model)
	mm, _ = m.Update(tea.PasteMsg{Content: "https://x/pull/1"})
	m = mm.(model)

	if want := "review https://x/pull/1!"; m.spawn.prompt != want {
		t.Fatalf("prompt=%q, want %q", m.spawn.prompt, want)
	}
}

// The custom path is seeded with the fallback, so the cursor starts at the end.
func TestSpawnCustomPathCursorStartsAtEnd(t *testing.T) {
	c := &spawnPickClient{}
	m := openSpawn(t, c) // no history → straight to the custom path
	if !m.spawn.custom {
		t.Fatal("expected the custom path step")
	}
	if m.spawn.pos != endPos(m.spawn.cwd) {
		t.Fatalf("pos=%d, want %d (end of %q)", m.spawn.pos, endPos(m.spawn.cwd), m.spawn.cwd)
	}
	// Backspace must trim the tail, which only holds if the cursor sits there.
	before := m.spawn.cwd
	mm, _ := m.handleKey(kp("backspace"))
	m = mm.(model)
	if want := string([]rune(before)[:len([]rune(before))-1]); m.spawn.cwd != want {
		t.Fatalf("cwd = %q, want %q", m.spawn.cwd, want)
	}
	if m.spawn.pos != endPos(m.spawn.cwd) {
		t.Fatalf("pos = %d, want %d", m.spawn.pos, endPos(m.spawn.cwd))
	}
}

// While a custom answer takes text, left must move the cursor, not switch tabs.
func TestQuestionOtherClaimsCursorKeys(t *testing.T) {
	ix := &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Question: "one", Options: []string{"a"}},
		{Question: "two", Options: []string{"b"}},
	}}
	m := promptModel(ix)
	m.prompt.tab = 1
	m.prompt.sel[1] = otherIndex(&ix.Questions[1]) // highlight "type your own"

	for _, k := range []string{"a", "c"} {
		mm, _ := m.handlePromptKey(kp(k))
		m = mm.(model)
	}
	mm, _ := m.handlePromptKey(kp("left"))
	m = mm.(model)
	if m.prompt.tab != 1 {
		t.Fatalf("left switched tab to %d while editing a custom answer", m.prompt.tab)
	}
	mm, _ = m.handlePromptKey(kp("b"))
	m = mm.(model)
	if m.prompt.text[1] != "abc" {
		t.Fatalf("custom answer = %q, want %q", m.prompt.text[1], "abc")
	}

	// Move off the custom row: left goes back to being a tab key.
	m.prompt.sel[1] = 0
	mm, _ = m.handlePromptKey(kp("left"))
	m = mm.(model)
	if m.prompt.tab != 0 {
		t.Fatalf("tab = %d, want 0 (left is a tab key when not editing)", m.prompt.tab)
	}
}

// The deny reason takes cursor keys; up and down still pick Allow or Deny.
func TestDenyReasonCursorAndSelection(t *testing.T) {
	m := promptModel(&session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Bash",
		Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"},
			{Label: "Deny", Value: "deny", Reject: true},
		},
	})
	mm, _ := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyDown}) // select Deny
	m = mm.(model)

	for _, k := range []string{"a", "c"} {
		mm, _ = m.handlePromptKey(kp(k))
		m = mm.(model)
	}
	mm, _ = m.handlePromptKey(kp("left"))
	m = mm.(model)
	mm, _ = m.handlePromptKey(kp("b"))
	m = mm.(model)
	if m.prompt.reasonText != "abc" {
		t.Fatalf("reason = %q, want %q", m.prompt.reasonText, "abc")
	}
	if m.prompt.decisionSel != 1 {
		t.Fatalf("decisionSel = %d, want 1 (cursor keys must not change it)", m.prompt.decisionSel)
	}

	// Up still moves the choice, which also hides the reason field.
	mm, _ = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyUp})
	m = mm.(model)
	if m.prompt.decisionSel != 0 {
		t.Fatalf("decisionSel = %d, want 0", m.prompt.decisionSel)
	}
}

// The idle composer inserts at the cursor and clears its position on submit.
func TestIdleComposerCursor(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	for _, k := range []string{"a", "c"} {
		mm, _ := m.handlePromptKey(kp(k))
		m = mm.(model)
	}
	mm, _ := m.handlePromptKey(kp("left"))
	m = mm.(model)
	mm, _ = m.handlePromptKey(kp("b"))
	m = mm.(model)
	if m.prompt.reasonText != "abc" || m.prompt.reasonPos != 2 {
		t.Fatalf("reason=%q pos=%d, want %q at 2", m.prompt.reasonText, m.prompt.reasonPos, "abc")
	}

	mm, _ = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(model)
	if m.prompt.reasonText != "" || m.prompt.reasonPos != 0 {
		t.Fatalf("after submit: reason=%q pos=%d, want empty at 0", m.prompt.reasonText, m.prompt.reasonPos)
	}
}
