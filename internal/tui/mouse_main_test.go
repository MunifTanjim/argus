package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestHomeClickSelectsThenOpens(t *testing.T) {
	m := withMouse(homeTestModel())
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	if homeOf(m).cursor != 1 {
		t.Fatalf("cursor = %d, want 1", homeOf(m).cursor)
	}
	m, _ = click(m, x, y)
	if m.liveSessionID() != m.order[1] {
		t.Errorf("live session = %q, want %q", m.liveSessionID(), m.order[1])
	}
}

func TestHomeClickFromAnotherPaneOnlySelects(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withFocus(m, leftSidebar)
	x, y := itemCell(t, m, regMain, 0)
	m, _ = click(m, x, y)
	if m.focused != mainPane || m.liveSessionID() != "" {
		t.Errorf("focused=%v live=%q; the first click must only focus and select", m.focused, m.liveSessionID())
	}
}

func TestClickBelowRowsOnlyFocuses(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withHome(m, homeComp{cursor: 1})
	m = withFocus(m, leftSidebar)
	m, _ = click(m, m.mainRect().Min.X+1, m.mainRect().Max.Y-1)
	if m.focused != mainPane || homeOf(m).cursor != 1 {
		t.Errorf("focused=%v cursor=%d; an empty cell must only focus", m.focused, homeOf(m).cursor)
	}
}

func TestHomeWheelMovesCursor(t *testing.T) {
	m := withMouse(homeTestModel())
	x := m.mainRect().Min.X + 1
	if m, _ = wheelAt(m, x, 10, 2); homeOf(m).cursor != 2 {
		t.Fatalf("cursor = %d, want 2", homeOf(m).cursor)
	}
	if m, _ = wheelAt(m, x, 10, -5); homeOf(m).cursor != 0 {
		t.Fatalf("cursor = %d, want 0", homeOf(m).cursor)
	}
}

func TestWheelScrollsUnfocusedPaneWithoutFocus(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withFocus(m, leftSidebar)
	m, _ = wheelAt(m, m.mainRect().Min.X+1, 10, 1)
	if m.focused != leftSidebar || homeOf(m).cursor != 1 {
		t.Errorf("focused=%v cursor=%d; the wheel scrolls the pane under it and keeps focus", m.focused, homeOf(m).cursor)
	}
}

func TestHomeTabsSwitch(t *testing.T) {
	m := withMouse(homeTestModel())
	y, x := findBlock(t, m, "History")
	m, _ = click(m, x, y)
	if _, ok := m.baseComp().(historyComp); !ok {
		t.Fatalf("base = %T, want the History tab", m.baseComp())
	}
	y, x = findBlock(t, m, "Sessions")
	m, _ = click(m, x, y)
	if _, ok := m.baseComp().(homeComp); !ok {
		t.Fatalf("base = %T, want the Sessions tab", m.baseComp())
	}
}

func TestWorkspaceClickOpensSession(t *testing.T) {
	m := withMouse(wideWorkspace())
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	if p := m.main.top().(workspaceComp); p.cursor != 1 || m.focused != mainPane {
		t.Fatalf("cursor=%d focused=%v, want 1 and the main pane", p.cursor, m.focused)
	}
	m, _ = click(m, x, y)
	if m.liveSessionID() == "" {
		t.Error("a second click must open the session")
	}
}

func TestHistoryClickOpensProject(t *testing.T) {
	m := withMouse(homeTestModel())
	m = withView(m, viewHistoryProjects)
	m = withHistoryProjects(m,
		session.HistoryProject{Label: "a", Cwd: "/a", NodeID: "n1", ProjectDir: "a"},
		session.HistoryProject{Label: "b", Cwd: "/b", NodeID: "n1", ProjectDir: "b"},
	)
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	m, _ = click(m, x, y)
	h := m.main.top().(historyComp)
	if !h.inProject || h.project.Label != "b" {
		t.Errorf("inProject=%v project=%q, want project b", h.inProject, h.project.Label)
	}
}

func TestLogsWheelScrolls(t *testing.T) {
	b := logbuf.New(1000)
	fillLogs(b, 100)
	m := withMouse(newModel(logsStubClient{}, false, b))
	m.left.hidden = true
	m.width, m.height = 80, 30
	m = withView(m, viewLogs)
	m, _ = wheelAt(m, 5, 10, -3)
	if l := logsOf(m); l.follow || l.scroll != 73 {
		t.Errorf("follow=%v scroll=%d, want a pause at 73", l.follow, l.scroll)
	}
}

func chunks(n int) []transcript.Chunk {
	out := make([]transcript.Chunk, n)
	for i := range out {
		out[i] = transcript.Chunk{ID: string(rune('a' + i)), Kind: transcript.ChunkSystem, Text: "note", Detail: "more"}
	}
	return out
}

