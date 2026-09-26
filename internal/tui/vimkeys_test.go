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
	m.projects.cursor = 2
	m = typeKeys(m, "gg")
	if m.projects.cursor != 0 {
		t.Errorf("gg: cursor = %d, want 0", m.projects.cursor)
	}
}

func TestVimGThenTimeoutDoesNothing(t *testing.T) {
	m := projectsTestModel()
	m.projects.cursor = 2
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyTimeoutMsg{gen: m.keyGen})
	if m.projects.cursor != 2 || len(m.keyBuf) != 0 {
		t.Errorf("g then the timeout: cursor = %d buf = %d, want 2 and empty", m.projects.cursor, len(m.keyBuf))
	}
}

func TestVimZDotAndZGToggleProjects(t *testing.T) {
	m := typeKeys(projectsTestModel(), "z.")
	if !m.projects.showHidden || m.projects.showGone {
		t.Errorf("z.: hidden = %v gone = %v, want only hidden", m.projects.showHidden, m.projects.showGone)
	}
	m = typeKeys(m, "zg")
	if !m.projects.showGone {
		t.Error("zg should show gone projects")
	}
}

func TestVimAOpensCreatePicker(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:p1")
	m = typeKeys(m, "a")
	if !m.projects.create.active || m.projects.create.projectID != "n1:p1" {
		t.Errorf("a: picker active = %v project = %q, want it open on n1:p1", m.projects.create.active, m.projects.create.projectID)
	}
}

