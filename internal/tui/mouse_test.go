package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func withMouse(m model) model {
	m.mouse = true
	m.hits = &hitMap{}
	return m
}

func click(m model, x, y int) (model, tea.Cmd) {
	m.View()
	return upd(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func wheelAt(m model, x, y, d int) (model, tea.Cmd) {
	m.View()
	return upd(m, wheelMsg{Mouse: tea.Mouse{X: x, Y: y}, delta: d})
}

// itemCell is a screen cell on item i in region r of the last frame.
func itemCell(t *testing.T, m model, r region, i int) (x, y int) {
	t.Helper()
	m.View()
	for _, a := range m.hits.areas {
		if a.region != r {
			continue
		}
		for _, s := range a.rows {
			if s.index == i {
				return a.rect.Min.X + 1, a.rect.Min.Y + s.top
			}
		}
	}
	t.Fatalf("item %d is not in region %d", i, r)
	return 0, 0
}

func TestMouseModeFollowsTheToggle(t *testing.T) {
	m := homeTestModel()
	if m.View().MouseMode != tea.MouseModeNone {
		t.Error("with the mouse off, Home must leave the mouse to the terminal")
	}
	m = withMouse(m)
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("with the mouse on, Home must capture the mouse")
	}
}

func TestToggleMouseCommand(t *testing.T) {
	m := withMouse(homeTestModel())
	m, _ = upd(m, cmdMsg(projectsKeys.ToggleMouse.name))
	if m.mouse || m.flash != "mouse off" {
		t.Fatalf("mouse=%v flash=%q, want off", m.mouse, m.flash)
	}
	m, _ = upd(m, cmdMsg(projectsKeys.ToggleMouse.name))
	if !m.mouse || m.flash != "mouse on" {
		t.Fatalf("mouse=%v flash=%q, want on", m.mouse, m.flash)
	}
}

func TestFrameRecordsRegions(t *testing.T) {
	m := withMouse(wideWorkspace())
	m.View()
	got := map[region]bool{}
	for _, a := range m.hits.areas {
		got[a.region] = true
	}
	for _, r := range []region{regTree, regTreeDivider, regMain, regRight, regFilesDivider} {
		if !got[r] {
			t.Errorf("region %d is missing", r)
		}
	}
}

func TestClickFocusesTreeWithoutFollow(t *testing.T) {
	m := withMouse(wideWorkspace())
	m = withFocus(m, mainPane)
	m.left.tree.cursor = 0
	m, _ = click(m, 1, m.height-4)
	if m.focused != leftSidebar {
		t.Fatalf("focused = %v, want the tree", m.focused)
	}
	if m.left.tree.cursor != 0 {
		t.Errorf("tree cursor = %d; a click must not move it to the main row", m.left.tree.cursor)
	}
}

func TestClickFocusesMainPane(t *testing.T) {
	m := withMouse(wideWorkspace())
	m, _ = click(m, m.mainRect().Min.X+1, 20)
	if m.focused != mainPane {
		t.Fatalf("focused = %v, want the main pane", m.focused)
	}
}

func TestPopupBlocksClick(t *testing.T) {
	m := withMouse(wideWorkspace())
	m = withPopup(m, fakePopup{name: "p"})
	m, _ = click(m, 1, 20)
	if m.focused != leftSidebar || len(m.popups) != 1 {
		t.Errorf("focused=%v popups=%d; a click under a popup must do nothing", m.focused, len(m.popups))
	}
}

func TestPopupBlocksWheel(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, fakePopup{name: "p"})
	m, _ = wheelAt(m, m.mainRect().Min.X+1, 10, 2)
	if h := m.main.top().(homeComp); h.cursor != 0 {
		t.Errorf("cursor = %d; the wheel under a popup must do nothing", h.cursor)
	}
}

func TestClickClosesHelp(t *testing.T) {
	m := withMouse(homeTestModel())
	m.showHelp = true
	m, _ = click(m, 5, 5)
	if m.showHelp {
		t.Error("a click must close the help")
	}
}

func TestFooterPromptDropsClick(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withHome(m, homeComp{killID: "n1:s1"})
	m, _ = click(m, 1, 20)
	if m.focused != mainPane {
		t.Errorf("focused = %v; a click during a prompt must do nothing", m.focused)
	}
}

func TestMouseOffClickLeavesHelp(t *testing.T) {
	m := withMouse(homeTestModel())
	m.mouse = false
	m.showHelp = true
	m, _ = click(m, 5, 5)
	if !m.showHelp {
		t.Error("with the mouse off a click must not close the help")
	}
}

func TestClickInDockFocusesDock(t *testing.T) {
	m := withMouse(sessionModel(dockPermission))
	m = withInteraction(m, dockPermission)
	m.View()
	var x, y int
	found := false
	for _, a := range m.hits.areas {
		if a.region == regDock {
			x, y, found = a.rect.Min.X+1, a.rect.Min.Y+1, true
		}
	}
	if !found {
		t.Fatal("no dock region in the frame")
	}
	m, _ = click(m, x, y)
	if m.focused != sessionDock {
		t.Errorf("focused = %v; a click in the dock must focus it", m.focused)
	}
}

