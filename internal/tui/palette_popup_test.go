package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func paletteOf(m model) (palettePopup, bool) {
	p, ok := m.popups.front().(palettePopup)
	return p, ok
}

func openPaletteIn(m model) model {
	m, _ = m.openPalette()
	return m
}

func paletteShows(m model) []string {
	p, _ := paletteOf(m)
	return matchIDs(p.matches)
}

// upKey and downKey are in cmdline_test.go.
var backspaceKey = tea.KeyPressMsg{Code: tea.KeyBackspace}

func paletteSelect(t *testing.T, m model, id string) model {
	t.Helper()
	p, _ := paletteOf(m)
	for range p.matches {
		if p, _ := paletteOf(m); p.matches[p.cursor].id == id {
			return m
		}
		m, _ = upd(m, ctrlKey('n'))
	}
	t.Fatalf("%s is not in the palette: %v", id, paletteShows(m))
	return m
}

// paletteTestModel is the Home screen with named sessions.
func paletteTestModel() model {
	m := homeTestModel()
	for id, name := range map[string]string{"n1:s1": "fix-login", "n1:s2": "add-tests", "n1:s3": "docs"} {
		s := m.sessions[id]
		s.Name = name
		m.sessions[id] = s
	}
	return m
}

func TestPaletteFirstScope(t *testing.T) {
	inTree := func(id string) model {
		m := selectRow(projectsTestModel(), id)
		return withFocus(m, leftSidebar)
	}
	onPane := func(id string) model {
		m := selectRow(projectsTestModel(), id)
		return withFocus(m, mainPane)
	}
	inSession := func(id string, ws string) model {
		m := homeTestModel()
		s := m.sessions[id]
		s.WorkspaceID = ws
		m.sessions[id] = s
		m, _ = m.enterSession(id)
		return withFocus(m, mainPane)
	}
	oneNode := projectsTestModel()
	oneNode.main = backStack{summaryComp{kind: rowNode, id: "n1"}}
	oneNode = withFocus(oneNode, mainPane)
	cases := []struct {
		name string
		m    model
		want string
	}{
		{"home", homeTestModel(), ""},
		{"tree on a workspace", inTree("n1:w1"), "ws:n1:w1"},
		{"tree on a project", inTree("n1:p1"), "project:n1:p1"},
		{"tree on home", inTree(homeRowID), ""},
		{"workspace pane", onPane("n1:w1"), "ws:n1:w1"},
		{"project summary", onPane("n1:p1"), "project:n1:p1"},
		{"session in a workspace", inSession("n1:s1", "n1:w1"), "ws:n1:w1"},
		{"session without a workspace", inSession("n1:s3", ""), ""},
		{"node that the snapshot lacks", oneNode, ""},
	}
	for _, c := range cases {
		p, ok := paletteOf(openPaletteIn(c.m))
		if !ok {
			t.Fatalf("%s: the palette did not open", c.name)
		}
		if p.scope != c.want {
			t.Errorf("%s: scope = %q, want %q", c.name, p.scope, c.want)
		}
	}
}

func TestPaletteListsTheScope(t *testing.T) {
	m := openPaletteIn(withFocus(selectRow(projectsTestModel(), "n1:w1"), leftSidebar))
	if got, want := paletteShows(m), []string{"session:n1:s1", "session:n1:s3"}; !slices.Equal(got, want) {
		t.Errorf("workspace scope shows %v, want %v", got, want)
	}
}

func TestPaletteTabNarrowsAndBackspaceWidens(t *testing.T) {
	m := openPaletteIn(paletteTestModel())
	m = paletteSelect(t, m, "session:n1:s1")
	m, _ = upd(m, keyMsg("tab"))
	if p, _ := paletteOf(m); p.scope != "" {
		t.Fatalf("tab on a session changed the scope to %q", p.scope)
	}
	m = paletteSelect(t, m, "project:n1:p1")
	m, _ = upd(m, keyMsg("tab"))
	p, _ := paletteOf(m)
	if p.scope != "project:n1:p1" || p.input.Value() != "" || p.cursor != 0 {
		t.Fatalf("tab on the project: scope %q, query %q, cursor %d", p.scope, p.input.Value(), p.cursor)
	}
	want := []string{"session:n1:s1", "session:n1:s2", "session:n1:s3", "ws:n1:w1", "ws:n1:w2"}
	if got := paletteShows(m); !slices.Equal(got, want) {
		t.Errorf("project scope shows %v, want %v", got, want)
	}
	m, _ = upd(m, backspaceKey)
	if p, _ := paletteOf(m); p.scope != "" {
		t.Errorf("backspace: scope = %q, want the root", p.scope)
	}
	m, _ = upd(m, backspaceKey)
	if p, ok := paletteOf(m); !ok || p.scope != "" {
		t.Error("backspace at the root must keep the palette open at the root")
	}
}

