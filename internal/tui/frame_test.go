package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// waitingSession is homeTestModel's first session, open and asking to run Bash.
func waitingSession() model {
	m := homeTestModel()
	s := m.sessions["n1:s1"]
	s.Status = session.StatusAwaitingInput
	s.Name = "fix-login"
	s.Branch = "main"
	s.Tmux = session.TmuxLocation{SessionName: "fix-login", PaneID: "%1"}
	s.Interaction = &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", Message: "Allow Bash?"}
	m.sessions["n1:s1"] = s
	m = openLive(m, "n1:s1")
	return withEntries(m, sampleEntries())
}

// logsModel is the Logs tab of an embedded node with a few log lines.
func logsModel(w int, treeHidden bool) model {
	b := logbuf.New(100)
	for i := range 5 {
		fmt.Fprintf(b, "time=12:00:0%d level=INFO msg=\"line %d\"\n", i, i)
	}
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = w, 30
	m.left.hidden = treeHidden
	return withView(m, viewLogs)
}

func historyProjects() []session.HistoryProject {
	return []session.HistoryProject{
		histProj,
		{NodeID: "n1", ProjectDir: "/q", Label: "other", Cwd: "/q"},
	}
}

func historyPage() session.HistorySessionPage {
	return session.HistorySessionPage{Items: []session.HistorySession{
		{SessionID: "h1", TranscriptPath: "/p/h1.jsonl", Agent: "claude", Title: "first session", Resumable: true},
		{SessionID: "h2", TranscriptPath: "/p/h2.jsonl", Agent: "claude", Title: "second session"},
	}}
}

// historyTranscript is a past session open from the History tab, or in the
// offline viewer.
func historyTranscript(viewer bool) model {
	m := homeTestModel()
	m.viewer = viewer
	m = withView(m, viewHistoryTranscript)
	m = withTr(m, func(t *transcriptComp) {
		t.history.project = histProj
		t.history.title = "first session"
		t.history.openSession = session.HistorySession{Title: "first session", ModelName: "Opus 4.8"}
	})
	return withEntries(m, sampleEntries())
}

func liveScreenModel() model { return liveScreenModelWith(func(*model) {}) }

// liveScreenModelWith is the live screen over Home, after edit sets up the
// frame around it.
func liveScreenModelWith(edit func(m *model)) model {
	m := homeTestModel()
	edit(&m)
	s := m.sessions["n1:s1"]
	s.Tmux = session.TmuxLocation{SessionName: "fix-login", PaneID: "%1"}
	m.sessions["n1:s1"] = s
	m = withScreenOf(withViews(m, viewHome, viewScreen), "n1:s1")
	cols, rows := m.termDims()
	term := vt.NewEmulator(cols, rows)
	_, _ = term.Write([]byte("$ go test ./...\r\nok  \tgithub.com/MunifTanjim/argus\r\n$ "))
	return withTerm(m, "t1", term)
}

func spawnOverHome() model {
	m := homeTestModel()
	m.client = &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
	m, cmd := upd(m, keyMsg("s"))
	return runSpawnReplies(m, cmd)
}

