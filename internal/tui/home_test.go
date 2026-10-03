package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func homeTestModel() model {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = withView(m, viewHome)
	m = withFocus(m, mainPane)
	m.order = []string{"n1:s1", "n1:s2", "n1:s3"}
	return m
}

func assertFits(t *testing.T, out string, w int) {
	t.Helper()
	for i, ln := range strings.Split(ansi.Strip(out), "\n") {
		if lipgloss.Width(ln) > w {
			t.Fatalf("line %d is %d wide, wider than %d: %q", i, lipgloss.Width(ln), w, ln)
		}
	}
}

func TestHomeTabsRenderInsideTheFrame(t *testing.T) {
	for _, view := range []shownView{viewHome, viewHistoryProjects, viewLogs} {
		m := homeTestModel()
		m = withView(m, view)
		if !framed(m) || m.bodyWidth() != m.frameWidth()-m.projectsLeftW()-dividerWidth {
			t.Errorf("view %v: embedded=%v bodyWidth=%d", view, framed(m), m.bodyWidth())
		}
		out := m.View().Content
		if !strings.Contains(ansi.Strip(out), "Projects") {
			t.Errorf("view %v: sidebar missing", view)
		}
		assertFits(t, out, 120)
	}
}

func TestSessionFromHistoryEmbeds(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewHistoryTranscript)
	mm, _ := m.enterSession("n1:s1")
	if returnView(mm) != viewHistoryTranscript || !framed(mm) {
		t.Errorf("a session resumed from History should embed: return=%v embedded=%v", returnView(mm), framed(mm))
	}
}

func TestViewerNeverEmbeds(t *testing.T) {
	m := homeTestModel()
	m.viewer = true
	m = withView(m, viewHistoryTranscript)
	if framed(m) || m.bodyWidth() != 120 {
		t.Errorf("viewer: embedded=%v bodyWidth=%d, want full screen", framed(m), m.bodyWidth())
	}
}

func TestHiddenSidebarGivesHomeFullWidth(t *testing.T) {
	m := homeTestModel()
	m.left.hidden = true
	if !framed(m) || m.bodyWidth() != m.frameWidth() {
		t.Errorf("hidden sidebar: embedded=%v bodyWidth=%d, want framed at full width", framed(m), m.bodyWidth())
	}
}

func TestHomeRowIsFirstAndSurvivesFilter(t *testing.T) {
	m := homeTestModel()
	m.left.tree.rebuild()
	if r := m.left.tree.rows[0]; r.kind != rowHome || r.id != homeRowID {
		t.Fatalf("first row = %+v, want Home", r)
	}
	m.left.tree.setFilter("zzz")
	if len(m.left.tree.rows) != 1 || m.left.tree.rows[0].kind != rowHome {
		t.Errorf("a no-match filter should leave only Home: %+v", m.left.tree.rows)
	}
}

func TestHomeBadgeCountsAllLiveSessions(t *testing.T) {
	m := homeTestModel()
	m.sessions["n1:s9"] = session.Session{ID: "n1:s9", Status: session.StatusAwaitingInput} // no workspace
	m.left.tree.rebuild()
	line := ansi.Strip(m.projRowLine(m.left.tree.rows[0], false, true, m.workspaceActivity(), 40))
	if !strings.HasSuffix(line, "◆ 4") {
		t.Errorf("Home row = %q, want a ◆ 4 badge across every live session", line)
	}
}

func TestTreeShowsHomeWhileProjectsLoadOrFail(t *testing.T) {
	m := homeTestModel()
	m.left.tree.data = nil
	m.left.tree.rebuild()
	tree := ansi.Strip(treePane(m, 40, 20))
	if !strings.Contains(tree, "Home") || !strings.Contains(tree, "loading projects") {
		t.Errorf("loading tree = %q", tree)
	}
	m.left.tree.err = errString("registry disabled")
	tree = ansi.Strip(treePane(m, 40, 20))
	if !strings.Contains(tree, "Home") || !strings.Contains(tree, "registry disabled") {
		t.Errorf("error tree = %q", tree)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Projects") {
		t.Error("the Home pane must still render next to the tree")
	}
}

func TestHomeUnderTheTreeHasASelectedCard(t *testing.T) {
	m := pressKeys(homeTestModel(), cw('h')...)
	out := m.View().Content
	if !strings.Contains(out, "┏") { // the selected card uses heavy chrome
		t.Error("Home under the tree should highlight its cursor card")
	}
	if !strings.Contains(ansi.Strip(out), "repo") {
		t.Error("Home under the tree should show the session list")
	}
}

