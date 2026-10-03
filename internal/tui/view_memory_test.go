package tui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func baseWorkspace(m model) string {
	p, _ := m.baseComp().(workspaceComp)
	return p.ws
}

func TestTreeCursorMovesNeverChangeTheMainPane(t *testing.T) {
	m := wideWorkspace() // the tree has focus on n1:w1, the main pane shows n1:w1
	for _, keys := range []string{"j", "kk", "k"} {
		m = typeKeys(m, keys)
		if baseWorkspace(m) != "n1:w1" {
			t.Fatalf("after %q: base = %#v, want the pane of n1:w1", keys, m.baseComp())
		}
	}
}

func TestEnterOpensTheRowAndFocusesTheMainPane(t *testing.T) {
	m := pressKeys(typeKeys(wideWorkspace(), "j"), keyMsg("enter"))
	if baseWorkspace(m) != "n1:w2" || m.focused != mainPane {
		t.Errorf("base = %#v focus = %v, want the pane of n1:w2 with focus", m.baseComp(), m.focused)
	}
}

func TestRightSidebarFollowsTheMainPaneNotTheCursor(t *testing.T) {
	m := typeKeys(wideWorkspace(), "j")
	if m.right.fileTree.ws != "n1:w1" {
		t.Errorf("cursor on n1:w2: files show %q, want n1:w1 (the main pane's)", m.right.fileTree.ws)
	}
	m = pressKeys(m, keyMsg("enter"))
	if m.right.fileTree.ws != "n1:w2" {
		t.Errorf("after enter: files show %q, want n1:w2", m.right.fileTree.ws)
	}
}

func TestTreeTakesFocusAboveATranscript(t *testing.T) {
	m := pressKeys(workspaceSession(nil), cw('h')...)
	if m.focused != leftSidebar || trOf(m).sessionID != "n1:s1" {
		t.Fatalf("<C-w>h: focus = %v session = %q, want the tree above n1:s1", m.focused, trOf(m).sessionID)
	}
	if id := m.left.tree.cursorRowID(); id != "n1:w1" {
		t.Errorf("cursor on %q, want n1:w1 (the row of the open session)", id)
	}
}

func TestLeavingTheTreeMovesTheCursorToTheRowOfTheMainPane(t *testing.T) {
	for _, leave := range [][]tea.KeyPressMsg{cw('l'), cw('w')} {
		m := pressKeys(workspaceSession(nil), cw('h')...)
		m = typeKeys(m, "j")
		if id := m.left.tree.cursorRowID(); id != "n1:w2" {
			t.Fatalf("j: cursor on %q, want n1:w2", id)
		}
		m = pressKeys(m, leave...)
		if m.focused == leftSidebar || trOf(m).sessionID != "n1:s1" {
			t.Fatalf("%v: focus = %v session = %q, want n1:s1 off the tree", leave, m.focused, trOf(m).sessionID)
		}
		if id := m.left.tree.cursorRowID(); id != "n1:w1" {
			t.Errorf("%v: cursor on %q, want n1:w1 (the row of the open session)", leave, id)
		}
	}
}

func TestAWorkspaceReopensItsSession(t *testing.T) {
	var chunks []transcript.Chunk
	for i := range 40 {
		chunks = append(chunks, userChunk(fmt.Sprintf("u%d", i), "line"))
	}
	m, _ := upd(workspaceSession(nil), transcriptMsg{id: "n1:s1", chunks: chunks})
	m = withTr(m, func(tr *transcriptComp) { tr.transcript.scroll = 0 })
	m = pressKeys(m, cw('h')...)
	m = pressKeys(typeKeys(m, "j"), keyMsg("enter"))
	if baseWorkspace(m) != "n1:w2" {
		t.Fatalf("enter on n1:w2: base = %#v", m.baseComp())
	}
	m = pressKeys(m, cw('h')...)
	m = pressKeys(typeKeys(m, "k"), keyMsg("enter"))
	tr := trOf(m)
	if !tr.live || tr.sessionID != "n1:s1" || m.focused != mainPane {
		t.Fatalf("back on n1:w1: session = %q focus = %v, want n1:s1 with focus", tr.sessionID, m.focused)
	}
	m, _ = upd(m, transcriptMsg{id: "n1:s1", chunks: chunks})
	if scroll, end := trOf(m).transcript.scroll, tvIn(m).maxScroll(); end == 0 || scroll != end {
		t.Errorf("reopened transcript scroll = %d, want its end %d (> 0)", scroll, end)
	}
	if returnView(m) != viewTree {
		t.Error("back from the reopened session should show the pane of n1:w1")
	}
}