func TestPaletteBackspaceEditsANonEmptyQuery(t *testing.T) {
	m := openPaletteIn(withFocus(selectRow(projectsTestModel(), "n1:w1"), leftSidebar))
	m = typeKeys(m, "ab")
	m, _ = upd(m, backspaceKey)
	if p, _ := paletteOf(m); p.input.Value() != "a" || p.scope != "ws:n1:w1" {
		t.Errorf("query %q scope %q, want \"a\" and the workspace scope", p.input.Value(), p.scope)
	}
}

func TestPaletteTypingFilters(t *testing.T) {
	m := typeKeys(openPaletteIn(paletteTestModel()), "fix")
	p, _ := paletteOf(m)
	if len(p.matches) == 0 || p.matches[0].id != "session:n1:s1" || p.cursor != 0 {
		t.Errorf("matches %v cursor %d, want session:n1:s1 first", paletteShows(m), p.cursor)
	}
}

func TestPaletteCursorWraps(t *testing.T) {
	m := openPaletteIn(paletteTestModel())
	n := len(paletteShows(m))
	m, _ = upd(m, upKey)
	if p, _ := paletteOf(m); p.cursor != n-1 {
		t.Errorf("up from the top: cursor %d, want %d", p.cursor, n-1)
	}
	m, _ = upd(m, downKey)
	if p, _ := paletteOf(m); p.cursor != 0 {
		t.Errorf("down from the bottom: cursor %d, want 0", p.cursor)
	}
}

func TestPaletteEnterOpensASession(t *testing.T) {
	m := paletteSelect(t, openPaletteIn(paletteTestModel()), "session:n1:s2")
	m, _ = upd(m, keyMsg("enter"))
	if _, ok := paletteOf(m); ok {
		t.Fatal("the palette is still open")
	}
	if tr := trOf(m); !tr.live || tr.sessionID != "n1:s2" {
		t.Errorf("main pane = %T, want the live transcript of n1:s2", m.baseComp())
	}
}

func TestPaletteEnterOpensAFoldedWorkspace(t *testing.T) {
	m := paletteTestModel()
	m.left.tree.setFolded("n1:p1", true)
	m = paletteSelect(t, openPaletteIn(m), "ws:n1:w2")
	m, _ = upd(m, keyMsg("enter"))
	if ws, ok := m.baseComp().(workspaceComp); !ok || ws.ws != "n1:w2" {
		t.Fatalf("main pane = %T, want workspace n1:w2", m.baseComp())
	}
	if m.left.tree.cursorRowID() != "n1:w2" {
		t.Errorf("tree row = %q, want n1:w2", m.left.tree.cursorRowID())
	}
}

func TestPaletteStaleSessionFlashes(t *testing.T) {
	m := paletteSelect(t, openPaletteIn(paletteTestModel()), "session:n1:s2")
	delete(m.sessions, "n1:s2")
	m, _ = upd(m, keyMsg("enter"))
	if _, ok := paletteOf(m); ok || m.flash != "session ended" {
		t.Errorf("open = %v, flash = %q; want closed with \"session ended\"", ok, m.flash)
	}
}

func TestPaletteEscCloses(t *testing.T) {
	m, _ := upd(openPaletteIn(paletteTestModel()), keyMsg("esc"))
	if _, ok := paletteOf(m); ok {
		t.Error("esc did not close the palette")
	}
}

func TestPalettePasteFilters(t *testing.T) {
	m, _ := upd(openPaletteIn(paletteTestModel()), tea.PasteMsg{Content: "docs"})
	if got := paletteShows(m); len(got) == 0 || got[0] != "session:n1:s3" {
		t.Errorf("paste: matches %v, want session:n1:s3 first", got)
	}
}