func TestEnterOnHomeFocusesHomePane(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	m.left.tree.rebuild()
	m.left.tree.cursor = 0
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: 'l', Text: "l"}} {
		res, _ := m.runKey(k)
		if mm := res.(model); viewOf(mm) != viewHome || mm.focused != mainPane {
			t.Errorf("%q on Home: view=%v focus=%v, want the Home pane", k.String(), viewOf(mm), mm.focused)
		}
	}
	for _, r := range []rune{'w', 'l'} {
		if mm := pressKeys(m, cw(r)...); viewOf(mm) != viewHome || mm.focused != mainPane {
			t.Errorf("<C-w>%c on Home: view=%v focus=%v, want the Home pane", r, viewOf(mm), mm.focused)
		}
	}
}

func TestHomePaneKeysReturnToTree(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEscape}} {
		m := homeTestModel()
		m.left.tree.rebuild()
		m.left.tree.cursor = 2 // a workspace, to prove we land on Home
		res, cmd := m.handleKey(k)
		mm := res.(model)
		if viewOf(mm) != viewHome || mm.focused != leftSidebar || mm.left.tree.cursorRowID() != homeRowID {
			t.Errorf("%q: view=%v focus=%v row=%q, want the tree on Home over Home", k.String(), viewOf(mm), mm.focused, mm.left.tree.cursorRowID())
		}
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Errorf("%q must not quit from the Home pane", k.String())
			}
		}
	}
}

func TestTreeQQuitsAndEscDoesNothing(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	m.left.tree.rebuild()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'Q', Text: "Q"})
	if cmd == nil {
		t.Fatal("Q on the tree should quit")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("Q on the tree should return tea.Quit")
	}
	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if mm := res.(model); viewOf(mm) != viewOf(m) || mm.focused != leftSidebar {
		t.Errorf("esc on the tree changed something: view=%v focus=%v", viewOf(mm), mm.focused)
	}
}

func TestPKeyDoesNothing(t *testing.T) {
	m := homeTestModel()
	res, _ := m.handleKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if mm := res.(model); viewOf(mm) != viewHome {
		t.Errorf("p changed view to %v", viewOf(mm))
	}
}

func TestNarrowTerminalHomeQuits(t *testing.T) {
	m := homeTestModel()
	m.width = 60 // sidebar auto-hides below 80
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'Q', Text: "Q"})
	if cmd == nil {
		t.Fatal("Q with no tree to return to should quit")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("want tea.Quit")
	}
	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if mm := res.(model); viewOf(mm) != viewHome {
		t.Errorf("esc with no tree changed view to %v", viewOf(mm))
	}
}

func TestNewModelStartsOnHome(t *testing.T) {
	m := newModel(&recordingClient{}, true, nil)
	m.width, m.height = 120, 30
	if viewOf(m) != viewHome || m.focused != mainPane || m.left.tree.cursorRowID() != homeRowID {
		t.Fatalf("start: view=%v focus=%v row=%q, want the Home pane", viewOf(m), m.focused, m.left.tree.cursorRowID())
	}
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "Projects") || !strings.Contains(out, "No sessions yet") {
		t.Error("with no sessions, the start view should be the full-screen splash")
	}
	m.sessions["s1"] = session.Session{ID: "s1", Repo: "repo"}
	m.order = []string{"s1"}
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"Projects", "Home", "loading projects"} {
		if !strings.Contains(out, want) {
			t.Errorf("start view with a session missing %q", want)
		}
	}
}

func TestInitFetchesProjects(t *testing.T) {
	rc := &recordingClient{}
	m := newModel(rc, true, nil)
	execCmd(m.Init())
	for _, c := range rc.calls {
		if c == api.MethodProjectList {
			return
		}
	}
	t.Errorf("Init should fetch project.list; calls = %v", rc.calls)
}

func TestLeaderOWorksOutsideTheTree(t *testing.T) {
	m := homeTestModel() // Home pane
	m = typeKeys(m, " o")
	if m.sidebarVisible() {
		t.Fatal("␣o in the Home pane should hide the sidebar")
	}
	m = typeKeys(m, " o")
	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if mm := res.(model); !mm.sidebarVisible() || !mm.treeFocused() || viewOf(mm) != viewHome {
		t.Errorf("␣o twice then esc should reach the tree over Home: visible=%v focus=%v view=%v", mm.sidebarVisible(), mm.focused, viewOf(mm))
	}

	s := homeTestModel()
	ss, _ := s.enterSession("n1:s1") // opened from Home: returns to viewHome
	if typeKeys(ss, " o").sidebarVisible() {
		t.Error("␣o in a session opened from Home should hide the sidebar")
	}
}