func TestAnEndedSessionStaysRememberedUntilTheRegistryRemovesIt(t *testing.T) {
	m := pressKeys(workspaceSession(nil), cw('h')...)
	m = pressKeys(typeKeys(m, "j"), keyMsg("enter"))
	s := m.sessions["n1:s1"]
	s.Status = session.StatusDead
	m.sessions["n1:s1"] = s
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("k"), keyMsg("enter"))
	if trOf(m).sessionID != "n1:s1" {
		t.Fatalf("an ended session should reopen, got base %#v", m.baseComp())
	}
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("j"), keyMsg("enter"))
	var rest []session.Session
	for id, s := range m.sessions {
		if id != "n1:s1" {
			rest = append(rest, s)
		}
	}
	m, _ = upd(m, sessionsReplacedMsg(rest))
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("k"), keyMsg("enter"))
	if baseWorkspace(m) != "n1:w1" {
		t.Errorf("after the registry removed n1:s1: base = %#v, want the pane of n1:w1", m.baseComp())
	}
}

func TestASessionOpenedFromHomeBelongsToItsWorkspace(t *testing.T) {
	m := homeTestModel()
	m.width = 160
	m = pressKeys(m, keyMsg("enter")) // opens n1:s1 over Home
	m = pressKeys(m, cw('h')...)
	if id := m.left.tree.cursorRowID(); id != "n1:w1" {
		t.Fatalf("cursor on %q, want n1:w1", id)
	}
	m = pressKeys(typeKeys(m, "kkk"), keyMsg("enter"))
	if viewOf(m) != viewHome {
		t.Fatalf("enter on Home: view = %v, want Home (Home does not remember n1:s1)", viewOf(m))
	}
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("j"), keyMsg("j"), keyMsg("j"), keyMsg("enter"))
	if trOf(m).sessionID != "n1:s1" {
		t.Errorf("enter on n1:w1: base = %#v, want n1:s1", m.baseComp())
	}
}

func TestEnterOnAProjectRowOpensItsSummaryAndDoesNotFold(t *testing.T) {
	m := pressKeys(typeKeys(wideWorkspace(), "k"), keyMsg("enter"))
	s, ok := m.baseComp().(summaryComp)
	if !ok || s.id != "n1:p1" || m.focused != mainPane {
		t.Fatalf("base = %#v focus = %v, want the summary of n1:p1 with focus", m.baseComp(), m.focused)
	}
	if len(m.left.tree.rows) != 5 {
		t.Errorf("enter folded the project: %d rows, want 5", len(m.left.tree.rows))
	}
	m = typeKeys(pressKeys(m, cw('h')...), "h")
	if len(m.left.tree.rows) != 3 {
		t.Errorf("h on the project should fold it: %d rows, want 3", len(m.left.tree.rows))
	}
}

func TestTheSummaryStaysUntilEnterOpensAnotherView(t *testing.T) {
	m := pressKeys(typeKeys(wideWorkspace(), "k"), keyMsg("enter"))
	m = typeKeys(pressKeys(m, cw('h')...), "jj")
	if _, ok := m.baseComp().(summaryComp); !ok {
		t.Errorf("base = %#v, want the summary to stay", m.baseComp())
	}
	m = pressKeys(pressKeys(m, cw('l')...), cw('h')...)
	if id := m.left.tree.cursorRowID(); id != "n1:p1" {
		t.Errorf("focus back into the tree: cursor on %q, want n1:p1", id)
	}
}

func TestBackOutOfASessionShowsTheWorkspaceListOnReturn(t *testing.T) {
	m := pressKeys(workspaceSession(nil), keyMsg("esc"))
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("j"), keyMsg("enter"))
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("k"), keyMsg("enter"))
	if baseWorkspace(m) != "n1:w1" {
		t.Errorf("base = %#v, want the pane of n1:w1: back forgot n1:s1", m.baseComp())
	}
}

func TestEnterOnTheShownRowOnlyMovesFocus(t *testing.T) {
	m := withTr(workspaceSession(nil), func(tr *transcriptComp) { tr.transcript.scroll = 5 })
	sub := trOf(m).activeSub.subID
	m = pressKeys(pressKeys(m, cw('h')...), keyMsg("enter"))
	if tr := trOf(m); tr.transcript.scroll != 5 || tr.activeSub.subID != sub || m.focused != mainPane {
		t.Errorf("scroll = %d sub = %q focus = %v, want the same transcript with focus", tr.transcript.scroll, tr.activeSub.subID, m.focused)
	}
}

func TestFocusIntoTheTreeUnfoldsTheRowOfTheMainPane(t *testing.T) {
	m := pressKeys(workspaceSession(nil), cw('h')...)
	m = typeKeys(m, "kh") // fold n1:p1
	m = pressKeys(pressKeys(m, cw('l')...), cw('h')...)
	if id := m.left.tree.cursorRowID(); id != "n1:w1" {
		t.Errorf("cursor on %q, want n1:w1 with its project unfolded", id)
	}
}

