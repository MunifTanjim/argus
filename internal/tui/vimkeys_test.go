package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func typeKeys(m model, s string) model {
	m, _ = typeKeysCmd(m, s)
	return m
}

func typeKeysCmd(m model, s string) (model, tea.Cmd) {
	var cmd tea.Cmd
	for _, r := range s {
		m, cmd = upd(m, keyMsg(string(r)))
	}
	return m, cmd
}

// seqKey is the key press the resolver hands a handler for the sequence in
// Vim notation, for tests that call a handler directly.
func seqKey(notation string) tea.KeyPressMsg {
	seq, err := parseKeySeq(notation)
	if err != nil || len(seq) < 2 {
		panic("seqKey: " + notation)
	}
	return tea.KeyPressMsg{Code: tea.KeyExtended, Text: seq.id()}
}

func TestVimGGGoesToTop(t *testing.T) {
	m := projectsTestModel()
	m = moveTree(m, 2)
	m = typeKeys(m, "gg")
	if m.left.tree.cursor != 0 {
		t.Errorf("gg: cursor = %d, want 0", m.left.tree.cursor)
	}
}

func TestVimGThenTimeoutDoesNothing(t *testing.T) {
	m := projectsTestModel()
	m = moveTree(m, 2)
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyTimeoutMsg{gen: m.keyGen})
	if m.left.tree.cursor != 2 || len(m.keyBuf) != 0 {
		t.Errorf("g then the timeout: cursor = %d buf = %d, want 2 and empty", m.left.tree.cursor, len(m.keyBuf))
	}
}

func TestVimZDotAndZGToggleProjects(t *testing.T) {
	m := typeKeys(projectsTestModel(), "z.")
	if !m.left.tree.showHidden || m.left.tree.showGone {
		t.Errorf("z.: hidden = %v gone = %v, want only hidden", m.left.tree.showHidden, m.left.tree.showGone)
	}
	m = typeKeys(m, "zg")
	if !m.left.tree.showGone {
		t.Error("zg should show gone projects")
	}
}

func TestVimAOpensCreatePicker(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	m = typeKeys(m, "a")
	if !createOpen(m) || createOf(m).projectID != "n1:p1" {
		t.Errorf("a: picker active = %v project = %q, want it open on n1:p1", createOpen(m), createOf(m).projectID)
	}
}

func TestVimDDRemovesWorkspace(t *testing.T) {
	m := projectsTestModel()
	delete(m.sessions, "n1:s2")
	m = selectRow(m, "n1:w2")
	m = typeKeys(m, "dd")
	if m.left.tree.pendingRemove != "n1:w2" {
		t.Fatalf("dd: pendingRemove = %q, want n1:w2", m.left.tree.pendingRemove)
	}
	m, cmd := upd(m, keyMsg("y"))
	if m.left.tree.pendingRemove != "" || cmd == nil {
		t.Errorf("y after dd: pendingRemove = %q cmd = %v, want the remove to run", m.left.tree.pendingRemove, cmd != nil)
	}
}

func TestVimQQuitsFromHomeWithTree(t *testing.T) {
	m := homeTestModel()
	if !m.sidebarVisible() {
		t.Fatal("setup: the tree should be visible")
	}
	if _, cmd := upd(m, keyMsg("Q")); !quits(cmd) {
		t.Error("Q on Home should quit")
	}
}

func TestVimEscOnHomeFocusesTree(t *testing.T) {
	m, _ := upd(homeTestModel(), keyMsg("esc"))
	if viewOf(m) != viewTree || m.focused != leftSidebar {
		t.Errorf("<Esc> on Home: view = %v focus = %v, want the tree", viewOf(m), m.focused)
	}
}

func TestVimQDoesNothingOnHome(t *testing.T) {
	m, cmd := upd(homeTestModel(), keyMsg("q"))
	if viewOf(m) != viewHome || m.focused != mainPane || quits(cmd) {
		t.Errorf("q on Home: view = %v focus = %v quit = %v, want nothing", viewOf(m), m.focused, quits(cmd))
	}
}

func TestVimLeaderToggles(t *testing.T) {
	m := projectsTestModel()
	m = typeKeys(m, " o")
	if !m.left.hidden {
		t.Error("<Space>o should hide the tree")
	}
	m = wideWorkspace()
	if m = typeKeys(m, " e"); !m.right.hidden {
		t.Error("<Space>e should hide the right sidebar")
	}
}