func TestNoVisibleTreeEscGoesHome(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEscape}} {
		m := homeTestModel()
		m = withView(m, viewTree)
		m = withFocus(m, leftSidebar)
		m.left.tree.rebuild()
		m = selectRow(m, "n1:w1")
		m, _ = upd(m, tea.WindowSizeMsg{Width: 70, Height: m.height}) // tree auto-hidden
		if mm, _ := upd(m, k); viewOf(mm) != viewHome {
			t.Errorf("%q with no visible tree: view=%v, want the Home pane", k.String(), viewOf(mm))
		}
	}
}

func TestHiddenTreeOnHomeRowEntersHome(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	m.left.tree.rebuild()
	m.left.tree.cursor = 0
	if mm := typeKeys(m, " o"); viewOf(mm) != viewHome {
		t.Errorf("␣o on the Home row: view=%v, want the Home pane", viewOf(mm))
	}

	m, _ = upd(m, tea.WindowSizeMsg{Width: 70, Height: m.height}) // resized below the breakpoint while on the Home row
	res, _ := m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if mm := res.(model); viewOf(mm) != viewHome || homeOf(mm).cursor != 1 {
		t.Errorf("a key on a hidden Home row should act in the Home pane: view=%v cursor=%d", viewOf(mm), homeOf(mm).cursor)
	}
}

func TestTreeStartedSpawnRendersInPane(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewTree)
	m.left.tree.rebuild()
	m = selectRow(m, "n1:w1")
	m.client = &recordingClient{}
	m.beginPresetSpawn("n1", "/repo", "")
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Projects") {
		t.Error("a spawn started from the tree should render in the pane, next to the tree")
	}
	assertFits(t, m.View().Content, 120)
}

func TestTreeReloadMovesTheCursorToTheWantedRowAndKeepsHome(t *testing.T) {
	m := homeTestModel() // Home pane
	m.left.tree.rebuild()
	m.left.tree.cursor = 0
	m.left.tree.want = "n1:w2" // e.g. a workspace create finished meanwhile
	res, _ := m.Update(projectsTreeMsg{tree: m.left.tree.data})
	mm := res.(model)
	if got := mm.left.tree.cursorRowID(); got != "n1:w2" || viewOf(mm) != viewHome {
		t.Errorf("tree reload: cursor on %q view = %v, want n1:w2 over Home", got, viewOf(mm))
	}
}

func TestSpinnerRunsInFramedViews(t *testing.T) {
	m := homeTestModel()
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Status: session.StatusWorking}
	m = withView(m, viewSession)
	m.spinning = false
	if cmd := m.maybeSpin(); cmd == nil {
		t.Error("the sidebar badges should keep spinning in a framed session view")
	}
}

func TestFilteredTreeFooterStillShowsQuit(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	m.left.tree.rebuild()
	m.left.tree.setFilter("argus")
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "clear filter") || !strings.Contains(f, "quit") {
		t.Errorf("filtered tree footer = %q, want both clear filter and quit", f)
	}
}

func TestListViewSurvivesPendingKillWithNoCursor(t *testing.T) {
	m := homeTestModel()
	m = withHome(m, homeComp{cursor: -1, killID: "n1:s1"})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("listView panicked: %v", r)
		}
	}()
	_ = paneView(m)
}

func linesContaining(out, sub string) int {
	n := 0
	for _, ln := range strings.Split(ansi.Strip(out), "\n") {
		if strings.Contains(ln, sub) {
			n++
		}
	}
	return n
}

// frameRow identifies the status bar every framed state shares.
const frameRow = "\U000f167a argus"

func TestFramedSessionHeaderInPane(t *testing.T) {
	m := homeTestModel()
	mm, _ := m.enterSession("n1:s1")
	out := mm.View().Content
	lines := strings.Split(ansi.Strip(out), "\n")
	if !strings.Contains(lines[0], frameRow) {
		t.Errorf("row 0 should be the frame header, got %q", lines[0])
	}
	if !strings.Contains(lines[2], "│") || !strings.Contains(lines[2], "repo") || strings.Contains(lines[2], "argus ·") {
		t.Errorf("the session header should sit in the pane without the brand, got %q", lines[2])
	}
	if len(lines) != mm.height {
		t.Errorf("framed session is %d lines tall, want %d", len(lines), mm.height)
	}
	assertFits(t, out, mm.width)
}

func TestHomeTabsStayInPane(t *testing.T) {
	m := homeTestModel()
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if !strings.Contains(lines[0], frameRow) {
		t.Errorf("row 0 should be the frame header, got %q", lines[0])
	}
	if !strings.Contains(lines[2], "│") || !strings.Contains(lines[2], "Sessions") || strings.Contains(lines[2], "│   \U000f167a") {
		t.Errorf("the Home tabs should sit in the pane without the brand, got %q", lines[2])
	}
	if n := linesContaining(m.View().Content, "Sessions"); n != 1 {
		t.Errorf("the Home tabs appear on %d lines, want 1", n)
	}
}