func TestFollowDoesNotUnfoldWhenFilterHidesTheRow(t *testing.T) {
	m := pressKeys(workspaceSession(nil), cw('h')...) // tree focused, cursor on n1:w1
	m = typeKeys(m, "kh")                             // fold n1:p1
	// "feat" matches n1:w2 (branch "feature") but not n1:w1 (branch "main")
	m.left.tree.setFilter("feat")
	cursorBefore := m.left.tree.cursor
	m = pressKeys(pressKeys(m, cw('l')...), cw('h')...) // focus pane, then tree
	if m.left.tree.cursor != cursorBefore {
		t.Errorf("cursor moved: before=%d after=%d, want no move when filter hides the row", cursorBefore, m.left.tree.cursor)
	}
	m.left.tree.setFilter("")
	if !m.left.tree.isFolded("n1:p1") {
		t.Error("project should still be folded: follow must not unfold when filter hides the row")
	}
}

func TestFilterInTheTreeKeepsTheCursor(t *testing.T) {
	m := typeKeys(wideWorkspace(), "j/") // cursor on n1:w2, the main pane on n1:w1
	if !m.inputActive() {
		t.Fatal("/ should open the filter")
	}
	if id := m.left.tree.cursorRowID(); id != "n1:w2" {
		t.Errorf("cursor on %q, want n1:w2: / inside the tree must not move it", id)
	}
}

func TestARowThatLeavesTheRegistryGivesWayToHome(t *testing.T) {
	m := pressKeys(wideWorkspace(), keyMsg("enter"))
	p := m.left.tree.data[0]
	p.Workspaces = p.Workspaces[1:] // n1:w1 leaves
	m, _ = upd(m, projectsTreeMsg{tree: []api.ProjectNode{p}})
	if viewOf(m) != viewHome {
		t.Errorf("view = %v, want Home after n1:w1 left", viewOf(m))
	}
}

func TestARowThatLeavesTheRegistryKeepsTheFocus(t *testing.T) {
	m := wideWorkspace() // the tree has focus, the main pane shows n1:w1
	m.memory.home = homeEntry{view: homeSession, session: "n1:s1"}
	p := m.left.tree.data[0]
	p.Workspaces = p.Workspaces[1:] // n1:w1 leaves
	m, _ = upd(m, projectsTreeMsg{tree: []api.ProjectNode{p}})
	if tr, ok := m.baseComp().(transcriptComp); !ok || tr.sessionID != "n1:s1" {
		t.Fatalf("base = %#v, want n1:s1, which Home remembers", m.baseComp())
	}
	if m.focused != leftSidebar {
		t.Errorf("focus = %v, want the tree to keep it", m.focused)
	}
}

func TestFailedProjectLoadKeepsTheMemoryAndThePane(t *testing.T) {
	m := pressKeys(wideWorkspace(), keyMsg("enter"))
	m, _ = upd(m, projectsTreeMsg{tree: m.left.tree.data})
	m, _ = upd(m, projectsTreeMsg{err: errors.New("down")})
	if baseWorkspace(m) != "n1:w1" {
		t.Errorf("base = %#v, want the pane of n1:w1 to stay after a failed load", m.baseComp())
	}
}

func TestEscWithNoTreeOpensTheHomeEntry(t *testing.T) {
	m := pressKeys(wideWorkspace(), keyMsg("enter"))
	m.memory.home = homeEntry{view: homeHistory}
	m.left.hidden = true
	m = pressKeys(m, keyMsg("esc"))
	if viewOf(m) != viewHistoryProjects {
		t.Errorf("view = %v, want History (the Home entry)", viewOf(m))
	}
}

func TestPaneCycleKeepsTheTranscript(t *testing.T) {
	m := workspaceSession(&session.Interaction{Kind: session.InteractionPermission})
	m = pressKeys(m, cw('W')...)
	if m.focused != leftSidebar || trOf(m).sessionID != "n1:s1" {
		t.Errorf("focus = %v session = %q, want the tree above n1:s1", m.focused, trOf(m).sessionID)
	}
	if !m.dockShown() {
		t.Error("dock should show while the tree has focus above a session with a permission prompt")
	}
}

func TestHiddenTreeRepairKeepsTheMainPane(t *testing.T) {
	m := typeKeys(wideWorkspace(), " o")
	if m.focused != mainPane || baseWorkspace(m) != "n1:w1" {
		t.Errorf("focus = %v base = %#v, want the pane of n1:w1 with focus", m.focused, m.baseComp())
	}
}