func TestVimTranscriptCardKeys(t *testing.T) {
	m := loaded()
	m.height = 40
	if m = typeKeys(m, "}"); trOf(m).transcript.cursor != 1 {
		t.Errorf("}: cursor = %d, want 1", trOf(m).transcript.cursor)
	}
	if m = typeKeys(m, "{"); trOf(m).transcript.cursor != 0 {
		t.Errorf("{: cursor = %d, want 0", trOf(m).transcript.cursor)
	}
}

func TestVimTranscriptJScrolls(t *testing.T) {
	m := loaded()
	m.height = 6
	m = typeKeys(m, "j")
	if trOf(m).transcript.scroll == 0 || trOf(m).transcript.cursor != 0 {
		t.Errorf("j: scroll = %d cursor = %d, want a scroll and no cursor move", trOf(m).transcript.scroll, trOf(m).transcript.cursor)
	}
	m = typeKeys(m, "k")
	if trOf(m).transcript.scroll != 0 {
		t.Errorf("k: scroll = %d, want 0", trOf(m).transcript.scroll)
	}
}

func TestVimTranscriptCollapseExpand(t *testing.T) {
	for _, keys := range [][2]string{{"h", "l"}, {"zc", "zo"}} {
		m := loaded()
		m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 1 })
		open := func() bool { return tvOf(&m).chunkExpanded(trOf(m).transcript.chunks[1]) }
		for range 2 {
			if m = typeKeys(m, keys[1]); !open() {
				t.Errorf("%s should expand the selected card", keys[1])
			}
		}
		for range 2 {
			if m = typeKeys(m, keys[0]); open() {
				t.Errorf("%s should collapse the selected card", keys[0])
			}
		}
	}
}

func TestVimDetailCollapseExpand(t *testing.T) {
	v := detailTestModel(transcript.Chunk{ID: "a", Kind: transcript.ChunkAI, Items: []transcript.Item{
		{Kind: transcript.ItemText, Text: "hi"}, {Kind: transcript.ItemTool, ToolName: "Read", ToolID: "t1"},
	}})
	v.width, v.height = 80, 30
	v.toolBodies = map[string]toolBodyEntry{}
	v.topFrame().cursor = 1
	v.put()
	m, cmd := upd(*v.model, keyMsg("l"))
	if !tvOf(&m).topFrame().isExpanded(1) || cmd == nil {
		t.Errorf("l should expand the selected node and fetch its body: fetch = %v", cmd != nil)
	}
	if m = typeKeys(m, "l"); !tvOf(&m).topFrame().isExpanded(1) {
		t.Error("l on an expanded node should keep it expanded")
	}
	for range 2 {
		if m = typeKeys(m, "h"); tvOf(&m).topFrame().isExpanded(1) {
			t.Error("h should collapse the selected node")
		}
	}
}

func TestVimTabKeysSwitchHomeTabs(t *testing.T) {
	m := typeKeys(homeTestModel(), "gt")
	if viewOf(m) != viewHistoryProjects {
		t.Fatalf("gt on Home: view = %v, want History", viewOf(m))
	}
	if m = typeKeys(m, "gT"); viewOf(m) != viewHome {
		t.Errorf("gT on History: view = %v, want Home", viewOf(m))
	}
}

func TestVimTabKeysSwitchSidebarTabs(t *testing.T) {
	m := filesFocused()
	m.client = &recordingClient{}
	if m = typeKeys(m, "gt"); m.right.tab != sideChanges {
		t.Fatalf("gt: sideTab = %v, want Changes", m.right.tab)
	}
	if m = typeKeys(m, "gT"); m.right.tab != sideFiles {
		t.Errorf("gT: sideTab = %v, want Files", m.right.tab)
	}
}

func TestVimDiffFileKeys(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go"}, api.ChangedFile{Path: "b.go"})
	m, _ = upd(m, keyMsg("enter"))
	if m = typeKeys(m, "]f"); fileOf(m).path != "b.go" {
		t.Fatalf("]f: path = %q, want b.go", fileOf(m).path)
	}
	if m = typeKeys(m, "[f"); fileOf(m).path != "a.go" {
		t.Errorf("[f: path = %q, want a.go", fileOf(m).path)
	}
}