func TestFooterPromptDropsWheel(t *testing.T) {
	m := withMouse(homeTestModel())
	x := m.mainRect().Min.X + 1
	if next, _ := wheelAt(m, x, 10, 1); homeOf(next).cursor != 1 {
		t.Fatalf("cursor = %d; without a prompt the wheel must move it", homeOf(next).cursor)
	}
	m = withHome(m, homeComp{killID: "n1:s1"})
	next, _ := wheelAt(m, x, 10, 1)
	if homeOf(next).cursor != 0 {
		t.Errorf("cursor = %d; a wheel during a prompt must do nothing", homeOf(next).cursor)
	}
}

func TestFooterPromptDropsTreeWheel(t *testing.T) {
	m := withMouse(homeTestModel())
	if next, _ := wheelAt(m, 1, 5, 1); next.left.tree.cursor != 1 {
		t.Fatalf("tree cursor = %d; without a prompt the wheel must move it", next.left.tree.cursor)
	}
	m = withHome(m, homeComp{killID: "n1:s1"})
	next, _ := wheelAt(m, 1, 5, 1)
	if next.left.tree.cursor != 0 {
		t.Errorf("tree cursor = %d; a wheel over the tree during a prompt must do nothing", next.left.tree.cursor)
	}
}

func TestClickClearsPendingKeys(t *testing.T) {
	m := withMouse(homeTestModel())
	m.keyBuf = []tea.KeyPressMsg{keyMsg("g")}
	m, _ = click(m, 1, 20)
	if len(m.keyBuf) != 0 {
		t.Error("a click must clear a pending key sequence")
	}
}

func TestMouseOffDropsEvents(t *testing.T) {
	m := withMouse(homeTestModel())
	m.mouse = false
	m, _ = click(m, 1, 20)
	if m.focused != mainPane {
		t.Errorf("focused = %v; with the mouse off a click must do nothing", m.focused)
	}
}

const dragRow = 10

func glyphAt(m model, x int) string { return ansi.Cut(frameLines(m)[dragRow], x, x+1) }

func drag(m model, from, to int) model {
	m, _ = click(m, from, dragRow)
	m, _ = upd(m, tea.MouseMotionMsg{X: to, Y: dragRow, Button: tea.MouseLeft})
	m, _ = upd(m, tea.MouseReleaseMsg{X: to, Y: dragRow, Button: tea.MouseLeft})
	return m
}

func TestDragTreeDivider(t *testing.T) {
	m := withMouse(wideWorkspace())
	w := m.projectsLeftW()
	x := w + screenMargin + 1
	if g := glyphAt(m, x); g != "│" {
		t.Fatalf("glyph at %d = %q, want the divider", x, g)
	}
	m = drag(m, x, x+6)
	if got := m.projectsLeftW(); got != w+6 {
		t.Fatalf("tree width = %d, want %d", got, w+6)
	}
	if g := glyphAt(m, x+6); g != "│" {
		t.Errorf("the divider is not under the pointer after the drag")
	}
	if m.drag != dragNone {
		t.Error("a release must end the drag")
	}
}

func TestDragTreeDividerClamps(t *testing.T) {
	m := withMouse(wideWorkspace())
	x := m.projectsLeftW() + screenMargin + 1
	if m = drag(m, x, 0); m.projectsLeftW() != 20 {
		t.Errorf("width = %d, want the minimum 20", m.projectsLeftW())
	}
	x = m.projectsLeftW() + screenMargin + 1
	if m = drag(m, x, m.width-1); m.projectsLeftW() != m.leftMaxW() {
		t.Errorf("width = %d, want the maximum %d", m.projectsLeftW(), m.leftMaxW())
	}
}

func TestDragFilesDivider(t *testing.T) {
	m := withMouse(wideWorkspace())
	w := m.projectsFilesW()
	x := m.width - w - 4
	if g := glyphAt(m, x); g != "│" {
		t.Fatalf("glyph at %d = %q, want the divider", x, g)
	}
	m = drag(m, x, x-5)
	if got := m.projectsFilesW(); got != w+5 {
		t.Fatalf("files width = %d, want %d", got, w+5)
	}
	if g := glyphAt(m, x-5); g != "│" {
		t.Errorf("the divider is not under the pointer after the drag")
	}
}

func TestDragStopsWhenSidebarHides(t *testing.T) {
	m := withMouse(wideWorkspace())
	x := m.projectsLeftW() + screenMargin + 1
	m, _ = click(m, x, dragRow)
	m.left.hidden = true
	before := m.left.width
	m, _ = upd(m, tea.MouseMotionMsg{X: x + 6, Y: dragRow, Button: tea.MouseLeft})
	if m.drag != dragNone || m.left.width != before {
		t.Errorf("drag=%v width=%d, want %d; a hidden sidebar must end the drag", m.drag, m.left.width, before)
	}
}

