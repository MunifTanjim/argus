package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// TestViewMap pins what the main pane shows for every reachable pair of its
// root and the focused container.
func TestViewMap(t *testing.T) {
	isHome := func(c component) bool { _, ok := c.(homeComp); return ok }
	isSummary := func(c component) bool { s, ok := c.(summaryComp); return ok && s.kind == rowProject }
	onWorkspace := func(ws string) func(component) bool {
		return func(c component) bool { p, ok := c.(workspaceComp); return ok && p.ws == ws }
	}
	onTree := func(m model) model { return pressKeys(m, cw('h')...) }
	hidden := func(m model) model { m.left.hidden = true; return m }
	wide := func() model {
		m := homeTestModel()
		m.width = 160
		m.right.hidden = false
		return m
	}
	cases := []struct {
		name  string
		build func() model
		root  func(component) bool
		focus container
		view  shownView
	}{
		{"Home focused, tree shown", homeTestModel, isHome, mainPane, viewHome},
		{"Home focused, tree hidden", func() model { return hidden(homeTestModel()) }, isHome, mainPane, viewHome},
		{"tree on the Home row", func() model { return onTree(homeTestModel()) }, isHome, leftSidebar, viewHome},
		{"tree on a project row", func() model { return typeKeys(onTree(homeTestModel()), "j") }, isHome, leftSidebar, viewHome},
		{"tree on a workspace row", func() model { return typeKeys(onTree(homeTestModel()), "jj") }, isHome, leftSidebar, viewHome},
		{"summary focused", func() model {
			return pressKeys(typeKeys(onTree(homeTestModel()), "j"), keyMsg("enter"))
		}, isSummary, mainPane, viewTree},
		{"workspace pane focused", func() model {
			return pressKeys(typeKeys(onTree(homeTestModel()), "jj"), keyMsg("enter"))
		}, onWorkspace("n1:w1"), mainPane, viewTree},
		{"right sidebar focused on a workspace", func() model {
			return pressKeys(pressKeys(typeKeys(onTree(wide()), "jj"), keyMsg("enter")), cw('l')...)
		}, onWorkspace("n1:w1"), rightSidebar, viewTree},
		{"tree hidden on the Home row", func() model { return typeKeys(onTree(homeTestModel()), " o") }, isHome, mainPane, viewHome},
		{"right sidebar focused when the tree empties", func() model {
			m := pressKeys(pressKeys(typeKeys(onTree(wide()), "jj"), keyMsg("enter")), cw('l')...)
			return treeEmptied(m)
		}, isHome, mainPane, viewHome},
		{"setup log over a workspace", func() model {
			return typeKeys(pressKeys(typeKeys(onTree(homeTestModel()), "jj"), keyMsg("enter")), "L")
		}, onWorkspace("n1:w1"), mainPane, viewTree},
		{"create picker over a project summary", func() model {
			return typeKeys(onTree(pressKeys(typeKeys(onTree(homeTestModel()), "j"), keyMsg("enter"))), "a")
		}, isSummary, leftSidebar, viewTree},
		{"create picker when the tree empties", func() model {
			return treeEmptied(typeKeys(onTree(pressKeys(typeKeys(onTree(homeTestModel()), "j"), keyMsg("enter"))), "a"))
		}, isHome, leftSidebar, viewHome},
		{"tree above History", func() model {
			return onTree(typeKeys(homeTestModel(), "gt"))
		}, func(c component) bool { _, ok := c.(historyComp); return ok }, leftSidebar, viewHistoryProjects},
		{"tree above Logs", func() model {
			m := homeTestModel()
			m.logs = logbuf.New(10)
			return onTree(typeKeys(m, "gT"))
		}, func(c component) bool { _, ok := c.(logsComp); return ok }, leftSidebar, viewLogs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build()
			if !tc.root(m.main[0]) || m.focused != tc.focus || viewOf(m) != tc.view {
				t.Errorf("root=%T focus=%v view=%v, want focus %v view %v", m.main[0], m.focused, viewOf(m), tc.focus, tc.view)
			}
		})
	}
}

// An empty project list moves the tree cursor to the Home row.
func treeEmptied(m model) model {
	m, _ = upd(m, projectsTreeMsg{tree: []api.ProjectNode{}})
	return m
}

// The create picker over an empty Home after a tree reload keeps the framed
// title: only the whole-terminal splash draws its own brand mark.
func TestCreatePickerOverAnEmptyHomeKeepsTheFramedTitle(t *testing.T) {
	m := homeTestModel()
	m.sessions, m.order = map[string]session.Session{}, nil
	m = treeEmptied(typeKeys(typeKeys(pressKeys(m, cw('h')...), "j"), "a"))
	if !createOpen(m) || !isHomeRoot(m) || !framed(m) {
		t.Fatalf("setup: picker open=%v root=%T framed=%v", createOpen(m), m.main[0], framed(m))
	}
	if n := strings.Count(ansi.Strip(m.View().Content), ansi.Strip(Icon.Claude.Render())+" Argus"); n != 1 {
		t.Errorf("the frame draws the Argus mark once, got %d:\n%s", n, ansi.Strip(m.View().Content))
	}
}

func isHomeRoot(m model) bool {
	_, ok := m.main[0].(homeComp)
	return ok
}

