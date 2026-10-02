package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestBareSplashTabClick(t *testing.T) {
	m := withMouse(homeTestModel())
	m.sessions = nil
	m.order = nil
	if !m.layout().bare {
		t.Fatal("the splash must take the whole terminal")
	}
	y, x := findBlock(t, m, "History")
	m, _ = click(m, x, y)
	if _, ok := m.baseComp().(historyComp); !ok {
		t.Fatalf("base = %T, want the History tab", m.baseComp())
	}
}

func TestLogsTabClickSwitchesToSessions(t *testing.T) {
	m := withMouse(newModel(logsStubClient{}, false, logbuf.New(100)))
	m.left.hidden = true
	m.width, m.height = 80, 30
	m = withView(m, viewLogs)
	y, x := findBlock(t, m, "Sessions")
	m, _ = click(m, x, y)
	if _, ok := m.baseComp().(homeComp); !ok {
		t.Fatalf("base = %T, want the Sessions tab", m.baseComp())
	}
}

func TestHistorySessionsClickSelectsThenOpens(t *testing.T) {
	m := withMouse(homeTestModel())
	p := session.HistoryProject{Label: "a", Cwd: "/a", NodeID: "n1", ProjectDir: "a"}
	m = withHistorySessions(m, p, session.HistorySessionPage{Items: []session.HistorySession{
		{SessionID: "h1", Title: "first", TranscriptPath: "/a/h1.jsonl"}, {SessionID: "h2", Title: "second", TranscriptPath: "/a/h2.jsonl"},
	}})
	if !historyOf(m).inProject {
		t.Fatal("the history must show the project's sessions")
	}
	y, x := findBlock(t, m, "second")
	y += 2 // the card's last row catches an offset that is too small
	m, _ = click(m, x, y)
	if h := historyOf(m); h.sessCursor != 1 {
		t.Fatalf("sessCursor = %d, want 1", h.sessCursor)
	}
	m, cmd := click(m, x, y)
	if cmd == nil {
		t.Error("a second click must open the session")
	}
}

func TestDetailClickWithBreadcrumbAndHeader(t *testing.T) {
	m := liveWithChunks(1)
	m = withChunks(m, []transcript.Chunk{{ID: "a", Kind: transcript.ChunkAI, Items: []transcript.Item{
		{Kind: transcript.ItemText, Text: "one"}, {Kind: transcript.ItemText, Text: "two"}, {Kind: transcript.ItemText, Text: "three"},
	}}})
	m, _ = onTr(m, func(v tview) tea.Cmd { v.enterDetail(); return nil })
	m = withTr(m, func(t *transcriptComp) {
		t.historyView = histDetail
		sub := t.transcript.detailStack[0]
		sub.label = "sub"
		sub.subagentType = "Explore"
		sub.subagentName = "scout"
		t.transcript.detailStack = append(t.transcript.detailStack, sub)
	})
	if got := tvIn(m).topFrame().detailHeaderText(60); got == "" {
		t.Fatal("the frame must draw a header")
	}
	y, x := findBlock(t, m, "three")
	m, _ = click(m, x, y)
	if f := tvIn(m).topFrame(); f == nil || f.cursor != 2 {
		t.Fatalf("detail cursor = %v, want 2", f)
	}
}

func TestTreeFoldClickOnNestedProject(t *testing.T) {
	m := withMouse(homeTestModel())
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2", NodeLabel: "far"})
	m.left.tree.rebuild()
	var row int
	for i, r := range m.left.tree.rows {
		if r.id == "n1:p1" {
			row = i
			if r.kind != rowProject || r.depth != 1 {
				t.Fatalf("project row = %+v, want a project at depth 1", r)
			}
		}
	}
	if row == 0 {
		t.Fatal("the project row is missing")
	}
	_, y := itemCell(t, m, regTree, row)
	line := frameLines(m)[y]
	i := strings.Index(line, "▾")
	if i < 0 {
		t.Fatalf("no fold marker on the project row: %q", line)
	}
	m, _ = click(m, ansi.StringWidth(line[:i]), y)
	if !m.left.tree.isFolded("n1:p1") {
		t.Error("a click on the project's marker must fold it")
	}
}

func TestChangesCommitSecondClickDrillsIn(t *testing.T) {
	m := withMouse(wideWorkspace())
	m.right.tab = sideChanges
	m = withFocus(m, rightSidebar)
	m.right.changes.files = []api.ChangedFile{{Path: "a.go", Change: "modified"}}
	m.right.changes.commits = []api.Commit{{SHA: "aaa", Short: "aaa", Subject: "one"}, {SHA: "bbb", Short: "bbb", Subject: "two"}}
	y, x := findBlock(t, m, "two")
	m, _ = click(m, x, y)
	if m.right.changes.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.right.changes.cursor)
	}
	m, _ = click(m, x, y)
	if ch := m.right.changes; ch.commit == nil || ch.commit.SHA != "bbb" {
		t.Errorf("commit = %+v, want bbb opened", ch.commit)
	}
}

func TestCreatePickerTabClickWithSetupLine(t *testing.T) {
	m := withMouse(wideWorkspace())
	m.left.tree.data[0].Scripts = &api.ProjectScripts{Setup: "make deps"}
	m = openCreate(m, "n1:p1")
	findBlock(t, m, "setup: make deps")
	y, x := findBlock(t, m, createTabNames[ctBranches])
	m, _ = click(m, x, y)
	if p, _ := m.frontCreate(); p.tab != ctBranches {
		t.Errorf("tab = %v, want the branches tab", p.tab)
	}
}

func TestCreatePickerListClickSelectsRow(t *testing.T) {
	m := withMouse(wideWorkspace())
	m = openCreate(m, "n1:p1")
	m = withCreate(m, func(p *createPicker) {
		p.tab = ctPRs
		p.prs = []api.PRInfo{{Number: 1, Title: "one"}, {Number: 2, Title: "two"}}
		p.prsLoaded = true
	})
	y, x := findBlock(t, m, "#2")
	m, _ = click(m, x, y)
	if p := createOf(m); p.cursor != 1 {
		t.Fatalf("PRs cursor = %d, want 1", p.cursor)
	}

	m = withCreate(m, func(p *createPicker) {
		p.tab = ctBranches
		p.branches.load([]api.BranchInfo{{Name: "main", Local: true}, {Name: "dev", Local: true}}, nil)
	})
	y, x = findBlock(t, m, "dev")
	m, _ = click(m, x, y)
	if p := createOf(m); p.branches.cursor != 1 {
		t.Fatalf("branches cursor = %d, want 1", p.branches.cursor)
	}
}
