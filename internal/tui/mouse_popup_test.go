package tui

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func popupRow(t *testing.T, m model, i int) (x, y int) {
	t.Helper()
	return itemCell(t, m, regPopup, i)
}

func TestPaletteClickSelectsThenRuns(t *testing.T) {
	m := withMouse(homeTestModel())
	m, _ = m.openPalette()
	p := m.popups.front().(palettePopup)
	if len(p.matches) < 2 {
		t.Fatal("the palette needs two matches for this test")
	}
	x, y := popupRow(t, m, 1)
	m, _ = click(m, x, y)
	if p := m.popups.front().(palettePopup); p.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", p.cursor)
	}
	m, _ = click(m, x, y)
	if len(m.popups) != 0 {
		t.Error("a second click must run the match and close the palette")
	}
}

func TestPaletteWheelMovesCursor(t *testing.T) {
	m := withMouse(homeTestModel())
	m, _ = m.openPalette()
	x, y := popupRow(t, m, 0)
	m, _ = wheelAt(m, x, y, 1)
	if p := m.popups.front().(palettePopup); p.cursor != 1 {
		t.Errorf("cursor = %d, want 1", p.cursor)
	}
}

func TestRetargetClickPicksBranch(t *testing.T) {
	m := withMouse(wideWorkspace())
	r := retargetPicker{workspaceID: "n1:w1", projectID: "n1:p1", label: "w1", pick: newBranchPicker()}
	r.pick.load([]api.BranchInfo{{Name: "main", Local: true}, {Name: "dev", Local: true}}, nil)
	m = withPopup(m, r)
	x, y := popupRow(t, m, 1)
	m, _ = click(m, x, y)
	if p := m.popups.front().(retargetPicker); p.pick.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", p.pick.cursor)
	}
	m, _ = click(m, x, y)
	if len(m.popups) != 0 {
		t.Error("a second click must pick the branch and close the picker")
	}
}

func TestCreatePickerTabClick(t *testing.T) {
	m := withMouse(wideWorkspace())
	m = openCreate(m, "n1:p1")
	y, x := findBlock(t, m, createTabNames[ctBranches])
	m, _ = click(m, x, y)
	if p, _ := m.frontCreate(); p.tab != ctBranches {
		t.Errorf("tab = %v, want the branches tab", p.tab)
	}
}
