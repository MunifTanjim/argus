package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxWordSkip bounds the retries after a match is rejected for lacking
	// a word boundary: a word is not searched for again once the search
	// position has moved this far. The first search accepts a match anywhere
	// ahead.
	maxWordSkip = 256
	// maxWrapGap is the most source text a wrap can stand for (markup such as
	// "**" or "`"); a longer gap is something the renderer dropped, so the
	// break stays.
	maxWrapGap = 16
)

// rejoin is a heuristic for visual-mode copy, not a general text aligner.
//
// rejoin joins one card's selected rows into copy text. A break between rows
// is a wrap when the card's source has no newline between the last word of
// one row and the first word of the next: a space there joins with a space,
// nothing there (a word split mid-way) joins with nothing. Anything it cannot
// line up keeps its break.
func rejoin(rows []string, source string) string {
	for len(rows) > 0 && strings.TrimSpace(rows[0]) == "" {
		rows = rows[1:] // padding above a card's text, such as the prompt band's
	}
	var b strings.Builder
	cur, prevEnd := 0, -1
	for i, row := range rows {
		row = strings.TrimRight(row, " ")
		start, end, next := matchWords(source, cur, strings.Fields(row))
		if i > 0 {
			sep := "\n"
			if prevEnd >= 0 && start >= prevEnd {
				sep = wrapSep(source[prevEnd:start])
			}
			if sep != "\n" {
				row = strings.TrimLeft(row, " ")
			}
			b.WriteString(sep)
		}
		b.WriteString(row)
		cur, prevEnd = next, end
	}
	return strings.TrimRight(b.String(), "\n ")
}

// matchWords finds words in order in source from cur. start is where the
// first word is (-1 if it is not found), end is just past the last word (-1
// if it is not found), and next is the cursor for the following row. Each
// word's search starts from next (just past the previous match in this row)
// and takes the first match anywhere ahead. The first word uses plain
// substring matching (for mid-word hard wraps like "abcdef"/"ghij" against
// "abcdefghij"). Later words (i > 0) require a word boundary before the match
// (preceding rune is absent or not letter/digit/underscore); a match without
// one is rejected and the search retries past it, until the search position
// is maxWordSkip past next. A word with no valid match is skipped and matching
// continues with the next word from the same starting position.
func matchWords(source string, cur int, words []string) (start, end, next int) {
	start, end, next = -1, -1, cur
	for i, w := range words {
		pos := next
		// Search for this word within maxWordSkip distance from pos
		for pos-next < maxWordSkip {
			j := strings.Index(source[pos:], w)
			if j < 0 {
				break
			}
			matchPos := pos + j
			// First word (i==0): allow substring match anywhere
			// Later words: require word boundary before the match
			if i > 0 && !hasWordBoundaryBefore(source, matchPos) {
				pos = matchPos + 1
				continue
			}
			// Found a match
			if i == 0 {
				start = matchPos
			}
			next = matchPos + len(w)
			if i == len(words)-1 {
				end = next
			}
			break
		}
		// If word not found, continue with next word from same starting position
	}
	return start, end, next
}

// hasWordBoundaryBefore reports whether there is a word boundary before pos.
func hasWordBoundaryBefore(source string, pos int) bool {
	if pos == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(source[:pos])
	return !isWordChar(r)
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// wrapSep is what a row break stands for, given the source between the words
// on either side of it.
func wrapSep(gap string) string {
	switch {
	case strings.Contains(gap, "\n") || len(gap) > maxWrapGap:
		return "\n"
	case strings.ContainsAny(gap, " \t"):
		return " "
	}
	return ""
}
