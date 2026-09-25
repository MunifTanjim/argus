package tui

import (
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
	m.mode = modeList
	m.projects.focus = focusPane
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

func TestHomeModesRenderInsideProjectsFrame(t *testing.T) {
	for _, mode := range []viewMode{modeList, modeHistoryProjects, modeLogs} {
		m := homeTestModel()
		m.mode = mode
		if !m.embedded() || m.bodyWidth() != m.frameWidth()-m.projectsLeftW()-dividerWidth {
			t.Errorf("mode %v: embedded=%v bodyWidth=%d", mode, m.embedded(), m.bodyWidth())
		}
		out := m.View().Content
		if !strings.Contains(ansi.Strip(out), "Projects") {
			t.Errorf("mode %v: sidebar missing", mode)
		}
		assertFits(t, out, 120)
	}
}

func TestSessionFromHistoryEmbeds(t *testing.T) {
	m := homeTestModel()
	m.mode = modeHistoryTranscript
	mm, _ := m.enterSession("n1:s1")
	if mm.sessionReturn != modeHistoryTranscript || !mm.embedded() {
		t.Errorf("a session resumed from History should embed: return=%v embedded=%v", mm.sessionReturn, mm.embedded())
	}
}

func TestViewerNeverEmbeds(t *testing.T) {
	m := homeTestModel()
	m.viewer = true
	m.mode = modeHistoryTranscript
	if m.embedded() || m.bodyWidth() != 120 {
		t.Errorf("viewer: embedded=%v bodyWidth=%d, want full screen", m.embedded(), m.bodyWidth())
	}
}

func TestHiddenSidebarGivesHomeFullWidth(t *testing.T) {
	m := homeTestModel()
	m.projects.sidebarHidden = true
	if !m.embedded() || m.bodyWidth() != m.frameWidth() {
		t.Errorf("hidden sidebar: embedded=%v bodyWidth=%d, want framed at full width", m.embedded(), m.bodyWidth())
	}
}

func TestHomeRowIsFirstAndSurvivesFilter(t *testing.T) {
	m := homeTestModel()
	m.projects.rebuild()
	if r := m.projects.rows[0]; r.kind != rowHome || r.id != homeRowID {
		t.Fatalf("first row = %+v, want Home", r)
	}
	m.projects.setFilter("zzz")
	if len(m.projects.rows) != 1 || m.projects.rows[0].kind != rowHome {
		t.Errorf("a no-match filter should leave only Home: %+v", m.projects.rows)
	}
}

func TestHomeBadgeCountsAllLiveSessions(t *testing.T) {
	m := homeTestModel()
	m.sessions["n1:s9"] = session.Session{ID: "n1:s9", Status: session.StatusAwaitingInput} // no workspace
	m.projects.rebuild()
	line := ansi.Strip(m.projRowLine(m.projects.rows[0], false, true, m.workspaceActivity(), 40))
	if !strings.HasSuffix(line, "◆ 4") {
		t.Errorf("Home row = %q, want a ◆ 4 badge across every live session", line)
	}
}

func TestTreeShowsHomeWhileProjectsLoadOrFail(t *testing.T) {
	m := homeTestModel()
	m.projects.tree = nil
	m.projects.rebuild()
	tree := ansi.Strip(m.projectsTreePane(40, 20))
	if !strings.Contains(tree, "Home") || !strings.Contains(tree, "loading projects") {
		t.Errorf("loading tree = %q", tree)
	}
	m.projects.err = errString("registry disabled")
	tree = ansi.Strip(m.projectsTreePane(40, 20))
	if !strings.Contains(tree, "Home") || !strings.Contains(tree, "registry disabled") {
		t.Errorf("error tree = %q", tree)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Projects") {
		t.Error("the Home pane must still render next to the tree")
	}
}

func TestHomePreviewHasNoSelectedCard(t *testing.T) {
	m := homeTestModel()
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.rebuild()
	m.projects.cursor = 0 // Home
	preview := m.homePreview()
	if strings.Contains(preview, "┏") { // the selected card uses heavy chrome
		t.Error("the Home preview must not highlight a card")
	}
	if !strings.Contains(ansi.Strip(preview), "repo") {
		t.Error("the Home preview should show the session list")
	}
}

func TestEnterOnHomeFocusesHomePane(t *testing.T) {
	m := homeTestModel()
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.rebuild()
	m.projects.cursor = 0
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: 'l', Text: "l"}, {Code: tea.KeyTab}} {
		res, _ := m.handleProjectsKey(k)
		if mm := res.(model); mm.mode != modeList || mm.projects.focus != focusPane {
			t.Errorf("%q on Home: mode=%v focus=%v, want the Home pane", k.String(), mm.mode, mm.projects.focus)
		}
	}
}

