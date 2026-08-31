package tui

import (
	tea "charm.land/bubbletea/v2"
)

// The hand-rolled text boxes (spawn prompt, spawn path, idle composer, deny
// reason, custom answer, redact input) share this editor. Each box keeps a
// plain string plus a cursor position counted in runes, never in bytes.
//
// ponytail: character motion only — no word jumps, no kill-to-end, no undo.
// bubbles/textinput has all of that, but its key map claims enter, tab, up,
// down and ctrl+u, which the prompt router already assigns to submit, leave,
// select and scroll. Swap to the widget only if a box stops sharing the
// router's keys.

// clampPos keeps pos inside s. Buffers are cleared and reseeded from several
// places (submit, cancel, a new interaction), so a stale position is normal;
// clamping absorbs it instead of panicking on the next keystroke.
func clampPos(s string, pos int) int {
	return min(max(pos, 0), len([]rune(s)))
}

// endPos is the position that puts the cursor after the last rune, for a buffer
// seeded with a value the user is expected to edit from the end.
func endPos(s string) int { return len([]rune(s)) }

// insertText splices ins into cur at pos and returns the new text and position.
// Typed runes, pasted blocks and inserted newlines all take this path.
func insertText(cur string, pos int, ins string) (string, int) {
	r, add := []rune(cur), []rune(ins)
	pos = clampPos(cur, pos)
	out := make([]rune, 0, len(r)+len(add))
	out = append(out, r[:pos]...)
	out = append(out, add...)
	out = append(out, r[pos:]...)
	return string(out), pos + len(add)
}

// editText applies a keypress to a free-text buffer. It returns the new text,
// the new cursor position, and whether Enter (submit) was pressed. Callers that
// give Enter another meaning handle it before calling.
func editText(cur string, pos int, msg tea.KeyPressMsg) (string, int, bool) {
	r := []rune(cur)
	pos = clampPos(cur, pos)
	switch msg.String() {
	case "enter":
		return cur, pos, true
	case "backspace":
		if pos == 0 {
			return cur, pos, false
		}
		return string(append(r[:pos-1:pos-1], r[pos:]...)), pos - 1, false
	case "delete":
		if pos >= len(r) {
			return cur, pos, false
		}
		return string(append(r[:pos:pos], r[pos+1:]...)), pos, false
	case "left", "ctrl+b":
		return cur, max(0, pos-1), false
	case "right", "ctrl+f":
		return cur, min(len(r), pos+1), false
	case "home", "ctrl+a":
		return cur, lineStart(r, pos), false
	case "end", "ctrl+e":
		return cur, lineEnd(r, pos), false
	}
	if msg.Text != "" {
		s, p := insertText(cur, pos, msg.Text)
		return s, p, false
	}
	return cur, pos, false
}

// isTextMotion reports whether a key moves the cursor within a buffer. A box
// that is currently taking text must claim these before the surrounding widget
// reads them as navigation (left/right also switch question tabs).
func isTextMotion(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "left", "right", "home", "end", "ctrl+a", "ctrl+b", "ctrl+e", "ctrl+f":
		return true
	}
	return false
}

// lineStart and lineEnd bound the line holding pos, so home and end behave on a
// multi-line composer the way they do in a single-line field.
func lineStart(r []rune, pos int) int {
	for i := pos - 1; i >= 0; i-- {
		if r[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

func lineEnd(r []rune, pos int) int {
	for i := pos; i < len(r); i++ {
		if r[i] == '\n' {
			return i
		}
	}
	return len(r)
}

// cursorRowCol maps a buffer position to its line index and its rune column
// within that line, for views that render a buffer line by line.
func cursorRowCol(s string, pos int) (row, col int) {
	r := []rune(s)
	pos = clampPos(s, pos)
	for i := 0; i < pos; i++ {
		if r[i] == '\n' {
			row, col = row+1, 0
			continue
		}
		col++
	}
	return row, col
}

// withCursor splices the cursor glyph into s at pos.
func withCursor(s string, pos int, glyph string) string {
	r := []rune(s)
	pos = clampPos(s, pos)
	return string(r[:pos]) + glyph + string(r[pos:])
}

// scrollWindow slides a width-wide window over a single line so the cursor
// stays on screen in a box that truncates rather than wraps. It returns the
// visible slice and pos rebased into it.
func scrollWindow(s string, pos, width int) (string, int) {
	r := []rune(s)
	pos = clampPos(s, pos)
	if width < 1 || len(r) <= width {
		return s, pos
	}
	start := max(0, pos-width)
	return string(r[start:min(start+width, len(r))]), pos - start
}
