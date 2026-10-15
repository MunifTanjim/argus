package tui

import (
	"image/color"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// heavyLead is how a selected card's top edge starts.
var heavyLead = cardHeavy.tl + cardHeavy.h + " "

// painted reports whether s draws glyph in color c.
func painted(s, glyph string, c color.Color) bool {
	return strings.Contains(s, lipgloss.NewStyle().Foreground(c).Render(glyph))
}

func TestTranscriptCursorDimsWithoutFocus(t *testing.T) {
	m := sessionModel(nil)
	if out := paneView(m); !painted(out, GlyphAccentBarFocused, ColorAccent) {
		t.Fatal("the focused transcript's cursor should be bright")
	}
	for _, k := range []container{leftSidebar, rightSidebar, sessionDock} {
		out := paneView(withFocus(m, k))
		if painted(out, GlyphAccentBarFocused, ColorAccent) || !painted(out, GlyphAccentBarFocused, ColorBorder) {
			t.Errorf("focus on %v: the transcript's cursor should be gray", k)
		}
	}
}

func TestSessionCardSelectionDimsWithoutFocus(t *testing.T) {
	m := homeTestModel()
	s := session.Session{ID: "s1", Status: session.StatusIdle}
	if card := m.sessionCard(s, true, true, 40, false, false); !painted(card, heavyLead, ColorFocus) {
		t.Fatal("a selected card in a focused pane should have the bright border")
	}
	card := m.sessionCard(s, true, false, 40, false, false)
	if painted(card, heavyLead, ColorFocus) || !painted(card, heavyLead, ColorTextDim) {
		t.Fatal("a selected card in an unfocused pane should keep the heavy border, in gray")
	}
	if card := m.sessionCard(s, false, true, 40, false, false); !strings.Contains(card, cardRounded.tl) {
		t.Fatal("an unselected card keeps the light border")
	}
}

func TestHistoryCardSelectionDimsWithoutFocus(t *testing.T) {
	if c, ch := historyCardChrome(true, true); c != ColorFocus || ch != cardHeavy {
		t.Fatal("a selected history card in a focused pane should have the bright heavy border")
	}
	if c, ch := historyCardChrome(true, false); c != ColorTextDim || ch != cardHeavy {
		t.Fatal("a selected history card in an unfocused pane should have the gray heavy border")
	}
}

func TestHomeCardSelectionFollowsFocus(t *testing.T) {
	m := homeTestModel()
	m.width = 160
	main := func(m model) string { return m.mainColumn(&ctx{m: &m}, m.layout()) }
	if !painted(main(m), heavyLead, ColorFocus) {
		t.Fatal("home with focus: the selected card should be bright")
	}
	m = withFocus(m, leftSidebar)
	if out := main(m); painted(out, heavyLead, ColorFocus) || !painted(out, heavyLead, ColorTextDim) {
		t.Fatal("home without focus: the selected card should be gray")
	}
}

func TestMainFocusIsLostToAPopup(t *testing.T) {
	m := sessionModel(nil)
	if !m.mainFocused() {
		t.Fatal("the main pane should hold focus")
	}
	m.popups = append(m.popups, nil)
	if m.mainFocused() {
		t.Fatal("an open popup takes focus from the main pane")
	}
}