func TestHomePaneKeysReturnToTree(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{{Code: 'q', Text: "q"}, {Code: tea.KeyEscape}, {Code: tea.KeyTab}} {
		m := homeTestModel()
		m.projects.rebuild()
		m.projects.cursor = 2 // a workspace, to prove we land on Home
		res, cmd := m.handleKey(k)
		mm := res.(model)
		if mm.mode != modeProjects || mm.projects.focus != focusTree || mm.projects.cursorRowID() != homeRowID {
			t.Errorf("%q: mode=%v focus=%v row=%q, want the tree on Home", k.String(), mm.mode, mm.projects.focus, mm.projects.cursorRowID())
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
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.rebuild()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q on the tree should quit")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("q on the tree should return tea.Quit")
	}
	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if mm := res.(model); mm.mode != modeProjects {
		t.Errorf("esc on the tree left the screen: mode=%v", mm.mode)
	}
}

func TestPKeyDoesNothing(t *testing.T) {
	m := homeTestModel()
	res, _ := m.handleKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if mm := res.(model); mm.mode != modeList {
		t.Errorf("p changed mode to %v", mm.mode)
	}
}

func TestNarrowTerminalHomeQuits(t *testing.T) {
	m := homeTestModel()
	m.width = 60 // sidebar auto-hides below 80
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q with no tree to return to should quit")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("want tea.Quit")
	}
	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if mm := res.(model); mm.mode != modeList {
		t.Errorf("esc with no tree changed mode to %v", mm.mode)
	}
}

func TestNewModelStartsOnHome(t *testing.T) {
	m := newModel(&recordingClient{}, true, nil)
	m.width, m.height = 120, 30
	if m.mode != modeList || m.projects.focus != focusPane || m.projects.cursorRowID() != homeRowID {
		t.Fatalf("start: mode=%v focus=%v row=%q, want the Home pane", m.mode, m.projects.focus, m.projects.cursorRowID())
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

func ctrlB() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl} }

func TestCtrlBWorksOutsideTheTree(t *testing.T) {
	m := homeTestModel() // Home pane
	res, _ := m.handleKey(ctrlB())
	m = res.(model)
	if m.sidebarVisible() {
		t.Fatal("ctrl+b in the Home pane should hide the sidebar")
	}
	res, _ = m.handleKey(ctrlB())
	m = res.(model)
	res, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if mm := res.(model); !mm.sidebarVisible() || mm.mode != modeProjects {
		t.Errorf("ctrl+b twice then esc should reach the tree: visible=%v mode=%v", mm.sidebarVisible(), mm.mode)
	}

	s := homeTestModel()
	ss, _ := s.enterSession("n1:s1") // opened from Home: sessionReturn = modeList
	res, _ = ss.handleKey(ctrlB())
	if res.(model).sidebarVisible() {
		t.Error("ctrl+b in a session opened from Home should hide the sidebar")
	}
}

func TestNoVisibleTreeEscGoesHome(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'q', Text: "q"}} {
		m := homeTestModel()
		m.mode = modeProjects
		m.projects.focus = focusTree
		m.projects.rebuild()
		m.projects.selectRow("n1:w1")
		m.width = 70 // tree auto-hidden
		res, _ := m.handleKey(k)
		if mm := res.(model); mm.mode != modeList {
			t.Errorf("%q with no visible tree: mode=%v, want the Home pane", k.String(), mm.mode)
		}
	}
}

func TestHiddenTreeOnHomeRowEntersHome(t *testing.T) {
	m := homeTestModel()
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.rebuild()
	m.projects.cursor = 0
	res, _ := m.handleKey(ctrlB())
	if mm := res.(model); mm.mode != modeList {
		t.Errorf("ctrl+b on the Home row: mode=%v, want the Home pane", mm.mode)
	}

	m.width = 70 // resized below the breakpoint while on the Home row
	res, _ = m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if mm := res.(model); mm.mode != modeList || mm.cursor != 1 {
		t.Errorf("a key on a hidden Home row should act in the Home pane: mode=%v cursor=%d", mm.mode, mm.cursor)
	}
}

func TestTreeStartedSpawnRendersInPane(t *testing.T) {
	m := homeTestModel()
	m.mode = modeProjects
	m.projects.rebuild()
	m.projects.selectRow("n1:w1")
	m.client = &recordingClient{}
	m.beginPresetSpawn("n1", "/repo", "")
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Projects") {
		t.Error("a spawn started from the tree should render in the pane, next to the tree")
	}
	assertFits(t, m.View().Content, 120)
}

func TestTreeReloadKeepsHomeWhileHomePaneShows(t *testing.T) {
	m := homeTestModel() // Home pane
	m.projects.rebuild()
	m.projects.cursor = 0
	m.projects.want = "n1:w2" // e.g. a workspace create finished meanwhile
	res, _ := m.Update(projectsTreeMsg{tree: m.projects.tree})
	if got := res.(model).projects.cursorRowID(); got != homeRowID {
		t.Errorf("tree reload moved the cursor to %q while the Home pane shows", got)
	}
}