func TestVimDDRemovesWorkspace(t *testing.T) {
	m := projectsTestModel()
	delete(m.sessions, "n1:s2")
	m.projects.selectRow("n1:w2")
	m = typeKeys(m, "dd")
	if m.projects.pendingRemove != "n1:w2" {
		t.Fatalf("dd: pendingRemove = %q, want n1:w2", m.projects.pendingRemove)
	}
	m, cmd := upd(m, keyMsg("y"))
	if m.projects.pendingRemove != "" || cmd == nil {
		t.Errorf("y after dd: pendingRemove = %q cmd = %v, want the remove to run", m.projects.pendingRemove, cmd != nil)
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
	if m.mode != modeProjects || m.projects.focus != focusTree {
		t.Errorf("<Esc> on Home: mode = %v focus = %v, want the tree", m.mode, m.projects.focus)
	}
}

func TestVimQDoesNothingOnHome(t *testing.T) {
	m, cmd := upd(homeTestModel(), keyMsg("q"))
	if m.mode != modeList || m.projects.focus != focusPane || quits(cmd) {
		t.Errorf("q on Home: mode = %v focus = %v quit = %v, want nothing", m.mode, m.projects.focus, quits(cmd))
	}
}

func TestVimLeaderToggles(t *testing.T) {
	m := projectsTestModel()
	m = typeKeys(m, " o")
	if !m.projects.sidebarHidden {
		t.Error("<Space>o should hide the tree")
	}
	m = wideWorkspace()
	if m = typeKeys(m, " e"); !m.projects.filesHidden {
		t.Error("<Space>e should hide the right sidebar")
	}
}

func TestVimTranscriptCardKeys(t *testing.T) {
	m := loaded()
	m.height = 40
	if m = typeKeys(m, "}"); m.transcript.cursor != 1 {
		t.Errorf("}: cursor = %d, want 1", m.transcript.cursor)
	}
	if m = typeKeys(m, "{"); m.transcript.cursor != 0 {
		t.Errorf("{: cursor = %d, want 0", m.transcript.cursor)
	}
}

func TestVimTranscriptJScrolls(t *testing.T) {
	m := loaded()
	m.height = 6
	m = typeKeys(m, "j")
	if m.transcript.scroll == 0 || m.transcript.cursor != 0 {
		t.Errorf("j: scroll = %d cursor = %d, want a scroll and no cursor move", m.transcript.scroll, m.transcript.cursor)
	}
	m = typeKeys(m, "k")
	if m.transcript.scroll != 0 {
		t.Errorf("k: scroll = %d, want 0", m.transcript.scroll)
	}
}

func TestVimTranscriptCollapseExpand(t *testing.T) {
	for _, keys := range [][2]string{{"h", "l"}, {"zc", "zo"}} {
		m := loaded()
		m.transcript.cursor = 1
		open := func() bool { return m.chunkExpanded(m.transcript.chunks[1]) }
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
	m := detailTestModel(transcript.Chunk{ID: "a", Kind: transcript.ChunkAI, Items: []transcript.Item{
		{Kind: transcript.ItemText, Text: "hi"}, {Kind: transcript.ItemTool, ToolName: "Read", ToolID: "t1"},
	}})
	m.width, m.height = 80, 30
	m.toolBodies = map[string]toolBodyEntry{}
	m.topFrame().cursor = 1
	m, cmd := upd(m, keyMsg("l"))
	if !m.topFrame().isExpanded(1) || cmd == nil {
		t.Errorf("l should expand the selected node and fetch its body: fetch = %v", cmd != nil)
	}
	if m = typeKeys(m, "l"); !m.topFrame().isExpanded(1) {
		t.Error("l on an expanded node should keep it expanded")
	}
	for range 2 {
		if m = typeKeys(m, "h"); m.topFrame().isExpanded(1) {
			t.Error("h should collapse the selected node")
		}
	}
}

func TestVimTabKeysSwitchHomeTabs(t *testing.T) {
	m := typeKeys(homeTestModel(), "gt")
	if m.mode != modeHistoryProjects {
		t.Fatalf("gt on Home: mode = %v, want History", m.mode)
	}
	if m = typeKeys(m, "gT"); m.mode != modeList {
		t.Errorf("gT on History: mode = %v, want Home", m.mode)
	}
}

func TestVimTabKeysSwitchSidebarTabs(t *testing.T) {
	m := filesFocused()
	m.client = &recordingClient{}
	if m = typeKeys(m, "gt"); m.projects.sideTab != sideChanges {
		t.Fatalf("gt: sideTab = %v, want Changes", m.projects.sideTab)
	}
	if m = typeKeys(m, "gT"); m.projects.sideTab != sideFiles {
		t.Errorf("gT: sideTab = %v, want Files", m.projects.sideTab)
	}
}

func TestVimDiffFileKeys(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go"}, api.ChangedFile{Path: "b.go"})
	m, _ = upd(m, keyMsg("enter"))
	if m = typeKeys(m, "]f"); m.projects.fileView.path != "b.go" {
		t.Fatalf("]f: path = %q, want b.go", m.projects.fileView.path)
	}
	if m = typeKeys(m, "[f"); m.projects.fileView.path != "a.go" {
		t.Errorf("[f: path = %q, want a.go", m.projects.fileView.path)
	}
}

func TestVimYowTogglesWrap(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go", Change: "modified"})
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, wsDiffMsg{ws: "n1:w1", path: "a.go", diff: "@@ -1 +1 @@\n+" + strings.Repeat("x", 300) + "TAIL"})
	m = typeKeys(m, "yow")
	if out := ansi.Strip(m.View().Content); !m.projects.fileView.wrap || !strings.Contains(out, "TAIL") {
		t.Errorf("yow should wrap long lines: wrap = %v", m.projects.fileView.wrap)
	}
}

func TestVimGRRefreshes(t *testing.T) {
	m := projectsTestModel()
	m.projects.err = errors.New("old")
	m, _ = upd(m, keyMsg("g"))
	m, cmd := upd(m, keyMsg("r"))
	if m.projects.err != nil || cmd == nil {
		t.Errorf("gr: err = %v cmd = %v, want a reload", m.projects.err, cmd != nil)
	}
}

func TestVimRRenamesProject(t *testing.T) {
	m := projectsTestModel()
	m.projects.selectRow("n1:p1")
	m = typeKeys(m, "r")
	if m.projects.inputMode != pmRename {
		t.Errorf("r: input mode = %v, want the rename input", m.projects.inputMode)
	}
}

func TestVimGQuestionOpensHelp(t *testing.T) {
	if m := typeKeys(projectsTestModel(), "g?"); !m.projects.showHelp {
		t.Error("g? should open the help")
	}
	if m := typeKeys(projectsTestModel(), "?"); m.projects.showHelp {
		t.Error("? alone should not open the help")
	}
}

func TestVimURemovesRedaction(t *testing.T) {
	m := newRedactModel()
	m.redact.literals = []string{"aaa", "bbb"}
	m = typeKeys(m, "D")
	if !m.redact.listActive {
		t.Fatal("D should open the redaction list")
	}
	if m = typeKeys(m, "u"); len(m.redact.literals) != 1 || m.redact.literals[0] != "bbb" {
		t.Errorf("u: literals = %v, want [bbb]", m.redact.literals)
	}
}

func sessionWithTerminal() model {
	m := testModel()
	m.client = &recordingClient{}
	m.sessions = map[string]session.Session{
		"oc": {ID: "oc", Agent: "opencode", Status: session.StatusAwaitingInput, CanOpenTerminal: true, Frontend: session.FrontendExternal},
	}
	m.selectedID = "oc"
	m.mode = modeSession
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
	m.redact.literals = []string{"aaa", "bbb"}
	m = typeKeys(m, "D")
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m = typeKeys(m, "u"); len(m.redact.literals) != 1 || m.redact.literals[0] != "aaa" {
		t.Errorf("<Down> then u: literals = %v, want [aaa]", m.redact.literals)
	}
}

func TestVimQQuitsFromProjectsPane(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.projects.selectRow("n1:w1")
	m.projects.focus = focusPane
	if _, cmd := upd(m, keyMsg("Q")); !quits(cmd) {
		t.Error("Q with the projects pane focused should quit")
	}
}