func TestEveryScreenStateFrameUnchanged(t *testing.T) {
	onTree := func(m model) model { return pressKeys(m, cw('h')...) }
	cases := []struct {
		name  string
		build func() model
	}{
		{"session-dock-collapsed", waitingSession},
		{"session-dock-focused", func() model { return withFocus(waitingSession(), sessionDock) }},
		{"session-tree-hidden", func() model {
			m := waitingSession()
			m.left.hidden = true
			return m
		}},
		{"session-files", func() model {
			m := waitingSession()
			m.width = 160
			m.right.hidden = false
			m, _ = m.syncSidebar()
			return withTreeLoaded(m, "n1:w1")
		}},
		{"session-file-open", func() model {
			m := waitingSession()
			m.width = 160
			m.right.hidden = false
			m, _ = m.syncSidebar()
			return openedFile(m)
		}},
		{"file-over-workspace", func() model { return withFocus(openedFile(wideWorkspace()), mainPane) }},
		{"files-focused", filesFocused},
		{"changes-tab", func() model {
			return changesFocused(
				api.ChangedFile{Path: "main.go", Change: "modified", Unstaged: true},
				api.ChangedFile{Path: "README.md", Change: "added", Staged: true},
			)
		}},
		{"logs-tree", func() model { return logsModel(120, false) }},
		{"logs-full", func() model { return logsModel(120, true) }},
		{"logs-full-files", func() model { return logsModel(160, true) }},
		{"logs-full-narrow", func() model { return logsModel(100, true) }},
		{"history-projects", func() model { return withHistoryProjects(homeTestModel(), historyProjects()...) }},
		{"history-sessions", func() model { return withHistorySessions(homeTestModel(), histProj, historyPage()) }},
		{"history-transcript", func() model { return historyTranscript(false) }},
		{"viewer", func() model { return historyTranscript(true) }},
		{"live-screen", liveScreenModel},
		{"live-screen-tree-hidden", func() model {
			return liveScreenModelWith(func(m *model) { m.left.hidden = true })
		}},
		{"live-screen-files", func() model {
			return liveScreenModelWith(func(m *model) {
				m.width = 160
				m.right.hidden = false
			})
		}},
		{"session-starting-dock", func() model {
			m := waitingSession()
			s := m.sessions["n1:s1"]
			s.Status = session.StatusStarting
			m.sessions["n1:s1"] = s
			return m
		}},
		{"spawn-from-splash", func() model {
			m := homeTestModel()
			m.sessions, m.order = map[string]session.Session{}, nil
			m.client = &spawnPickClient{projects: []session.HistoryProject{{Label: "p", Cwd: "/p"}}}
			m, cmd := upd(m, keyMsg("s"))
			return runSpawnReplies(m, cmd)
		}},
		{"spawn-over-home", spawnOverHome},
		{"spawn-prompt", func() model { return pressKeys(spawnOverHome(), keyMsg("enter")) }},
		{"spawn-over-tree", func() model {
			m := typeKeys(onTree(homeTestModel()), "jjj")
			m.client = &recordingClient{}
			m.beginPresetSpawn("n1", "/repo", "")
			return m
		}},
		{"create-picker", func() model { return createTestModel(t) }},
		{"help-tree", func() model { return typeKeys(onTree(homeTestModel()), "g?") }},
		{"help-home", func() model {
			m := homeTestModel()
			m.showHelp = true
			return m
		}},
		{"splash-quarantined", func() model {
			m := quarantinedModel(true)
			m.width, m.height = 100, 30
			m.reconnecting = true
			return m
		}},
		{"narrow-home", func() model {
			m := homeTestModel()
			m.width = sidebarMinWidth - 1
			return m
		}},
		{"narrow-session", func() model {
			m := waitingSession()
			m.width = sidebarMinWidth - 1
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertGolden(t, tc.name, tc.build()) })
	}
}

func TestHomeSplashTakesTheWholeTerminal(t *testing.T) {
	m := homeTestModel()
	m.width = 160
	m.right.hidden = false
	m.sessions, m.order = map[string]session.Session{}, nil
	if l := m.layout(); !l.bare || l.left != 0 || l.right != 0 || l.w != m.width || l.h != m.height {
		t.Errorf("splash layout = %+v, want the whole %dx%d terminal", l, m.width, m.height)
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	out := strings.Join(lines, "\n")
	if len(lines) != m.height || strings.Contains(out, "Projects") || strings.Contains(out, "Files  Changes") {
		t.Errorf("the splash should draw no sidebars, %d lines tall:\n%s", m.height, out)
	}
	if !strings.Contains(lines[0], "Argus") || !strings.Contains(lines[len(lines)-1], "spawn") {
		t.Errorf("the splash should draw its own title and the footer on the last line:\n%s", out)
	}
}

func TestLogsWithTheTreeHiddenSpanTheFrame(t *testing.T) {
	m := logsModel(160, true)
	l := m.layout()
	if l.bare || !l.full || l.left != 0 || l.right == 0 || l.w != m.width-l.right-screenMargin-dividerWidth || l.h != m.framedHeight() {
		t.Errorf("logs layout = %+v, want the frame edge to edge with the right sidebar", l)
	}
	out := ansi.Strip(m.View().Content)
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "Argus") || !strings.Contains(out, "Files  Changes") {
		t.Errorf("logs should keep the frame's title and the right sidebar:\n%s", out)
	}
	if !strings.HasPrefix(lines[4], "time=12:00:00") {
		t.Errorf("a log line should start at the terminal edge: %q", lines[4])
	}
}

