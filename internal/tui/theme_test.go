package tui

import "testing"

// The accent is reserved for focus, so no content color may equal it.
// Regression: Task (and Subagent via it) used to be the exact accent.
func TestFocusAccentIsReserved(t *testing.T) {
	if ColorToolTask == ColorAccent {
		t.Error("ColorToolTask must differ from ColorAccent (reserved for focus)")
	}
}

// ColorFocus must differ from the card status signals it competes with:
// the awaiting-input accent (blue) and the working/ongoing green.
func TestFocusColorIsDistinct(t *testing.T) {
	if ColorFocus == ColorAccent {
		t.Error("ColorFocus must differ from ColorAccent (awaiting-input)")
	}
	if ColorFocus == ColorOngoing {
		t.Error("ColorFocus must differ from ColorOngoing (working)")
	}
}