func TestVimYowTogglesWrap(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go", Change: "modified"})
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, wsDiffMsg{ws: "n1:w1", path: "a.go", diff: "@@ -1 +1 @@\n+" + strings.Repeat("x", 300) + "TAIL"})
	m = typeKeys(m, "yow")
	if out := ansi.Strip(m.View().Content); !fileOf(m).wrap || !strings.Contains(out, "TAIL") {
		t.Errorf("yow should wrap long lines: wrap = %v", fileOf(m).wrap)
	}
}

func TestVimGRRefreshes(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.err = errors.New("old")
	m, _ = upd(m, keyMsg("g"))
	m, cmd := upd(m, keyMsg("r"))
	if m.left.tree.err != nil || cmd == nil {
		t.Errorf("gr: err = %v cmd = %v, want a reload", m.left.tree.err, cmd != nil)
	}
}

func TestVimRRenamesProject(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	m = typeKeys(m, "r")
	if m.left.tree.inputMode != pmRename {
		t.Errorf("r: input mode = %v, want the rename input", m.left.tree.inputMode)
	}
}

func TestVimGQuestionOpensHelp(t *testing.T) {
	if m := typeKeys(projectsTestModel(), "g?"); !m.showHelp {
		t.Error("g? should open the help")
	}
	if m := typeKeys(projectsTestModel(), "?"); m.showHelp {
		t.Error("? alone should not open the help")
	}
}

func TestVimURemovesRedaction(t *testing.T) {
	m := newRedactModel()
	m = withTr(m, func(t *transcriptComp) { t.redact.literals = []string{"aaa", "bbb"} })
	m = typeKeys(m, "D")
	if !trOf(m).redact.listActive {
		t.Fatal("D should open the redaction list")
	}
	if m = typeKeys(m, "u"); len(trOf(m).redact.literals) != 1 || trOf(m).redact.literals[0] != "bbb" {
		t.Errorf("u: literals = %v, want [bbb]", trOf(m).redact.literals)
	}
}

func sessionWithTerminal() model {
	m := testModel()
	m.client = &recordingClient{}
	m.sessions = map[string]session.Session{
		"oc": {ID: "oc", Agent: "opencode", Status: session.StatusAwaitingInput, CanOpenTerminal: true, Frontend: session.FrontendExternal},
	}
	m = withLive(m, "oc")
	return m
}

func TestVimCtrlTOpensLiveScreen(t *testing.T) {
	m := sessionWithTerminal()
	_, cmd := upd(m, ctrlKey('t'))
	if cmd == nil {
		t.Fatal("<C-t> should open the live screen")
	}
	runCmd(cmd)
	if !slices.Contains(m.client.(*recordingClient).calledMethods(), api.MethodTerminalOpen) {
		t.Error("<C-t> should call terminal.open")
	}
}

func TestVimRemovedCommandsAreUnknown(t *testing.T) {
	for _, cmd := range []string{"expand-all-cards", "collapse-all-cards", "focus-right-sidebar", "focus-tree", "toggle-card-fold", "toggle-detail-fold"} {
		_, errs := buildKeymap(map[string]map[string]string{"global": {"<C-y>": cmd}}, time.Second, "")
		if len(errs) != 1 || !strings.Contains(errs[0], "unknown command") {
			t.Errorf("%s: errs = %v, want an unknown command", cmd, errs)
		}
	}
}

func TestVimRedactListArrowKeys(t *testing.T) {
	m := newRedactModel()
	m = withTr(m, func(t *transcriptComp) { t.redact.literals = []string{"aaa", "bbb"} })
	m = typeKeys(m, "D")
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m = typeKeys(m, "u"); len(trOf(m).redact.literals) != 1 || trOf(m).redact.literals[0] != "aaa" {
		t.Errorf("<Down> then u: literals = %v, want [aaa]", trOf(m).redact.literals)
	}
}

func TestVimQQuitsFromProjectsPane(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m = selectRow(m, "n1:w1")
	m = withFocus(m, mainPane)
	if _, cmd := upd(m, keyMsg("Q")); !quits(cmd) {
		t.Error("Q with the projects pane focused should quit")
	}
}
