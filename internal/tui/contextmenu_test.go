package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/MunifTanjim/argus/internal/session"
)

// killable gives every session a tmux pane, so that a kill is not refused.
func killable(m model) model {
	for id, s := range m.sessions {
		s.Tmux.PaneID = "%" + id
		m.sessions[id] = s
	}
	return m
}

func TestRunNamedSkipsHistory(t *testing.T) {
	m := killable(homeTestModel())
	c := &ctx{m: &m}
	c.runNamed(listKeys.Kill.name)
	m.apply(c)
	if h := m.main.top().(homeComp); h.killID != m.order[0] {
		t.Fatalf("killID = %q, want the kill prompt for %q", h.killID, m.order[0])
	}
	if len(m.cmdHistory) != 0 {
		t.Errorf("history = %v, want empty", m.cmdHistory)
	}
}

func killMenu(m model, at uv.Position) contextMenu {
	return contextMenu{at: at, screen: m.screen(), entries: []binding{listKeys.Jump, listKeys.Kill}}
}

func menuNames(p contextMenu) []string {
	out := make([]string, len(p.entries))
	for i, b := range p.entries {
		out[i] = b.name
	}
	return out
}

func TestContextMenuKeys(t *testing.T) {
	m := withMouse(killable(homeTestModel()))
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	m, _ = upd(m, keyMsg("j"))
	if p := m.popups.front().(contextMenu); p.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", p.cursor)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.popups) != 0 {
		t.Fatal("enter must close the menu")
	}
	if h := m.main.top().(homeComp); h.killID == "" {
		t.Error("enter must run the entry under the cursor")
	}
	if len(m.cmdHistory) != 0 {
		t.Errorf("history = %v, want empty", m.cmdHistory)
	}
}

func TestContextMenuEscCloses(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(m.popups) != 0 {
		t.Error("esc must close the menu")
	}
}

func TestContextMenuDrawsAtThePointer(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	y, x := findBlock(t, m, "open tmux-pane")
	if y != 6 || x != 12 {
		t.Errorf("first entry at (%d,%d), want (12,6): inside the border and padding of a box at (10,5)", x, y)
	}
}

func TestContextMenuStaysOnScreen(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(m.width-1, m.height-1)))
	m.View()
	found := false
	for _, a := range m.hits.areas {
		if a.region != regPopup {
			continue
		}
		found = true
		if a.rect.Empty() || a.rect.Max.X > m.width || a.rect.Max.Y > m.height {
			t.Errorf("menu rect %v leaves the %dx%d screen", a.rect, m.width, m.height)
		}
	}
	if !found {
		t.Fatal("the menu recorded no area")
	}
}

func TestContextMenuClickRunsEntry(t *testing.T) {
	m := withMouse(killable(homeTestModel()))
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	x, y := itemCell(t, m, regPopup, 1)
	m, _ = click(m, x, y)
	if len(m.popups) != 0 || m.main.top().(homeComp).killID == "" {
		t.Errorf("popups=%d; a left click on an entry must close the menu and run it", len(m.popups))
	}
}

func TestContextMenuWheel(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	x, y := itemCell(t, m, regPopup, 0)
	m, _ = wheelAt(m, x, y, 3)
	if p := m.popups.front().(contextMenu); p.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (clamped)", p.cursor)
	}
}