func liveWithChunks(n int) model {
	m := withMouse(testModel())
	m = withView(m, viewSession)
	m = withFocus(m, mainPane)
	return withChunks(m, chunks(n))
}

func TestTranscriptClickSelectsThenDrills(t *testing.T) {
	m := liveWithChunks(3)
	x, y := itemCell(t, m, regMain, 2)
	m, _ = click(m, x, y)
	if trOf(m).transcript.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", trOf(m).transcript.cursor)
	}
	m, _ = click(m, x, y)
	if trOf(m).historyView != histDetail {
		t.Error("a second click must open the detail")
	}
}

func TestTranscriptWheelScrolls(t *testing.T) {
	m := liveWithChunks(40)
	m, _ = wheelAt(m, m.mainRect().Min.X+1, 10, 3)
	if s := trOf(m).transcript.scroll; s != 3 {
		t.Fatalf("scroll = %d, want 3", s)
	}
	if !tvIn(m).cursorVisible() {
		t.Error("the cursor must follow into the viewport")
	}
}

func TestFileWheelScrolls(t *testing.T) {
	m := withMouse(testModel())
	m = withView(m, viewSession)
	m = withFile(m, fileComp{ws: "n1:w1", path: "a.go", lines: strings.Split(strings.Repeat("x\n", 100), "\n")})
	m, _ = wheelAt(m, m.mainRect().Min.X+1, 10, 4)
	if fileOf(m).scroll != 4 {
		t.Errorf("scroll = %d, want 4", fileOf(m).scroll)
	}
}

func TestHelpWheelScrolls(t *testing.T) {
	m := withMouse(homeTestModel())
	m.height = 12
	m.showHelp = true
	if m.helpMaxScroll() == 0 {
		t.Skip("the help fits; this test needs a short terminal")
	}
	m, _ = wheelAt(m, 5, 5, 2)
	if m.helpScroll != 2 {
		t.Errorf("helpScroll = %d, want 2", m.helpScroll)
	}
}

func TestDetailClickSelectsItem(t *testing.T) {
	m := liveWithChunks(1)
	m = withChunks(m, []transcript.Chunk{{ID: "a", Kind: transcript.ChunkAI, Items: []transcript.Item{
		{Kind: transcript.ItemText, Text: "one"}, {Kind: transcript.ItemText, Text: "two"},
	}}})
	m, _ = onTr(m, func(v tview) tea.Cmd { v.enterDetail(); return nil })
	m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	if f := tvIn(m).topFrame(); f == nil || f.cursor != 1 {
		t.Fatalf("detail cursor = %v, want 1", f)
	}
}

func longQuestion() *session.Interaction {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("context line %d", i)
	}
	return question(session.QuestionSpec{Question: strings.Join(lines, "\n"), Options: []string{"yes", "no"}})
}

func TestDockWheelScrolls(t *testing.T) {
	m := withMouse(sessionModel(longQuestion()))
	m = withFocus(m, sessionDock)
	m.View()
	var dock *hitArea
	for _, a := range m.hits.areas {
		if a.region == regDock {
			dock = a
		}
	}
	if dock == nil {
		t.Fatal("the dock region is missing")
	}
	m, _ = wheelAt(m, dock.rect.Min.X+1, dock.rect.Min.Y+1, 1)
	if m.dock.scroll != 1 || m.focused != sessionDock {
		t.Errorf("scroll=%d focused=%v, want 1 and the dock", m.dock.scroll, m.focused)
	}
}

func TestDockWheelKeepsFocusWhenCollapsed(t *testing.T) {
	m := withMouse(sessionModel(longQuestion()))
	m.View()
	var dock *hitArea
	for _, a := range m.hits.areas {
		if a.region == regDock {
			dock = a
		}
	}
	if dock == nil {
		t.Fatal("the dock region is missing")
	}
	m.dock.scroll = 3
	m, _ = wheelAt(m, dock.rect.Min.X+1, dock.rect.Min.Y, 1)
	if m.focused != mainPane {
		t.Error("the wheel must not move focus")
	}
	if m.dock.scroll != 3 {
		t.Errorf("dock scroll = %d, want 3; a collapsed dock must not scroll", m.dock.scroll)
	}
}

func TestTranscriptWheelIgnoredUnderRedactList(t *testing.T) {
	m := withMouse(withView(model{viewer: true, redactMode: true, width: 120, height: 40}, viewHistoryTranscript))
	m = withFocus(m, mainPane)
	m = withChunks(m, chunks(40))
	m = withTr(m, func(t *transcriptComp) { t.redact.listActive = true })
	if !tvIn(m).redactListActive() {
		t.Fatal("the redact list must be active")
	}
	before := trOf(m).transcript
	m, _ = wheelAt(m, m.mainRect().Min.X+1, 10, 3)
	if got := trOf(m).transcript; got.scroll != before.scroll || got.cursor != before.cursor {
		t.Errorf("scroll=%d cursor=%d, want %d and %d; the wheel must not move the hidden transcript", got.scroll, got.cursor, before.scroll, before.cursor)
	}
}