func TestHomeLayoutStableAcrossFocus(t *testing.T) {
	pane := homeTestModel() // Home pane focused
	pane.left.tree.rebuild()
	pane.left.tree.cursor = 0
	tree := pane
	tree = withView(tree, viewTree)
	tree = withFocus(tree, leftSidebar)

	pl := strings.Split(ansi.Strip(pane.View().Content), "\n")
	tl := strings.Split(ansi.Strip(tree.View().Content), "\n")
	if len(pl) != len(tl) {
		t.Fatalf("screen height changes with focus: %d vs %d", len(pl), len(tl))
	}
	for i := 0; i < 4; i++ {
		if pl[i] != tl[i] {
			t.Errorf("row %d moves with focus:\n pane: %q\n tree: %q", i, pl[i], tl[i])
		}
	}
	if !strings.Contains(tl[len(tl)-1]+tl[len(tl)-2], "/ filter") {
		t.Error("tree-focused Home footer should show the tree's keys")
	}
}

func TestWorkspaceRowKeepsFrameHeader(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	m.left.tree.rebuild()
	m = selectRow(m, "n1:w1")
	if first := strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]; !strings.Contains(first, frameRow) {
		t.Errorf("row 0 should be the frame header on a workspace too, got %q", first)
	}
}

// cardColumn is the display column of the first session-card border in out.
func cardColumn(out string) int {
	for _, ln := range strings.Split(ansi.Strip(out), "\n") {
		for _, border := range []string{"┏", "╭"} {
			if i := strings.Index(ln, border); i >= 0 {
				return lipgloss.Width(ln[:i])
			}
		}
	}
	return -1
}

func TestHomeCardsAlignWithWorkspace(t *testing.T) {
	home := homeTestModel()
	ws := homeTestModel()
	ws = withView(ws, viewTree)
	ws = withFocus(ws, mainPane)
	ws.left.tree.rebuild()
	ws = selectRow(ws, "n1:w1")
	hc, wc := cardColumn(home.View().Content), cardColumn(ws.View().Content)
	if hc < 0 || hc != wc {
		t.Errorf("Home cards start at column %d, workspace cards at %d; want the same", hc, wc)
	}

}

func TestHiddenSidebarHomeMatchesWorkspace(t *testing.T) {
	home := homeTestModel()
	home.left.hidden = true
	ws := homeTestModel()
	ws.left.hidden = true
	ws = withView(ws, viewTree)
	ws = withFocus(ws, mainPane)
	ws.left.tree.rebuild()
	ws = selectRow(ws, "n1:w1")

	hl := strings.Split(ansi.Strip(home.View().Content), "\n")
	wl := strings.Split(ansi.Strip(ws.View().Content), "\n")
	if !strings.Contains(hl[0], frameRow) || hl[0] != wl[0] {
		t.Errorf("hidden-sidebar header rows differ:\n home: %q\n ws:   %q", hl[0], wl[0])
	}
	if hc, wc := cardColumn(home.View().Content), cardColumn(ws.View().Content); hc != wc {
		t.Errorf("hidden-sidebar Home cards start at column %d, workspace at %d", hc, wc)
	}
	if len(hl) != home.height {
		t.Errorf("hidden-sidebar Home is %d lines tall, want %d", len(hl), home.height)
	}
}

func TestEmptyHomeSplashIsFullScreen(t *testing.T) {
	m := homeTestModel()
	m.order, m.sessions = nil, map[string]session.Session{}
	if viewOf(m) != viewHome {
		t.Fatalf("empty Home should show viewHome, got %v", viewOf(m))
	}
	if framed(m) || m.bodyWidth() != m.width {
		t.Fatalf("empty Home should use the whole screen: embedded=%v bodyWidth=%d", framed(m), m.bodyWidth())
	}
	out := ansi.Strip(m.View().Content)
	if strings.Contains(out, "Projects") {
		t.Error("the full-screen splash should not draw the tree")
	}
	if !strings.Contains(out, "esc tree") {
		t.Error("the splash footer should say how to reach the tree")
	}

	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	mm := res.(model)
	if !mm.treeFocused() || !strings.Contains(ansi.Strip(mm.View().Content), "Projects") {
		t.Errorf("esc from the splash should show the tree: focus=%v", mm.focused)
	}

	m.sessions = map[string]session.Session{"n1:s1": {ID: "n1:s1", WorkspaceID: "n1:w1", Repo: "repo"}}
	m.order = []string{"n1:s1"}
	if !framed(m) {
		t.Error("with a session, Home should be framed again")
	}
}