// With nothing to list, enter does nothing.
func TestPaletteEmptyShowsNoMatches(t *testing.T) {
	m := homeTestModel()
	m.sessions = map[string]session.Session{}
	m.order = nil
	m.left.tree.data = nil
	m = openPaletteIn(m)
	if !strings.Contains(ansi.Strip(m.View().Content), "no matches") {
		t.Error("an empty palette must show \"no matches\"")
	}
	m, _ = upd(m, keyMsg("enter"))
	if _, ok := paletteOf(m); !ok {
		t.Error("enter with no match closed the palette")
	}
}

// A small terminal and wide labels stay inside the frame. The palette must not
// make a frame wider than the terminal, or than the frame without the palette.
func TestPaletteFitsSmallTerminalsAndWideLabels(t *testing.T) {
	widest := func(s string) int {
		w := 0
		for _, ln := range strings.Split(ansi.Strip(s), "\n") {
			w = max(w, lipgloss.Width(ln))
		}
		return w
	}
	m := paletteTestModel()
	s := m.sessions["n1:s1"]
	s.Name = "修正ログインの不具合を直すセッション"
	m.sessions["n1:s1"] = s
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{ID: "n1:p2", Name: "a-very-long-project-name-that-does-not-fit", NodeID: "n1"})
	for _, size := range [][2]int{{120, 30}, {40, 12}, {24, 10}} {
		m.width, m.height = size[0], size[1]
		limit := max(size[0], widest(m.View().Content))
		if got := widest(openPaletteIn(m).View().Content); got > limit {
			t.Errorf("%dx%d: the frame is %d wide, wider than %d", size[0], size[1], got, limit)
		}
	}
}

func TestPaletteFrames(t *testing.T) {
	cases := []struct {
		name string
		m    model
	}{
		{"palette-global", openPaletteIn(paletteTestModel())},
		{"palette-workspace-scope", openPaletteIn(withFocus(selectRow(paletteTestModel(), "n1:w1"), leftSidebar))},
		{"palette-no-match", typeKeys(openPaletteIn(paletteTestModel()), "zzzz")},
	}
	for _, c := range cases {
		assertGolden(t, c.name, c.m)
	}
}

func TestCtrlKOpensThePalette(t *testing.T) {
	logs := homeTestModel()
	logs = withView(logs, viewLogs)
	cases := []struct {
		name string
		m    model
	}{
		{"home", homeTestModel()},
		{"tree", projectsTestModel()},
		{"workspace pane", withFocus(selectRow(projectsTestModel(), "n1:w1"), mainPane)},
		{"files sidebar", withFocus(wideWorkspace(), rightSidebar)},
		{"live transcript", openLive(homeTestModel(), "n1:s1")},
		{"logs", logs},
	}
	for _, c := range cases {
		m, _ := upd(c.m, ctrlKey('k'))
		if _, ok := paletteOf(m); !ok {
			t.Errorf("%s: <C-k> did not open the palette", c.name)
		}
	}
}

func TestCtrlKSkipsTextInputsAndPopups(t *testing.T) {
	dock := withFocus(promptModel(&session.Interaction{Kind: session.InteractionIdle}), sessionDock)
	m, _ := upd(dock, ctrlKey('k'))
	if _, ok := paletteOf(m); ok {
		t.Error("<C-k> in the prompt opened the palette")
	}
	m, _ = upd(typeKeys(wideWorkspace(), ":"), ctrlKey('k'))
	if _, ok := paletteOf(m); ok || !cmdLineOpen(m) {
		t.Error("<C-k> over the command line opened the palette")
	}
}

// <C-k> in the open palette does not open a second one.
func TestCtrlKInThePaletteOpensNoSecondPalette(t *testing.T) {
	m, _ := upd(homeTestModel(), ctrlKey('k'))
	m, _ = upd(m, ctrlKey('k'))
	if len(m.popups) != 1 {
		t.Errorf("popups = %d, want 1", len(m.popups))
	}
}