func TestReleaseAppliesFinalPosition(t *testing.T) {
	m := withMouse(wideWorkspace())
	w := m.projectsLeftW()
	x := w + screenMargin + 1
	m, _ = click(m, x, dragRow)
	m, _ = upd(m, tea.MouseReleaseMsg{X: x + 4, Y: dragRow, Button: tea.MouseLeft})
	if got := m.projectsLeftW(); got != w+4 {
		t.Errorf("tree width = %d, want %d", got, w+4)
	}
	if m.drag != dragNone {
		t.Error("a release must end the drag")
	}
}

func TestDividerClickStartsDragAndReleaseEndsIt(t *testing.T) {
	m := withMouse(wideWorkspace())
	cases := []struct {
		name string
		x    int
		want dragTarget
	}{
		{"tree", m.projectsLeftW() + screenMargin + 1, dragTree},
		{"files", m.width - m.projectsFilesW() - 4, dragFiles},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := click(m, tc.x, dragRow)
			if m.drag != tc.want {
				t.Fatalf("drag = %v, want %v", m.drag, tc.want)
			}
			m, _ = upd(m, tea.MouseReleaseMsg{X: tc.x, Y: dragRow, Button: tea.MouseLeft})
			if m.drag != dragNone {
				t.Errorf("drag = %v after release, want none", m.drag)
			}
		})
	}
}

func titleIconCell(t *testing.T, m model, icon int) (x, y int) {
	t.Helper()
	m.View()
	for _, a := range m.hits.areas {
		if a.region != regTitle {
			continue
		}
		for _, z := range a.zones {
			if z.target.index == icon {
				return a.rect.Min.X + z.rect.Min.X, a.rect.Min.Y + z.rect.Min.Y
			}
		}
	}
	t.Fatalf("title icon %d is not in the frame", icon)
	return 0, 0
}

func TestTitleIconsToggleSidebars(t *testing.T) {
	m := withFocus(withMouse(wideWorkspace()), mainPane)
	x, y := titleIconCell(t, m, treeIcon)
	m, _ = click(m, x, y)
	if !m.left.hidden || m.focused != mainPane {
		t.Fatalf("left hidden=%v focused=%v, want the tree hidden", m.left.hidden, m.focused)
	}
	x, y = titleIconCell(t, m, treeIcon)
	m, _ = click(m, x, y)
	if m.left.hidden || m.focused != leftSidebar {
		t.Fatalf("left hidden=%v focused=%v, want the tree shown and focused", m.left.hidden, m.focused)
	}
	x, y = titleIconCell(t, m, filesIcon)
	m, _ = click(m, x, y)
	if !m.right.hidden {
		t.Fatal("the files icon should hide the right sidebar")
	}
	x, y = titleIconCell(t, m, filesIcon)
	m, _ = click(m, x, y)
	if m.right.hidden || m.focused != rightSidebar {
		t.Fatalf("right hidden=%v focused=%v, want the right sidebar shown and focused", m.right.hidden, m.focused)
	}
}

func TestTitleIconsShowSidebarState(t *testing.T) {
	m := wideWorkspace()
	title := func() string { return strings.Split(ansi.Strip(m.View().Content), "\n")[0] }
	if got := title(); !strings.HasSuffix(got, glyphSidebarLeft+" "+glyphSidebarRight) {
		t.Errorf("title = %q, want both sidebars shown", got)
	}
	m.left.hidden, m.right.hidden = true, true
	if got := title(); !strings.HasSuffix(got, glyphSidebarLeftOff+" "+glyphSidebarRightOff) {
		t.Errorf("title = %q, want both sidebars hidden", got)
	}
}

func TestViewerDrawsNoTitleIcons(t *testing.T) {
	m := withMouse(historyTranscript(true))
	m.width = 160
	out := m.View().Content
	for _, g := range []string{glyphSidebarLeft, glyphSidebarLeftOff, glyphSidebarRight, glyphSidebarRightOff} {
		if strings.Contains(out, g) {
			t.Errorf("the viewer should draw no sidebar icon, found %q", g)
		}
	}
	for _, a := range m.hits.areas {
		if a.region == regTitle {
			t.Error("the viewer should record no title icons")
		}
	}
}

func TestTitleIconsToggleSidebarsOverTheLiveScreen(t *testing.T) {
	m := withMouse(liveScreenModelWith(func(m *model) { m.width = 160 }))
	x, y := titleIconCell(t, m, treeIcon)
	m, _ = click(m, x, y)
	if !m.left.hidden {
		t.Fatal("the tree icon should hide the tree over the live screen")
	}
	x, y = titleIconCell(t, m, treeIcon)
	m, _ = click(m, x, y)
	if m.left.hidden || m.focused != mainPane || m.topScreen() < 0 {
		t.Fatalf("left hidden=%v focused=%v, want the tree shown and the live screen kept", m.left.hidden, m.focused)
	}
}