func TestClickClearsFlashWhenMovingFocus(t *testing.T) {
	m := withMouse(wideWorkspace())
	m = withFocus(m, mainPane)
	m.flash = "stale"
	m, _ = click(m, 1, m.height-4)
	if m.focused != leftSidebar {
		t.Fatalf("focused = %v, want the tree", m.focused)
	}
	if m.flash != "" {
		t.Errorf("flash = %q, want it cleared", m.flash)
	}
}

func TestSpawnClickChoosesNode(t *testing.T) {
	c := &spawnPickClient{nodes: []api.NodeInfo{
		{ID: "alpha", Capabilities: api.NodeCapabilities{SpawnSession: true}},
		{ID: "beta", Capabilities: api.NodeCapabilities{SpawnSession: true}},
	}}
	m := withMouse(openSpawn(t, c))
	m.width, m.height = 120, 30
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	if spawnOf(m).cursor != 1 {
		t.Fatalf("cursor = %d, want 1", spawnOf(m).cursor)
	}
	m, _ = click(m, x, y)
	if spawnOf(m).nodeID != "beta" {
		t.Errorf("node = %q, want beta", spawnOf(m).nodeID)
	}
}

func TestSpawnWheelMovesCursor(t *testing.T) {
	c := &spawnPickClient{nodes: []api.NodeInfo{
		{ID: "alpha", Capabilities: api.NodeCapabilities{SpawnSession: true}},
		{ID: "beta", Capabilities: api.NodeCapabilities{SpawnSession: true}},
	}}
	m := withMouse(openSpawn(t, c))
	m.width, m.height = 120, 30
	m, _ = wheelAt(m, m.mainRect().Min.X+1, 10, 5)
	if spawnOf(m).cursor != 1 {
		t.Errorf("cursor = %d, want 1", spawnOf(m).cursor)
	}
}

func TestTranscriptBodyClickSelectsWithoutDrilling(t *testing.T) {
	m := liveWithChunks(3)
	x, y := itemCell(t, m, regMain, 2)
	m, _ = click(m, x, y+1)
	if trOf(m).transcript.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", trOf(m).transcript.cursor)
	}
	m, _ = click(m, x, y+1)
	if trOf(m).historyView == histDetail {
		t.Error("a click on the card body must not open the detail")
	}
	m, _ = click(m, x, y)
	if trOf(m).historyView != histDetail {
		t.Error("a click on the selected card's header must open the detail")
	}
}

// nestedDetail is the sample AI turn's detail with its first item drilled into.
func nestedDetail(mouse bool) model {
	m := withFocus(waitingSession(), mainPane)
	m.mouse, m.hits = mouse, &hitMap{}
	v := tvOf(&m)
	v.transcript.cursor = 1
	v.actDrillChunk(tea.KeyPressMsg{})
	v.actDetailDrill(tea.KeyPressMsg{})
	v.put()
	return m
}

func closeCell(t *testing.T, m model) (x, y int) {
	t.Helper()
	m.View()
	for _, a := range m.hits.areas {
		for _, z := range a.zones {
			if a.region == regMain && z.target.kind == hitClose {
				return a.rect.Min.X + z.rect.Min.X, a.rect.Min.Y + z.rect.Min.Y
			}
		}
	}
	t.Fatal("no close button in the detail")
	return 0, 0
}

func TestDetailCloseButtonShowsOnlyWithTheMouse(t *testing.T) {
	if strings.Contains(nestedDetail(false).View().Content, glyphClose) {
		t.Error("with the mouse off, the detail must draw no close button")
	}
	m := nestedDetail(true)
	x, y := closeCell(t, m)
	line := strings.Split(ansi.Strip(m.View().Content), "\n")[y]
	if cell := ansi.Cut(line, x, x+1); cell != glyphClose || !strings.Contains(line, "›") {
		t.Errorf("the zone at %d covers %q, want the close button at the end of the breadcrumb %q", x, cell, line)
	}
}

func TestDetailCloseGoesBackOneLevel(t *testing.T) {
	m := nestedDetail(true)
	if n := len(trOf(m).transcript.detailStack); n != 2 {
		t.Fatalf("detail depth = %d, want 2", n)
	}
	x, y := closeCell(t, m)
	m, _ = click(m, x, y)
	if n := len(trOf(m).transcript.detailStack); n != 1 || trOf(m).historyView != histDetail {
		t.Fatalf("depth = %d view = %v, want one level up", n, trOf(m).historyView)
	}
	x, y = closeCell(t, m)
	m, _ = click(m, x, y)
	if trOf(m).historyView != histTranscript {
		t.Error("closing the root frame must return to the cards")
	}
}
