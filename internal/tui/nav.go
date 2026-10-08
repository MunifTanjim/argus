package tui

import "strings"

// Shared list-navigation helpers: cursor bounds and viewport windowing, used by
// the session and history lists so the math lives in one place.

// cursorUp returns the cursor moved up one, clamped at 0.
func cursorUp(i int) int { return max(0, i-1) }

// cursorDown returns the cursor moved down one, clamped at the last index (n-1).
func cursorDown(i, n int) int { return min(max(0, n-1), i+1) }

// cursorBottom returns the last valid index for n items (0 when empty).
func cursorBottom(n int) int { return max(0, n-1) }

func cursorBy(i, d, n int) int { return max(0, min(i+d, n-1)) }

// windowScroll keeps prev, the first line shown last time, and slides only as
// far as needed to show [curStart, curEnd) in full.
func windowScroll(n, curStart, curEnd, avail, prev int) int {
	if avail <= 0 || n <= avail {
		return 0
	}
	scroll := prev
	if curEnd > scroll+avail {
		scroll = curEnd - avail
	}
	if curStart < scroll {
		scroll = curStart
	}
	return max(0, min(scroll, n-avail))
}

type itemLines struct {
	key   string // names the list in model.listScroll, so its scroll persists
	lines []string
	spans []rowSpan
}

func (l *itemLines) add(item int, block string) {
	start := len(l.lines)
	l.lines = append(l.lines, strings.Split(block, "\n")...)
	l.spans = append(l.spans, rowSpan{index: item, top: start, bottom: len(l.lines)})
}

func (l *itemLines) text(block string) { l.lines = append(l.lines, strings.Split(block, "\n")...) }

func (l itemLines) scroll(c *ctx, cursor, avail int) int {
	start, end := 0, 0
	for _, s := range l.spans {
		if s.index == cursor {
			start, end = s.top, s.bottom
			break
		}
	}
	var saved map[string]int
	if c != nil && c.m != nil {
		saved = c.m.listScroll
	}
	scroll := windowScroll(len(l.lines), start, end, avail, saved[l.key])
	if saved != nil {
		saved[l.key] = scroll
	}
	return scroll
}

// window keeps the cursor item in full view and records the rows it shows.
func (l itemLines) window(c *ctx, cursor, avail int) []string {
	scroll := l.scroll(c, cursor, avail)
	end := len(l.lines)
	if avail > 0 {
		end = min(end, scroll+avail)
	}
	for _, s := range l.spans {
		if top, bottom := max(s.top, scroll), min(s.bottom, end); top < bottom {
			c.hitRows(rowSpan{index: s.index, top: top - scroll, bottom: bottom - scroll})
		}
	}
	return l.lines[scroll:end]
}
