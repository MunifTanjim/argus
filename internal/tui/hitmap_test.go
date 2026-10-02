package tui

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestHitAreaZoneWinsOverRow(t *testing.T) {
	var h hitMap
	a := h.add(regMain, uv.Rect(10, 2, 20, 10))
	c := &ctx{area: a}
	c.below(1).hitRows(rowSpan{index: 3, top: 0, bottom: 2})
	c.hitZone(uv.Rect(0, 1, 4, 1), hitTarget{kind: hitTab, index: 1})

	if got, ok := a.target(11, 3); !ok || got != (hitTarget{kind: hitTab, index: 1}) {
		t.Errorf("zone cell: got %v %v", got, ok)
	}
	if got, ok := a.target(20, 4); !ok || got != (hitTarget{kind: hitRow, index: 3}) {
		t.Errorf("row cell: got %v %v", got, ok)
	}
	if _, ok := a.target(20, 9); ok {
		t.Error("a cell below the rows has no target")
	}
}

func TestHitMapLaterAreaWinsAndResetClears(t *testing.T) {
	var h hitMap
	h.add(regMain, uv.Rect(0, 0, 50, 20))
	h.add(regPopup, uv.Rect(10, 5, 10, 5))
	if a, ok := h.at(12, 6); !ok || a.region != regPopup {
		t.Errorf("got %v, want the popup over the pane", a)
	}
	if a, ok := h.at(1, 1); !ok || a.region != regMain {
		t.Errorf("got %v, want the pane", a)
	}
	h.reset()
	if _, ok := h.at(1, 1); ok {
		t.Error("reset must clear the areas")
	}
}

func TestNilHitMapRecordsNothing(t *testing.T) {
	var h *hitMap
	h.reset()
	c := &ctx{area: h.add(regMain, uv.Rect(0, 0, 5, 5))}
	c.hitRows(rowSpan{index: 0, top: 0, bottom: 1})
	if _, ok := h.at(0, 0); ok || c.recording() {
		t.Error("a nil hit map takes no areas")
	}
}

func TestItemLinesWindowRecordsVisibleRows(t *testing.T) {
	var h hitMap
	c := &ctx{area: h.add(regMain, uv.Rect(0, 0, 10, 10))}
	var l itemLines
	for i := range 5 {
		if i > 0 {
			l.text("")
		}
		l.add(i, "a\nb")
	}
	got := l.window(c.below(2), 4, 4)
	if len(got) != 4 {
		t.Fatalf("got %d lines, want 4", len(got))
	}
	want := []rowSpan{{index: 3, top: 2, bottom: 3}, {index: 4, top: 4, bottom: 6}}
	if a := h.areas[0]; len(a.rows) != 2 || a.rows[0] != want[0] || a.rows[1] != want[1] {
		t.Errorf("rows = %v, want %v", h.areas[0].rows, want)
	}
}