func TestSpinnerRunsInFramedViews(t *testing.T) {
	m := homeTestModel()
	m.sessions["n1:s1"] = session.Session{ID: "n1:s1", WorkspaceID: "n1:w1", Status: session.StatusWorking}
	m.mode, m.sessionReturn = modeSession, modeList
	m.spinning = false
	if cmd := m.maybeSpin(); cmd == nil {
		t.Error("the sidebar badges should keep spinning in a framed session view")
	}
}

func TestFilteredTreeFooterStillShowsQuit(t *testing.T) {
	m := homeTestModel()
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.rebuild()
	m.projects.setFilter("argus")
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "clear filter") || !strings.Contains(f, "quit") {
		t.Errorf("filtered tree footer = %q, want both clear filter and quit", f)
	}
}

func TestListViewSurvivesPendingKillWithNoCursor(t *testing.T) {
	m := homeTestModel()
	m.pendingKill = true
	m.cursor = -1
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("listView panicked: %v", r)
		}
	}()
	_ = m.listView()
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
	pane.projects.rebuild()
	pane.projects.cursor = 0
	tree := pane
	tree.mode = modeProjects
	tree.projects.focus = focusTree

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
	if !strings.Contains(tl[len(tl)-1]+tl[len(tl)-2], "fold") {
		t.Error("tree-focused Home footer should show the tree's keys")
	}
}

func TestWorkspaceRowKeepsFrameHeader(t *testing.T) {
	m := homeTestModel()
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.rebuild()
	m.projects.selectRow("n1:w1")
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
	ws.mode = modeProjects
	ws.projects.focus = focusPane
	ws.projects.rebuild()
	ws.projects.selectRow("n1:w1")
	hc, wc := cardColumn(home.View().Content), cardColumn(ws.View().Content)
	if hc < 0 || hc != wc {
		t.Errorf("Home cards start at column %d, workspace cards at %d; want the same", hc, wc)
	}

}

func TestHiddenSidebarHomeMatchesWorkspace(t *testing.T) {
	home := homeTestModel()
	home.projects.sidebarHidden = true
	ws := homeTestModel()
	ws.projects.sidebarHidden = true
	ws.mode = modeProjects
	ws.projects.focus = focusPane
	ws.projects.rebuild()
	ws.projects.selectRow("n1:w1")

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
	if m.embedded() || m.bodyWidth() != m.width {
		t.Fatalf("empty Home should use the whole screen: embedded=%v bodyWidth=%d", m.embedded(), m.bodyWidth())
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
	if mm.mode != modeProjects || !strings.Contains(ansi.Strip(mm.View().Content), "Projects") {
		t.Errorf("esc from the splash should show the tree: mode=%v", mm.mode)
	}

	m.sessions = map[string]session.Session{"n1:s1": {ID: "n1:s1", WorkspaceID: "n1:w1", Repo: "repo"}}
	m.order = []string{"n1:s1"}
	if !m.embedded() {
		t.Error("with a session, Home should be framed again")
	}
}

func TestEmptyHomeRowFramesSplashInPane(t *testing.T) {
	m := homeTestModel()
	m.order, m.sessions = nil, map[string]session.Session{}
	m.projects.filesHidden = false
	res, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = res.(model)
	if !m.treeFocused() || !m.onHomeRow() {
		t.Fatalf("want the tree focused on Home: focus=%v row=%q", m.projects.focus, m.projects.cursorRowID())
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
	hidden.projects.sidebarHidden = true
	check("home, sidebar hidden", hidden)
	if want := screenMargin + (hidden.frameWidth()-maxCardWidth)/2; cardColumn(hidden.View().Content) != want {
		t.Errorf("hidden-sidebar cards start at column %d, want centered at %d", cardColumn(hidden.View().Content), want)
	}
	ws := homeTestModel()
	ws.mode = modeProjects
	ws.projects.focus = focusTree
	ws.projects.rebuild()
	ws.projects.selectRow("n1:w1")
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
	m.mode = modeProjects
	m.projects.focus = focusTree
	m.projects.rebuild()
	m.projects.selectRow("n1:p1") // cursor on the project row
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
	ws.mode = modeProjects
	ws.projects.focus = focusPane
	ws.projects.rebuild()
	ws.projects.selectRow("n1:w1")
	if c := cardColumn(ws.View().Content); c != wantCards {
		t.Errorf("workspace cards at column %d, want centered at %d", c, wantCards)
	}

	ws.projects.fileView = fileViewState{ws: "n1:w1", path: "a.go", diff: true, lines: []string{"@@ -1 +1 @@", "-a", "+bb"}}
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
	if !strings.HasSuffix(strings.TrimRight(bar, " "), "argus lock pin") {
		t.Errorf("status should be right-aligned: %q", bar)
	}
	assertFits(t, out, m.width)
	for _, ln := range lines[1:] {
		if strings.Contains(ln, "reconnecting") || strings.Contains(ln, "QUARANTINED") {
			t.Errorf("status repeated below the bar: %q", ln)
		}
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