func TestHomeSKeyStartsSession(t *testing.T) {
	m := homeTestModel()
	res, cmd := m.handleKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if mm := res.(model); cmd == nil || mm.flash != "" || viewOf(mm) == viewScreen {
		t.Errorf("s on Home should start a session: cmd=%v flash=%q view=%v", cmd != nil, mm.flash, viewOf(mm))
	}
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"}); cmd != nil {
		t.Error("n on Home should do nothing")
	}
	f := ansi.Strip(m.View().Content)
	if !strings.Contains(f, "s spawn") || strings.Contains(f, "screen") {
		t.Errorf("Home footer should offer s spawn and no screen key:\n%s", f)
	}
}

func TestHelpOpensFromHomeTabs(t *testing.T) {
	for _, view := range []shownView{viewHome, viewHistoryProjects} {
		m := homeTestModel()
		m.height = 40
		m = withView(m, view)
		m = typeKeys(m, "g?")
		if out := ansi.Strip(m.View().Content); !m.showHelp || !strings.Contains(out, "Manage (tree)") || !strings.Contains(out, "any key close") {
			t.Errorf("view %v: g? should show the help:\n%s", view, out)
		}
		m, _ = upd(m, keyMsg("j"))
		if m.showHelp || viewOf(m) != view {
			t.Errorf("view %v: any key should close the help and stay: help=%v view=%v", view, m.showHelp, viewOf(m))
		}
	}
}

func TestFocusLeftFromHomeTabsReachesTree(t *testing.T) {
	for _, view := range []shownView{viewHome, viewHistoryProjects} {
		m := homeTestModel()
		m = withView(m, view)
		m = pressKeys(m, cw('h')...)
		if viewOf(m) != view || !m.treeFocused() || m.left.tree.cursorRowID() != homeRowID {
			t.Errorf("view %v, <C-w>h: want the tree on Home over the view: view=%v focus=%v row=%q", view, viewOf(m), m.focused, m.left.tree.cursorRowID())
		}
	}
}

func TestFooterSpansTheFrameInEveryState(t *testing.T) {
	base := homeTestModel()
	states := map[string]model{}
	states["home pane"] = base
	hist := base
	hist = withHistoryProjects(hist, session.HistoryProject{Label: "p", NodeID: "n1"})
	states["history"] = hist
	sess := base
	sess = withLive(sess, "n1:s1")
	states["session"] = sess
	ws := base
	ws = withView(ws, viewTree)
	ws = withFocus(ws, leftSidebar)
	ws = selectRow(ws, "n1:w1")
	states["workspace row"] = ws
	for name, m := range states {
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		if len(lines) != m.height {
			t.Errorf("%s: %d rows, want %d", name, len(lines), m.height)
			continue
		}
		if last := lines[len(lines)-1]; strings.Contains(last, "│") || strings.TrimSpace(last) == "" {
			t.Errorf("%s: the last row should be a footer across the frame: %q", name, last)
		}
		if above := lines[len(lines)-3]; !strings.Contains(above, "│") {
			t.Errorf("%s: the tree should reach the row above the footer gap: %q", name, above)
		}
	}
}

func TestSessionShowsAndClearsFlash(t *testing.T) {
	m := homeTestModel()
	m = withLive(m, "n1:s1")
	m.flash = "terminal detached"
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "terminal detached") {
		t.Fatalf("the session view should show the flash:\n%s", out)
	}
	m, _ = upd(m, keyMsg("j"))
	if m.flash != "" {
		t.Errorf("a key in the session should clear the flash: %q", m.flash)
	}
}

func TestEmptyHomeRowFramesSplashInPane(t *testing.T) {
	m := homeTestModel()
	m.order, m.sessions = nil, map[string]session.Session{}
	m.right.hidden = false
	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = res.(model)
	if !m.treeFocused() || !m.onHomeRow() {
		t.Fatalf("want the tree focused on Home: focus=%v row=%q", m.focused, m.left.tree.cursorRowID())
	}
	if !m.filesVisible() {
		t.Error("the right sidebar should show next to the tree")
	}
	out := m.View().Content
	if n := strings.Count(out, "\n") + 1; n != m.height {
		t.Errorf("view is %d rows, want %d", n, m.height)
	}
	assertFits(t, out, m.width)
	for _, ln := range strings.Split(ansi.Strip(out), "\n") {
		if strings.Contains(ln, "Sessions") && strings.Contains(ln, "argus") {
			t.Errorf("the framed splash should not repeat the argus brand: %q", ln)
		}
	}
}