func rightClick(m model, x, y int) (model, tea.Cmd) {
	m.View()
	return upd(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
}

func wantMenu(t *testing.T, m model, want ...string) {
	t.Helper()
	p, ok := m.popups.front().(contextMenu)
	if !ok {
		t.Fatalf("front popup = %T, want a context menu", m.popups.front())
	}
	if got := menuNames(p); !slices.Equal(got, want) {
		t.Errorf("menu = %v, want %v", got, want)
	}
}

func TestMenuOnHomeSessionCard(t *testing.T) {
	m := withMouse(homeTestModel())
	x, y := itemCell(t, m, regMain, 1)
	m, _ = rightClick(m, x, y)
	wantMenu(t, m, "open tmux-pane", "session kill")
}

func TestMenuOnWorkspaceSessionCard(t *testing.T) {
	m := withMouse(wideWorkspace())
	x, y := itemCell(t, m, regMain, 0)
	m, _ = rightClick(m, x, y)
	wantMenu(t, m, "open tmux-pane", "session kill")
}

func TestMenuOnHistorySessionCard(t *testing.T) {
	m := withMouse(homeTestModel())
	p := session.HistoryProject{Label: "a", Cwd: "/a", NodeID: "n1", ProjectDir: "a"}
	m = withHistorySessions(m, p, session.HistorySessionPage{Items: []session.HistorySession{
		{SessionID: "x", Resumable: true}, {SessionID: "y", Resumable: true},
	}})
	x, y := itemCell(t, m, regMain, 1)
	m, _ = rightClick(m, x, y)
	wantMenu(t, m, "session resume", "transcript export")
}

func TestMenuOnTreeRows(t *testing.T) {
	m := withMouse(homeTestModel())
	x, y := itemCell(t, m, regTree, 1)
	mm, _ := rightClick(m, x, y)
	wantMenu(t, mm, "session spawn", "workspace new", "project rename", "project pin", "project hide", "project forget")

	x, y = itemCell(t, m, regTree, 3)
	mm, _ = rightClick(m, x, y)
	wantMenu(t, mm, "session spawn", "workspace change-target", "workspace rerun-setup", "open setup-log", "workspace remove")
}

func TestMenuOnFileView(t *testing.T) {
	for _, diff := range []bool{false, true} {
		m := withMouse(homeTestModel())
		m = withView(m, viewSession)
		m = withFile(m, fileComp{ws: "n1:w1", path: "a.go", diff: diff, lines: strings.Split(strings.Repeat("x\n", 20), "\n")})
		m, _ = rightClick(m, m.mainRect().Min.X+2, m.mainRect().Min.Y+4)
		want := []string{"toggle line-wrap"}
		if diff {
			want = append(want, "next diff-file", "prev diff-file")
		}
		wantMenu(t, m, want...)
	}
}

func TestNoMenuOnRowsWithoutCommands(t *testing.T) {
	m := withMouse(homeTestModel())
	x, y := itemCell(t, m, regTree, 0)
	if mm, _ := rightClick(m, x, y); len(mm.popups) != 0 {
		t.Error("the tree Home row has no menu")
	}
	m = withMouse(withTreeEntries(wideWorkspace(), "n1:w1", "a.go"))
	x, y = itemCell(t, m, regRight, 0)
	if mm, _ := rightClick(m, x, y); len(mm.popups) != 0 {
		t.Error("a Files row has no menu")
	}
}

func TestRightClickSelectsWithoutOpening(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withFocus(m, leftSidebar)
	x, y := itemCell(t, m, regMain, 1)
	m, _ = rightClick(m, x, y)
	if m.focused != mainPane || m.main.top().(homeComp).cursor != 1 || m.liveSessionID() != "" {
		t.Errorf("focused=%v cursor=%d live=%q; a right-click focuses and selects only", m.focused, m.main.top().(homeComp).cursor, m.liveSessionID())
	}
	m.popups = nil
	m, _ = rightClick(m, x, y)
	if m.liveSessionID() != "" {
		t.Error("a right-click on the cursor row of a focused pane must not open it")
	}
}

func TestMenuRunsOnTheClickedPane(t *testing.T) {
	m := withMouse(killable(homeTestModel()))
	m = withFocus(m, leftSidebar)
	x, y := itemCell(t, m, regMain, 2)
	m, _ = rightClick(m, x, y)
	m, _ = upd(m, keyMsg("j"))
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if h := m.main.top().(homeComp); h.killID != m.order[2] {
		t.Errorf("killID = %q, want the kill prompt for the clicked session %q", h.killID, m.order[2])
	}
}

func TestOutsideClickOnlyClosesMenu(t *testing.T) {
	m := withMouse(homeTestModel())
	x, y := itemCell(t, m, regMain, 1)
	m, _ = rightClick(m, x, y)
	x0, y0 := itemCell(t, m, regMain, 0)
	m, _ = click(m, x0, y0)
	if len(m.popups) != 0 {
		t.Fatal("an outside click must close the menu")
	}
	if c := m.main.top().(homeComp).cursor; c != 1 {
		t.Errorf("cursor = %d; the outside click must not reach the pane", c)
	}
}

func TestRightClickOnZoneOrDividerDoesNothing(t *testing.T) {
	m := withMouse(wideWorkspace())
	y, x := findBlock(t, m, "Changes")
	mm, _ := rightClick(m, x, y)
	if len(mm.popups) != 0 || mm.right.tab != m.right.tab {
		t.Error("a right-click on a tab does nothing")
	}
	dx := m.projectsLeftW() + screenMargin + 1
	mm, _ = rightClick(m, dx, 10)
	if len(mm.popups) != 0 || mm.drag != dragNone {
		t.Error("a right-click on a divider does nothing")
	}
}

func TestRightClickGates(t *testing.T) {
	m := withMouse(homeTestModel())
	x, y := itemCell(t, m, regMain, 1)
	off := m
	off.mouse = false
	if mm, _ := rightClick(off, x, y); len(mm.popups) != 0 {
		t.Error("with the mouse off a right-click does nothing")
	}
	prompt := withHome(m, homeComp{killID: "n1:s1"})
	if mm, _ := rightClick(prompt, x, y); len(mm.popups) != 0 {
		t.Error("during a footer prompt a right-click does nothing")
	}
}

func TestNoMenuOnRawPane(t *testing.T) {
	m := withMouse(homeTestModel())
	m.left.tree.offerSpawn = &spawnOffer{nodeID: "n1", cwd: "/repo", prompt: "x"}
	x, y := itemCell(t, m, regTree, 1)
	m, _ = rightClick(m, x, y)
	if len(m.popups) != 0 {
		t.Error("a pane that takes every key (a pending answer) opens no menu, as : refuses there")
	}
}

func TestRightClickOnZoneKeepsFocus(t *testing.T) {
	m := withMouse(wideWorkspace())
	y, x := findBlock(t, m, "Changes")
	if mm, _ := rightClick(m, x, y); mm.focused != m.focused {
		t.Errorf("focused = %v; a right-click on a tab must not move focus", mm.focused)
	}
	m = withMouse(homeTestModel())
	y, x = findBlock(t, m, "▾")
	mm, _ := rightClick(m, x, y)
	if mm.focused != mainPane || mm.left.tree.isFolded("n1:p1") || len(mm.popups) != 0 {
		t.Errorf("focused=%v folded=%v; a right-click on a fold marker does nothing", mm.focused, mm.left.tree.isFolded("n1:p1"))
	}
}

func TestMiddleClickOutsideClosesMenu(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	m.View()
	m, _ = upd(m, tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseMiddle})
	if len(m.popups) != 0 {
		t.Error("a click with any button outside the menu closes it")
	}
}