func TestViewerTakesTheWholeTerminal(t *testing.T) {
	m := historyTranscript(true)
	m.width = 160
	m.right.hidden = false
	if l := m.layout(); !l.bare || l.left != 0 || l.right != 0 || l.w != m.width || l.h != m.height {
		t.Errorf("viewer layout = %+v, want the whole %dx%d terminal", l, m.width, m.height)
	}
	out := ansi.Strip(m.View().Content)
	if strings.Contains(out, "Projects") || strings.Contains(out, "Files  Changes") {
		t.Errorf("the viewer should draw no sidebars:\n%s", out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "Argus · proj") {
		t.Errorf("the viewer should draw its own title:\n%s", out)
	}
}

// TestTermDimsMatchTheBase pins the live screen's attach size, which resizes
// the remote terminal, to the base code's numbers at 120x30.
func TestTermDimsMatchTheBase(t *testing.T) {
	cases := []struct {
		name       string
		edit       func(m *model)
		cols, rows int
	}{
		{"with the tree", func(*model) {}, 77, 22},
		{"tree hidden", func(m *model) { m.left.hidden = true }, 114, 22},
		{"with both sidebars", func(m *model) {
			m.width = 160
			m.right.hidden = false
		}, 80, 22},
	}
	for _, tc := range cases {
		m := liveScreenModelWith(tc.edit)
		if cols, rows := m.termDims(); cols != tc.cols || rows != tc.rows {
			t.Errorf("%s: termDims = %dx%d, want %dx%d", tc.name, cols, rows, tc.cols, tc.rows)
		}
	}
}

func working(m model) model {
	s := m.sessions["n1:s1"]
	s.Status = session.StatusWorking
	m.sessions["n1:s1"] = s
	return m
}

func withSetupRunning(m model) model {
	m.left.tree.data[0].Workspaces = append([]api.WorkspaceNode{}, m.left.tree.data[0].Workspaces...)
	m.left.tree.data[0].Workspaces[1].Setup = &api.ScriptRun{State: "running", Command: "pnpm install"}
	return m
}

func TestSpinnerRunsWhileAShownComponentSpins(t *testing.T) {
	onTree := func(m model) model { return pressKeys(m, cw('h')...) }
	treeHidden := func(m model) model {
		m.left.hidden = true
		return m
	}
	cases := []struct {
		name string
		m    model
		want bool
	}{
		{"home, idle", homeTestModel(), false},
		{"home, working", working(homeTestModel()), true},
		{"tree screen, working", working(onTree(homeTestModel())), true},
		{"workspace pane, working", working(pressKeys(typeKeys(onTree(homeTestModel()), "jjj"), keyMsg("enter"))), true},
		{"session with the tree, working", working(openLive(homeTestModel(), "n1:s2")), true},
		{"session without the tree, working", working(treeHidden(openLive(homeTestModel(), "n1:s2"))), false},
		{"session, idle", openLive(homeTestModel(), "n1:s2"), false},
		{"history with the tree, setup running", withSetupRunning(withHistoryProjects(homeTestModel(), histProj)), true},
		{"history without the tree, setup running", withSetupRunning(treeHidden(withHistoryProjects(homeTestModel(), histProj))), false},
		{"create picker loading", toPRsTab(createTestModel(t)), true},
		{"viewer, working", working(historyTranscript(true)), false},
	}
	for _, tc := range cases {
		m := tc.m
		m.spinning = false
		if cmd := m.maybeSpin(); (cmd != nil) != tc.want || m.spinning != tc.want {
			t.Errorf("%s: spinner armed = %v, want %v", tc.name, cmd != nil, tc.want)
		}
	}
}

func TestSpinnerStartsWhenASpinningComponentShows(t *testing.T) {
	m := working(openLive(homeTestModel(), "n1:s2"))
	m.left.hidden = true
	m.spinning = false
	m, _ = upd(m, keyMsg("esc"))
	if _, ok := m.main.top().(homeComp); !ok || !m.spinning {
		t.Errorf("back to Home with a working session should start the spinner: top=%T spinning=%v", m.main.top(), m.spinning)
	}
}

// TestCardListPageStepMatchesTheBase pins the half-page step of every state
// that pages a card list. The steps are the base code's: (H-4)/10 where the
// tree screen or a bare pane showed, (H-6)/10 under the frame elsewhere.
func TestCardListPageStepMatchesTheBase(t *testing.T) {
	onTree := func(m model) model { return pressKeys(m, cw('h')...) }
	wide := func(m model) model {
		m.width = 160
		m.right.hidden = false
		m, _ = m.syncSidebar()
		return withTreeLoaded(m, "n1:w1")
	}
	treeScreen := map[int]int{23: 1, 24: 2, 25: 2, 26: 2, 30: 2, 31: 2, 34: 3, 35: 3}
	framedStep := map[int]int{23: 1, 24: 1, 25: 1, 26: 2, 30: 2, 31: 2, 34: 2, 35: 2}
	cases := []struct {
		name  string
		build func() model
		want  map[int]int
	}{
		{"home", homeTestModel, framedStep},
		{"home splash", func() model {
			m := homeTestModel()
			m.sessions, m.order = map[string]session.Session{}, nil
			return m
		}, treeScreen},
		{"history projects", func() model { return withHistoryProjects(homeTestModel(), historyProjects()...) }, framedStep},
		{"history sessions", func() model { return withHistorySessions(homeTestModel(), histProj, historyPage()) }, framedStep},
		{"tree focused over History", func() model {
			return withFocus(withHistoryProjects(homeTestModel(), historyProjects()...), leftSidebar)
		}, framedStep},
		{"tree over Home", func() model { return onTree(homeTestModel()) }, framedStep},
		{"workspace pane", func() model { return pressKeys(typeKeys(onTree(homeTestModel()), "jjj"), keyMsg("enter")) }, treeScreen},
		{"files tab on the tree screen", filesFocused, treeScreen},
		{"changes tab on the tree screen", func() model { return changesFocused() }, treeScreen},
		{"file over a workspace", func() model { return withFocus(openedFile(wideWorkspace()), mainPane) }, treeScreen},
		{"files tab over a session", func() model { return withFocus(wide(waitingSession()), rightSidebar) }, framedStep},
		{"file over a session", func() model { return withFocus(openedFile(wide(waitingSession())), mainPane) }, framedStep},
		{"spawn over the tree screen", func() model {
			m := typeKeys(onTree(homeTestModel()), "jjj")
			m.client = &recordingClient{}
			m.beginPresetSpawn("n1", "/repo", "")
			return m
		}, framedStep},
		{"spawn over Home", spawnOverHome, framedStep},
	}
	for _, tc := range cases {
		for h, want := range tc.want {
			m := tc.build()
			m.height = h
			if got := m.cardListPageStep(); got != want {
				t.Errorf("%s at height %d: page step = %d, want %d", tc.name, h, got, want)
			}
		}
	}
}

func spinTicks(cmd tea.Cmd) int {
	n := 0
	for _, msg := range execCmd(cmd) {
		if _, ok := msg.(spinTickMsg); ok {
			n++
		}
	}
	return n
}

func TestOneSpinnerTickChainRuns(t *testing.T) {
	m := homeTestModel()
	m.client = &recordingClient{}
	working := m.sessions["n1:s1"]
	working.Status = session.StatusWorking
	event, _ := json.Marshal(registry.Event{Type: registry.EventUpdated, Session: working})
	var list []session.Session
	for _, s := range m.sessions {
		if s.ID == working.ID {
			s = working
		}
		list = append(list, s)
	}
	steps := []struct {
		name string
		msg  tea.Msg
		want int
	}{
		{"sessions replaced with one working", sessionsReplacedMsg(list), 1},
		{"a registry event", notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: event}), 0},
		{"a tree reply", projectsTreeMsg{tree: withSetupRunning(m).left.tree.data}, 0},
		{"a key", keyMsg("j"), 0},
		{"the tick", spinTickMsg{}, 1},
		{"a key after the tick", keyMsg("k"), 0},
	}
	for _, st := range steps {
		var cmd tea.Cmd
		m, cmd = upd(m, st.msg)
		if got := spinTicks(cmd); got != st.want {
			t.Errorf("%s: %d spinner ticks scheduled, want %d", st.name, got, st.want)
		}
	}
}