func TestHomeKillDisarmsUnderAResumedSession(t *testing.T) {
	m := typeKeys(killableHome(), "jdd")
	m, _ = upd(m, resumeResultMsg{sessionID: "n1:s1"})
	if viewOf(m) != viewSession || homeOf(m).killID != "" {
		t.Fatalf("a resumed session over Home: view=%v kill=%q, want the prompt dropped", viewOf(m), homeOf(m).killID)
	}
	m = pressKeys(m, keyMsg("esc"))
	if viewOf(m) != viewHome || m.keysRaw() {
		t.Fatalf("back on Home: view=%v raw=%v, want Home with keys through the keymap", viewOf(m), m.keysRaw())
	}
	if _, cmd := m.runKey(keyMsg("y")); cmd != nil {
		t.Error("y on Home after the resumed session must not kill")
	}
}

func TestHomeKeysMoveTheHomeCursor(t *testing.T) {
	m := homeTestModel()
	for _, step := range []struct {
		keys string
		want int
	}{{"j", 1}, {"j", 2}, {"j", 2}, {"k", 1}, {"G", 2}, {"gg", 0}} {
		m = typeKeys(m, step.keys)
		if got := homeOf(m).cursor; got != step.want {
			t.Fatalf("%s: cursor=%d, want %d", step.keys, got, step.want)
		}
	}
}

func TestHomeCursorSurvivesASessionAndTheTabs(t *testing.T) {
	m := typeKeys(homeTestModel(), "j")
	m = pressKeys(m, keyMsg("enter"))
	if viewOf(m) != viewSession || m.liveSessionID() != "n1:s2" {
		t.Fatalf("enter should open the second session: view=%v id=%q", viewOf(m), m.liveSessionID())
	}
	m = pressKeys(m, keyMsg("esc"))
	if viewOf(m) != viewHome || homeOf(m).cursor != 1 {
		t.Fatalf("back from the session: view=%v cursor=%d, want Home on card 1", viewOf(m), homeOf(m).cursor)
	}
	m = typeKeys(typeKeys(m, "gt"), "gT")
	if viewOf(m) != viewHome || homeOf(m).cursor != 1 {
		t.Errorf("History and back: view=%v cursor=%d, want Home on card 1", viewOf(m), homeOf(m).cursor)
	}
}

func TestHomeCursorSurvivesATripThroughTheTree(t *testing.T) {
	m := typeKeys(homeTestModel(), "j")
	m = typeKeys(pressKeys(m, cw('h')...), "jjkk")
	if _, ok := m.main[0].(homeComp); !ok || !m.onHomeRow() {
		t.Fatalf("the tree back on the Home row should show the Home pane: root=%T", m.main[0])
	}
	m = pressKeys(m, keyMsg("enter"))
	if viewOf(m) != viewHome || homeOf(m).cursor != 1 {
		t.Errorf("enter on the Home row: view=%v cursor=%d, want Home on card 1", viewOf(m), homeOf(m).cursor)
	}
}

func TestHomeKillConfirmation(t *testing.T) {
	rc := &recordingClient{}
	m := killableHome()
	m.client = rc
	m = typeKeys(m, "jdd")
	if homeOf(m).killID == "" || paneOf(m).killID != "" {
		t.Fatalf("dd should ask to kill on Home only: home=%q pane=%q", homeOf(m).killID, paneOf(m).killID)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "kill session repo · %1? y/n") {
		t.Error("the footer should ask to kill")
	}
	if n := typeKeys(m, "n"); homeOf(n).killID != "" {
		t.Error("any key but y should cancel")
	}
	m, cmd := typeKeysCmd(m, "y")
	if homeOf(m).killID != "" || cmd == nil {
		t.Fatal("y should clear the prompt and kill")
	}
	cmd()
	if p := rc.params[len(rc.params)-1].(api.SessionRef); p.SessionID != "n1:s2" {
		t.Errorf("kill sent for %q, want n1:s2", p.SessionID)
	}
}

func TestHomeKillTargetsTheSessionAskedAbout(t *testing.T) {
	rc := &recordingClient{}
	m := killableHome()
	m.client = rc
	m = typeKeys(m, "jdd")
	s3 := m.sessions["n1:s3"]
	s3.Status = session.StatusAwaitingInput
	params, _ := json.Marshal(registry.Event{Type: registry.EventUpdated, Session: s3})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params}))
	if m.order[homeOf(m).cursor] == "n1:s2" {
		t.Fatalf("setup: the order should move another session under the cursor: %v", m.order)
	}
	m, cmd := typeKeysCmd(m, "y")
	if cmd == nil {
		t.Fatal("y should kill")
	}
	cmd()
	if p := rc.params[len(rc.params)-1].(api.SessionRef); p.SessionID != "n1:s2" {
		t.Errorf("kill sent for %q, want n1:s2, the session the prompt named", p.SessionID)
	}
}

func TestTreeAboveHomeKeepsTheHomeCursor(t *testing.T) {
	m := pressKeys(typeKeys(homeTestModel(), "j"), cw('h')...)
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "┏") {
		t.Error("Home under the tree should draw its cursor card")
	}
	if !strings.Contains(out, "s spawn · / filter") {
		t.Errorf("the tree's footer should show:\n%s", out)
	}
	if homeOf(m).cursor != 1 {
		t.Errorf("the tree keeps the Home cursor: %d", homeOf(m).cursor)
	}
}

func TestHomeKeysReachOnlyTheHomePane(t *testing.T) {
	m := homeTestModel()
	res, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	mm := res.(model)
	if homeOf(mm).cursor != 1 || mm.left.tree.cursor != 0 {
		t.Errorf("j on Home: home cursor=%d tree cursor=%d", homeOf(mm).cursor, mm.left.tree.cursor)
	}
}