func TestContextMenuFitsShortTerminal(t *testing.T) {
	m := withMouse(homeTestModel())
	m.height = 7
	k := projectsKeys
	menu := contextMenu{at: uv.Pos(5, 0), screen: m.screen(), entries: []binding{k.Spawn, k.New, k.Rename, k.Pin, k.Unpin, k.Hide, k.Unhide, k.Forget}, cursor: 7}
	m = withPopup(m, menu)
	if y, _ := findBlock(t, m, "project forget"); y >= m.height {
		t.Errorf("the cursor entry is on row %d, outside the %d-row screen", y, m.height)
	}
	x, y := itemCell(t, m, regPopup, 7)
	if y >= m.height || x >= m.width {
		t.Errorf("the cursor entry's hit row (%d,%d) is outside the screen", x, y)
	}
}

func TestMenuOnPinnedProject(t *testing.T) {
	m := withMouse(homeTestModel())
	m.left.tree.data[0].Pinned = true
	m.left.tree.rebuild()
	x, y := itemCell(t, m, regTree, 1)
	m, _ = rightClick(m, x, y)
	wantMenu(t, m, "session spawn", "workspace new", "project rename", "project unpin", "project hide", "project forget")
}

func TestNoMenuOnHistoryProject(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withView(m, viewHistoryProjects)
	m = withHistoryProjects(m, session.HistoryProject{Label: "a", Cwd: "/a", NodeID: "n1", ProjectDir: "a"})
	x, y := itemCell(t, m, regMain, 0)
	if mm, _ := rightClick(m, x, y); len(mm.popups) != 0 {
		t.Error("a History project card has no menu")
	}
}

func TestRightClickOnLiveScreenDoesNothing(t *testing.T) {
	m, _ := screenModel()
	m.hits = &hitMap{}
	m.mouse = true
	r := m.mainRect()
	if mm, _ := rightClick(m, r.Min.X+2, r.Min.Y+4); len(mm.popups) != 0 {
		t.Error("a right-click on the live screen does nothing")
	}
}

func TestRightClickKeepsOtherPopupsOpen(t *testing.T) {
	m := withMouse(homeTestModel())
	m, _ = m.openPalette()
	if mm, _ := rightClick(m, 1, 1); len(mm.popups) != 1 {
		t.Error("a right-click outside the palette leaves it open")
	}
}

func hoverAt(m model, x, y int) (model, tea.Cmd) {
	m.View()
	return upd(m, tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
}

func TestMenuAsksForAllMotion(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	if mode := m.View().MouseMode; mode != tea.MouseModeAllMotion {
		t.Errorf("mouse mode = %v with the menu open, want all motion", mode)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if mode := m.View().MouseMode; mode != tea.MouseModeCellMotion {
		t.Errorf("mouse mode = %v after the menu closed, want cell motion", mode)
	}
}

func TestHoverMovesMenuCursor(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	x, y := itemCell(t, m, regPopup, 1)
	m, _ = hoverAt(m, x, y)
	if p := m.popups.front().(contextMenu); p.cursor != 1 {
		t.Errorf("cursor = %d, want the hovered row 1", p.cursor)
	}
}

func TestHoverOutsideMenuKeepsCursor(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withPopup(m, killMenu(m, uv.Pos(10, 5)))
	m, _ = hoverAt(m, 1, 1)
	if p := m.popups.front().(contextMenu); p.cursor != 0 || len(m.popups) != 1 {
		t.Errorf("cursor=%d popups=%d; a hover outside the menu changes nothing", p.cursor, len(m.popups))
	}
}