// idleSession is homeTestModel's first session, open with no pending
// interaction, so no dock shows.
func idleSession() model {
	m := homeTestModel()
	s := m.sessions["n1:s1"]
	s.Status = session.StatusIdle
	s.Name = "fix-login"
	s.Branch = "main"
	s.Tmux = session.TmuxLocation{SessionName: "fix-login", PaneID: "%1"}
	m.sessions["n1:s1"] = s
	m = openLive(m, "n1:s1")
	return withEntries(m, sampleEntries())
}

func TestPickerInputAndDetailFramesUnchanged(t *testing.T) {
	onTree := func(m model) model { return pressKeys(m, cw('h')...) }
	cases := []struct {
		name  string
		build func() model
	}{
		{"session-no-dock", idleSession},
		{"session-card-detail", func() model { return pressKeys(typeKeys(idleSession(), "gg"), keyMsg("enter")) }},
		{"retarget-picker", func() model {
			m := onTree(pressKeys(typeKeys(onTree(homeTestModel()), "jjj"), keyMsg("enter")))
			m.client = &recordingClient{}
			m = typeKeys(m, "T")
			m, _ = upd(m, branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main"}, {Name: "feature"}}})
			return m
		}},
		{"tree-filter", func() model { return typeKeys(onTree(homeTestModel()), "/fe") }},
		{"tree-rename", func() model {
			return typeKeys(onTree(pressKeys(typeKeys(onTree(homeTestModel()), "jj"), keyMsg("enter"))), "rx")
		}},
		{"history-export-prompt", func() model {
			return typeKeys(withHistorySessions(homeTestModel(), histProj, historyPage()), "E")
		}},
		{"history-transcript-export-prompt", func() model { return typeKeys(historyTranscript(false), "E") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertGolden(t, tc.name, tc.build()) })
	}
}