func TestFramedScreenHasLeftMargin(t *testing.T) {
	check := func(name string, m model) {
		t.Helper()
		out := m.View().Content
		for i, ln := range strings.Split(ansi.Strip(out), "\n") {
			if strings.TrimSpace(ln) != "" && !strings.HasPrefix(ln, "  ") && !strings.HasPrefix(ln, "▌ ") {
				t.Errorf("%s: line %d has no left margin (blank or the cursor bar): %q", name, i, ln)
				return
			}
		}
		assertFits(t, out, m.width)
	}
	home := homeTestModel()
	check("home", home)
	hidden := homeTestModel()
	hidden.left.hidden = true
	check("home, sidebar hidden", hidden)
	if want := screenMargin + (hidden.frameWidth()-maxCardWidth)/2; cardColumn(hidden.View().Content) != want {
		t.Errorf("hidden-sidebar cards start at column %d, want centered at %d", cardColumn(hidden.View().Content), want)
	}
	ws := homeTestModel()
	ws = withView(ws, viewTree)
	ws = withFocus(ws, leftSidebar)
	ws.left.tree.rebuild()
	ws = selectRow(ws, "n1:w1")
	check("workspace", ws)
	s, _ := homeTestModel().enterSession("n1:s1")
	check("session", s)
}

// columnOf is the display column where sub starts on the first line holding it.
func columnOf(out, sub string) int {
	for _, ln := range strings.Split(ansi.Strip(out), "\n") {
		if i := strings.Index(ln, sub); i >= 0 {
			return lipgloss.Width(ln[:i])
		}
	}
	return -1
}

func TestTreeRowsAlignWithTitle(t *testing.T) {
	m := homeTestModel()
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	m.left.tree.rebuild()
	m = selectRow(m, "n1:p1") // cursor on the project row
	out := m.View().Content
	title := columnOf(out, "Projects")
	if row := columnOf(out, "▾ argus"); row != title {
		t.Errorf("project row starts at column %d, the Projects title at %d", row, title)
	}
	if bar := columnOf(out, "▌"); bar != title-screenMargin {
		t.Errorf("cursor bar at column %d, want it in the margin at %d", bar, title-screenMargin)
	}
}

func TestPanesCenterTheirContentColumn(t *testing.T) {
	home := homeTestModel()
	home.width = 220
	paneX := screenMargin + home.projectsLeftW() + dividerWidth
	paneW := home.bodyWidth()
	wantCards := paneX + (paneW-maxCardWidth)/2
	if c := cardColumn(home.View().Content); c != wantCards {
		t.Errorf("Home cards at column %d, want centered at %d", c, wantCards)
	}

	ws := homeTestModel()
	ws.width = 220
	ws = withView(ws, viewTree)
	ws = withFocus(ws, mainPane)
	ws.left.tree.rebuild()
	ws = selectRow(ws, "n1:w1")
	if c := cardColumn(ws.View().Content); c != wantCards {
		t.Errorf("workspace cards at column %d, want centered at %d", c, wantCards)
	}

	ws = withFile(ws, fileComp{ws: "n1:w1", path: "a.go", diff: true, lines: []string{"@@ -1 +1 @@", "-a", "+bb"}})
	if c := columnOf(ws.View().Content, "repo  main"); c != wantCards {
		t.Errorf("diff viewer header moved to %d, want it to stay at %d", c, wantCards)
	}
	wantText := paneX + (paneW-min(paneW, maxContentWidth))/2
	if c := columnOf(ws.View().Content, "+bb"); c != wantText {
		t.Errorf("diff content at %d, want the wide column at %d", c, wantText)
	}
}

func TestStatusBarShowsGlobalState(t *testing.T) {
	m := homeTestModel()
	m.width = 80
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Status: session.StatusAwaitingInput, Repo: "repo"}
	m.reconnecting = true
	m.client = &stubQuarantinedClient{q: true}
	out := m.View().Content
	lines := strings.Split(ansi.Strip(out), "\n")
	bar := lines[0]
	for _, want := range []string{"argus", "◆ 1 need you", "reconnecting…", "QUARANTINED", "argus lock pin"} {
		if !strings.Contains(bar, want) {
			t.Errorf("status bar missing %q: %q", want, bar)
		}
	}
	if strings.Contains(bar, "projects") {
		t.Errorf("status bar should drop the static label: %q", bar)
	}
	if !strings.HasSuffix(bar, "argus lock pin  "+glyphSidebarLeft+" "+glyphSidebarRightOff) {
		t.Errorf("status should be right-aligned: %q", bar)
	}
	assertFits(t, out, m.width)
	for _, ln := range lines[1:] {
		if strings.Contains(ln, "reconnecting") || strings.Contains(ln, "QUARANTINED") {
			t.Errorf("status repeated below the bar: %q", ln)
		}
	}
}

