package tui

// Shared list-navigation helpers: cursor bounds and viewport windowing, used by
// the session and history lists so the math lives in one place.

// cursorUp returns the cursor moved up one, clamped at 0.
func cursorUp(i int) int { return max(0, i-1) }

// cursorDown returns the cursor moved down one, clamped at the last index (n-1).
func cursorDown(i, n int) int { return min(max(0, n-1), i+1) }

// cursorBottom returns the last valid index for n items (0 when empty).
func cursorBottom(n int) int { return max(0, n-1) }

// windowSpan returns at most avail lines, scrolled to keep the [curStart, curEnd)
// span fully visible (sliding down only as much as needed). Returns lines unchanged
// when they already fit.
func windowSpan(lines []string, curStart, curEnd, avail int) []string {
	if avail <= 0 || len(lines) <= avail {
		return lines
	}
	scroll := 0
	if curEnd > avail {
		scroll = curEnd - avail
	}
	if curStart < scroll {
		scroll = curStart
	}
	scroll = max(0, min(scroll, len(lines)-avail))
	return lines[scroll : scroll+avail]
}