func TestRemappedPaletteKey(t *testing.T) {
	m := withKeymap(homeTestModel(), map[string]map[string]string{"global": {"<C-g>": "open palette"}})
	m, _ = upd(m, ctrlKey('g'))
	if _, ok := paletteOf(m); !ok {
		t.Error("the remapped key did not open the palette")
	}
}

func TestCommandLineOpensThePalette(t *testing.T) {
	m := typeKeys(homeTestModel(), ":open palette")
	m, _ = upd(m, keyMsg("enter"))
	if _, ok := paletteOf(m); !ok {
		t.Errorf("`:open palette` did not open the palette; flash = %q", m.flash)
	}
}

// A first scope with nothing under it, such as a new workspace, falls back to
// the nearest scope above it that lists something.
func TestPaletteFirstScopeSkipsEmptyPlaces(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.data[0].Workspaces = append(m.left.tree.data[0].Workspaces, api.WorkspaceNode{ID: "n1:w3", Dir: "/repo-new", Branch: "new"})
	m.left.tree.rebuild()
	m = withFocus(selectRow(m, "n1:w3"), leftSidebar)
	if p, _ := paletteOf(openPaletteIn(m)); p.scope != "project:n1:p1" {
		t.Errorf("scope = %q, want project:n1:p1", p.scope)
	}
}

// The scope chain has its own line above the query, so a long chain never
// hides the query.
func TestPaletteScopeLineIsAboveTheQuery(t *testing.T) {
	m := paletteTestModel()
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2", NodeLabel: "work"})
	m.left.tree.data[0].Workspaces[0].Dir = "/a-workspace-with-a-very-long-directory-name"
	m.left.tree.data[0].Workspaces[0].Branch = "feature/a-branch-name-that-is-also-very-long"
	m.left.tree.rebuild()
	m = withFocus(selectRow(m, "n1:w1"), leftSidebar)
	m = typeKeys(openPaletteIn(m), "fix")
	if p, _ := paletteOf(m); p.scope != "ws:n1:w1" {
		t.Fatalf("scope = %q, want ws:n1:w1", p.scope)
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	q := slices.IndexFunc(lines, func(ln string) bool { return strings.Contains(ln, "│ fix") })
	if q < 1 {
		t.Fatal("the query row is missing")
	}
	if strings.Contains(lines[q], "›") {
		t.Errorf("the query row holds the scope chain: %q", strings.TrimSpace(lines[q]))
	}
	if above := lines[q-1]; !strings.Contains(above, "…") || !strings.Contains(above, "is-also-very-long") {
		t.Errorf("the row above must end with the nearest scope, cut at its start: %q", strings.TrimSpace(above))
	}
}

func TestPaletteScopeLineAtTheRoot(t *testing.T) {
	m := typeKeys(openPaletteIn(paletteTestModel()), "fix")
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	q := slices.IndexFunc(lines, func(ln string) bool { return strings.Contains(ln, "│ fix") })
	if q < 1 || !strings.Contains(lines[q-1], "Everywhere") {
		t.Errorf("the row above the query at the root must say Everywhere")
	}
}

// Each scope in the chain shows the marker that its row shows in the list.
func TestPaletteScopeLineShowsMarkers(t *testing.T) {
	m := openPaletteIn(withFocus(selectRow(paletteTestModel(), "n1:w1"), leftSidebar))
	want := "▪ argus › " + Icon.Branch.Glyph + " repo main"
	if !strings.Contains(ansi.Strip(m.View().Content), want) {
		t.Errorf("the scope line must read %q", want)
	}
}

func TestPaletteWidth(t *testing.T) {
	for _, c := range []struct{ term, want int }{{160, 100}, {90, 86}} {
		m := paletteTestModel()
		m.width = c.term
		frame := ansi.Strip(openPaletteIn(m).View().Content)
		var got int
		for _, ln := range strings.Split(frame, "\n") {
			if i := strings.Index(ln, "╭"); i >= 0 && strings.Contains(ln, "─╮") && strings.Contains(frame, "Go to") {
				if j := strings.Index(ln[i:], "╮"); j > 0 {
					got = max(got, lipgloss.Width(ln[i:i+j])+1)
				}
			}
		}
		if got != c.want {
			t.Errorf("terminal %d: palette is %d wide, want %d", c.term, got, c.want)
		}
	}
}