func TestNarrowStatusBarKeepsTheState(t *testing.T) {
	m := homeTestModel()
	m.width = 30
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", Status: session.StatusAwaitingInput}
	m.reconnecting = true
	bar := ansi.Strip(m.frameTitle())
	if !strings.HasSuffix(bar, "1 need you · reconnecting…") || ansi.StringWidth(bar) > m.width {
		t.Errorf("a narrow status bar should cut the brand, not the state: %q", bar)
	}
}

func TestStatusBarQuietWhenAllIsWell(t *testing.T) {
	m := homeTestModel()
	bar := strings.Split(ansi.Strip(m.View().Content), "\n")[0]
	if strings.Contains(bar, "need you") || strings.Contains(bar, "reconnecting") || strings.Contains(bar, "QUARANTINED") {
		t.Errorf("status bar should show only the brand when nothing needs attention: %q", bar)
	}
}

func TestSplashKeepsConnectionState(t *testing.T) {
	m := homeTestModel()
	m.order, m.sessions = nil, map[string]session.Session{}
	m.reconnecting = true
	m.client = &stubQuarantinedClient{q: true}
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "reconnecting") || !strings.Contains(out, "QUARANTINED") {
		t.Error("the full-screen splash has no status bar, so it must keep its own indicators")
	}
}

func TestActiveOnlyFiltersTheSessionLists(t *testing.T) {
	za := []tea.KeyPressMsg{keyMsg("z"), keyMsg("a")}
	statuses := func(m model) model {
		m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Status: session.StatusIdle}
		m.sessions["n1:s2"] = session.Session{ID: "n1:s2", WorkspaceID: "n1:w2", Status: session.StatusWorking}
		m.sessions["n1:s3"] = session.Session{ID: "n1:s3", WorkspaceID: "n1:w1", Status: session.StatusAwaitingInput}
		m.reorder()
		return m
	}

	m := statuses(homeTestModel())
	m.main = m.main.replaceAt(0, homeComp{cursor: slices.Index(m.order, "n1:s2")})
	m = pressKeys(m, za...)
	if !m.activeOnly || !slices.Equal(m.order, []string{"n1:s3", "n1:s2"}) {
		t.Fatalf("za on Home: activeOnly=%v order=%v, want the awaiting and working sessions", m.activeOnly, m.order)
	}
	if got := m.rootSessionID(); got != "n1:s2" {
		t.Errorf("the cursor should stay on its session: %q", got)
	}
	if !strings.Contains(m.flash, "active") {
		t.Errorf("flash = %q", m.flash)
	}
	m = pressKeys(m, za...)
	if m.activeOnly || len(m.order) != 3 || m.rootSessionID() != "n1:s2" {
		t.Errorf("za again: activeOnly=%v order=%v cursor=%q, want all sessions", m.activeOnly, m.order, m.rootSessionID())
	}

	m = statuses(homeTestModel())
	m.main = backStack{workspaceComp{ws: "n1:w1"}}
	m = pressKeys(m, za...)
	if ss := m.wsSessions("n1:w1"); len(ss) != 1 || ss[0].ID != "n1:s3" {
		t.Errorf("za on a workspace: sessions=%v, want only n1:s3", ss)
	}
}

func TestActiveOnlyWithNoActiveSessionsKeepsTheList(t *testing.T) {
	m := homeTestModel()
	for id, s := range m.sessions {
		s.Status = session.StatusIdle
		m.sessions[id] = s
	}
	m.activeOnly = true
	m.reorder()
	if len(m.order) != 0 {
		t.Fatalf("order = %v, want empty", m.order)
	}
	if got := m.rootComp().fullScreen(&ctx{m: &m}); got != notFull {
		t.Errorf("an empty filtered list must not show the welcome splash: %v", got)
	}
	if out := m.View().Content; !strings.Contains(out, "no active sessions") {
		t.Errorf("want the filtered empty state, got:\n%s", out)
	}
}

func TestSessionMatchesQuery(t *testing.T) {
	s := session.Session{ID: "n1:s1", Name: "Refactor", Repo: "argus", Branch: "feat/filter", NodeLabel: "mini",
		Cwd: "/src/hidden", Summary: &session.Summary{Task: "Fix the Parser"}}
	for q, want := range map[string]bool{
		"":       true,
		"refac":  true,
		"parser": true,
		"ARGUS":  true,
		"filter": true,
		"mini":   true,
		"hidden": false,
		"nope":   false,
	} {
		if got := sessionMatches(s, q); got != want {
			t.Errorf("sessionMatches(%q) = %v, want %v", q, got, want)
		}
	}
}

