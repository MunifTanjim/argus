package tui

import "testing"

// The accent is the reserved focus-highlight color, so no content color may equal
// it — otherwise a focused item is indistinguishable from its own coloring. Task
// (and, via it, Subagent items) used to be the exact accent; guard against a
// regression. Colors are resolved by TestMain's initTheme(true).
func TestFocusAccentIsReserved(t *testing.T) {
	if ColorToolTask == ColorAccent {
		t.Error("ColorToolTask must differ from ColorAccent (reserved for focus)")
	}
}

// The bright focus color must be distinct from the status signals it competes
// with on a card: the awaiting-input accent (blue) and the working/ongoing green.
func TestFocusColorIsDistinct(t *testing.T) {
	if ColorFocus == ColorAccent {
		t.Error("ColorFocus must differ from ColorAccent (awaiting-input)")
	}
	if ColorFocus == ColorOngoing {
		t.Error("ColorFocus must differ from ColorOngoing (working)")
	}
}