func filterTestModel() model {
	m := homeTestModel()
	m.sessions = map[string]session.Session{
		"n1:s1": {ID: "n1:s1", Name: "alpha", WorkspaceID: "n1:w1", Status: session.StatusIdle},
		"n1:s2": {ID: "n1:s2", Name: "beta", WorkspaceID: "n1:w1", Status: session.StatusIdle},
		"n1:s3": {ID: "n1:s3", Name: "alphabet", WorkspaceID: "n1:w2", Status: session.StatusIdle},
	}
	m.reorder()
	return m
}

func TestFilterNarrowsHomeAsYouType(t *testing.T) {
	m := filterTestModel()
	m.main = m.main.replaceAt(0, homeComp{cursor: slices.Index(m.order, "n1:s3")})
	m = typeKeys(m, "/alp")
	if m.sessionFilter != "alp" || !slices.Equal(m.order, []string{"n1:s1", "n1:s3"}) {
		t.Fatalf("filter=%q order=%v, want alpha and alphabet", m.sessionFilter, m.order)
	}
	if got := m.rootSessionID(); got != "n1:s3" {
		t.Errorf("the cursor should stay on its session: %q", got)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "filter: alp") {
		t.Errorf("want the filter prompt, got:\n%s", out)
	}
	m = pressKeys(m, keyMsg("enter"))
	if m.sessionFilter != "alp" || m.rootComp().raw(&ctx{m: &m}) {
		t.Fatalf("enter should keep the filter and close the prompt: filter=%q", m.sessionFilter)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "/alp") {
		t.Errorf("the title should show the filter, got:\n%s", out)
	}
	m = pressKeys(m, keyMsg("esc"))
	if m.sessionFilter != "" || len(m.order) != 3 || m.focused != mainPane {
		t.Errorf("esc on a filtered list should clear the filter first: filter=%q order=%v focus=%v",
			m.sessionFilter, m.order, m.focused)
	}
	if got := m.rootSessionID(); got != "n1:s3" {
		t.Errorf("clearing the filter should keep the cursor on its session: %q", got)
	}
}

func TestFilterEscInPromptClears(t *testing.T) {
	m := filterTestModel()
	m = pressKeys(m, keyMsg("/"), keyMsg("b"), keyMsg("esc"))
	if m.sessionFilter != "" || len(m.order) != 3 || m.rootComp().raw(&ctx{m: &m}) {
		t.Errorf("esc in the prompt should clear the filter: filter=%q order=%v", m.sessionFilter, m.order)
	}
}

func TestFilterWithNoMatchKeepsTheList(t *testing.T) {
	m := filterTestModel()
	m.sessionFilter = "zzz"
	m.reorder()
	if got := m.rootComp().fullScreen(&ctx{m: &m}); got != notFull {
		t.Errorf("an empty filtered list must not show the welcome splash: %v", got)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "no sessions match /zzz") {
		t.Errorf("want the filtered empty state, got:\n%s", out)
	}
}

func TestFilterInWorkspace(t *testing.T) {
	m := filterTestModel()
	m.main = backStack{workspaceComp{ws: "n1:w1"}}
	m = pressKeys(m, keyMsg("/"), keyMsg("b"), keyMsg("enter"))
	if ss := m.wsSessions("n1:w1"); len(ss) != 1 || ss[0].ID != "n1:s2" {
		t.Errorf("/ on a workspace pane: sessions=%v, want only n1:s2", ss)
	}
	if m.left.tree.filter != "" {
		t.Errorf("/ on the pane must not filter the tree: %q", m.left.tree.filter)
	}

	m = filterTestModel()
	m.main = backStack{workspaceComp{ws: "n1:w1"}}
	m = withFocus(m, leftSidebar)
	m = pressKeys(m, keyMsg("/"), keyMsg("b"), keyMsg("enter"))
	if m.sessionFilter != "" || m.left.tree.filter != "b" {
		t.Errorf("/ on the tree should filter the tree: session filter=%q tree filter=%q",
			m.sessionFilter, m.left.tree.filter)
	}
}

func TestPaneHeadStyleShowsFocus(t *testing.T) {
	m := homeTestModel()
	m, _ = m.enterSession("n1:s1")
	m = withFocus(m, mainPane)
	if got := m.paneHeadStyle().GetForeground(); got != ColorAccent {
		t.Errorf("a focused pane's title should use the accent, got %v", got)
	}
	for _, k := range []container{sessionDock, leftSidebar} {
		m = withFocus(m, k)
		if got := m.paneHeadStyle().GetForeground(); got == ColorAccent {
			t.Errorf("focus on %v: the pane's title should not use the accent", k)
		}
	}
}
