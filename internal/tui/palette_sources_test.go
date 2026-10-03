package tui

import (
	"slices"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func runAction(m model, a paletteAction) model {
	c := &ctx{m: &m}
	a.run(c)
	m.apply(c)
	return m
}

func TestRevealRowUnfoldsAndClearsTheFilter(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.setFolded("n1:p1", true)
	m.left.tree.setFilter("zzz")
	if !m.left.tree.revealRow("n1:w2") {
		t.Fatal("revealRow(n1:w2) = false, want the row shown")
	}
	if m.left.tree.filter != "" || m.left.tree.collapsed["n1:p1"] {
		t.Errorf("filter = %q, folded = %v; want no filter and the project open", m.left.tree.filter, m.left.tree.collapsed["n1:p1"])
	}
	if got := m.left.tree.cursorRowID(); got != "n1:w2" {
		t.Errorf("cursor row = %q, want n1:w2", got)
	}
}

func TestOpenPlaceOpensAFoldedWorkspace(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.setFolded("n1:p1", true)
	m = runAction(m, openPlace(rowWorkspace, "n1:w2"))
	if ws, ok := m.baseComp().(workspaceComp); !ok || ws.ws != "n1:w2" {
		t.Fatalf("main pane = %T %+v, want workspace n1:w2", m.baseComp(), m.baseComp())
	}
	if m.focused != mainPane || m.left.tree.cursorRowID() != "n1:w2" {
		t.Errorf("focus = %v, tree row = %q; want the main pane and n1:w2", m.focused, m.left.tree.cursorRowID())
	}
}

func TestOpenPlaceOpensAProject(t *testing.T) {
	m := runAction(projectsTestModel(), openPlace(rowProject, "n1:p1"))
	if s, ok := m.baseComp().(summaryComp); !ok || s.kind != rowProject || s.id != "n1:p1" {
		t.Fatalf("main pane = %T %+v, want the project summary", m.baseComp(), m.baseComp())
	}
}

func TestOpenPlaceFlashesWhenGone(t *testing.T) {
	m := runAction(projectsTestModel(), openPlace(rowWorkspace, "n1:w9"))
	if m.flash != "workspace no longer exists" {
		t.Errorf("flash = %q", m.flash)
	}
	if _, ok := m.baseComp().(workspaceComp); ok || m.focused != leftSidebar {
		t.Error("the main pane or the focus changed for a missing workspace")
	}
}

func TestOpenSessionActionOpensTheSession(t *testing.T) {
	m := runAction(homeTestModel(), openSessionAction("n1:s2"))
	if tr := trOf(m); !tr.live || tr.sessionID != "n1:s2" {
		t.Fatalf("main pane = %T, want the live transcript of n1:s2", m.baseComp())
	}
}

func TestOpenSessionActionFlashesWhenEnded(t *testing.T) {
	m := runAction(homeTestModel(), openSessionAction("n1:s9"))
	if m.flash != "session ended" || viewOf(m) != viewHome {
		t.Errorf("flash = %q, view = %v; want \"session ended\" on Home", m.flash, viewOf(m))
	}
}

func TestPlaceID(t *testing.T) {
	cases := map[string]string{
		placeID(rowNode, "n1"):      "node:n1",
		placeID(rowProject, "n1:p"): "project:n1:p",
		placeID(rowWorkspace, "w"):  "ws:w",
		placeID(rowHome, homeRowID): "",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("placeID = %q, want %q", got, want)
		}
	}
}

func itemByID(items []paletteItem, id string) (paletteItem, bool) {
	i := slices.IndexFunc(items, func(it paletteItem) bool { return it.id == id })
	if i < 0 {
		return paletteItem{}, false
	}
	return items[i], true
}

func TestPlacesSourceWithOneNode(t *testing.T) {
	items := placesSource{}.items(projectsTestModel())
	want := []string{"node:n1", "project:n1:p1", "ws:n1:w1", "ws:n1:w2"}
	if got := itemIDs(items); !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	cases := []struct{ id, label, detail, parent string }{
		{"project:n1:p1", "argus", "home", "node:n1"},
		{"ws:n1:w1", "repo main", "argus", "project:n1:p1"},
		{"ws:n1:w2", "repo-feat feature", "argus", "project:n1:p1"},
	}
	for _, c := range cases {
		it, _ := itemByID(items, c.id)
		if it.label != c.label || it.detail != c.detail || it.parent != c.parent {
			t.Errorf("%s = %q %q parent %q; want %q %q parent %q", c.id, it.label, it.detail, it.parent, c.label, c.detail, c.parent)
		}
	}
}

func TestPlacesSourceWithManyNodes(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2", NodeLabel: "work"})
	items := placesSource{}.items(m)
	n1, ok := itemByID(items, "node:n1")
	if !ok || n1.label != "home" || n1.detail != "node" || n1.parent != "" {
		t.Fatalf("node:n1 = %+v, want label home at the root", n1)
	}
	p1, _ := itemByID(items, "project:n1:p1")
	if p1.parent != "node:n1" || p1.detail != "home" {
		t.Errorf("project:n1:p1 parent %q detail %q, want node:n1 and home", p1.parent, p1.detail)
	}
}

func TestPlacesSourceFollowsTheTreeToggles(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.data = append(m.left.tree.data, api.ProjectNode{ID: "n1:p2", Name: "secret", NodeID: "n1", Hidden: true})
	m.left.tree.data[0].Workspaces = append(m.left.tree.data[0].Workspaces, api.WorkspaceNode{ID: "n1:w3", Dir: "/old", IsGone: true})
	m.left.tree.setFilter("zzz")
	items := placesSource{}.items(m)
	if _, ok := itemByID(items, "project:n1:p2"); ok {
		t.Error("a hidden project shows while the tree hides it")
	}
	if _, ok := itemByID(items, "ws:n1:w3"); ok {
		t.Error("a gone workspace shows while the tree hides it")
	}
	if _, ok := itemByID(items, "ws:n1:w1"); !ok {
		t.Error("the tree filter must not apply to the palette")
	}
	m.left.tree.showHidden, m.left.tree.showGone = true, true
	items = placesSource{}.items(m)
	if _, ok := itemByID(items, "project:n1:p2"); !ok {
		t.Error("show hidden: the hidden project is missing")
	}
	if it, ok := itemByID(items, "ws:n1:w3"); !ok || it.label != "old" {
		t.Errorf("show gone: ws:n1:w3 = %+v, want label \"old\" without a branch", it)
	}
}

func TestSessionsSource(t *testing.T) {
	m := homeTestModel()
	s1 := m.sessions["n1:s1"]
	s1.Name, s1.Branch, s1.NodeLabel = "fix-login", "main", "home"
	m.sessions["n1:s1"] = s1
	s2 := m.sessions["n1:s2"]
	s2.Tmux.SessionName = "work"
	m.sessions["n1:s2"] = s2
	s3 := m.sessions["n1:s3"]
	s3.Agent, s3.WorkspaceID = "claude", ""
	m.sessions["n1:s3"] = s3
	items := sessionsSource{}.items(m)
	if got, want := itemIDs(items), []string{"session:n1:s1", "session:n1:s2", "session:n1:s3"}; !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v (home list order)", got, want)
	}
	cases := []struct{ id, label, detail, parent string }{
		{"session:n1:s1", "fix-login", "repo · main", "ws:n1:w1"},
		{"session:n1:s2", "work", "repo", "ws:n1:w2"},
		{"session:n1:s3", "claude", "repo", ""},
	}
	for _, c := range cases {
		it, _ := itemByID(items, c.id)
		if it.label != c.label || it.detail != c.detail || it.parent != c.parent {
			t.Errorf("%s = %q %q parent %q; want %q %q parent %q", c.id, it.label, it.detail, it.parent, c.label, c.detail, c.parent)
		}
	}
}

func TestSessionsSourceIgnoresTheListFilters(t *testing.T) {
	m := homeTestModel()
	m.sessionFilter = "zzz"
	m.activeOnly = true
	m.order = nil
	if got := len(sessionsSource{}.items(m)); got != len(m.sessions) {
		t.Errorf("items = %d, want every session (%d)", got, len(m.sessions))
	}
}

func TestSessionLabelFallsBackToTheID(t *testing.T) {
	if got := sessionLabel(session.Session{ID: "n1:s7"}); got != "n1:s7" {
		t.Errorf("label = %q, want the id", got)
	}
}

func TestSessionDetailShowsTheNodeWithManyNodes(t *testing.T) {
	s := session.Session{Repo: "repo", NodeLabel: "work"}
	if got := sessionDetail(s, false); got != "repo" {
		t.Errorf("one node: %q", got)
	}
	if got := sessionDetail(s, true); got != "repo · work" {
		t.Errorf("many nodes: %q", got)
	}
}

// Review focus: a session under a workspace that the snapshot lacks shows at
// the root only.
func TestSessionUnderAHiddenWorkspaceShowsAtTheRoot(t *testing.T) {
	m := homeTestModel()
	m.left.tree.data[0].Hidden = true
	snap := takeSnapshot(m, paletteMode{sources: []paletteSource{sessionsSource{}, placesSource{}}})
	if _, ok := itemByID(snap.under(""), "session:n1:s1"); !ok {
		t.Error("the session is missing at the root")
	}
	if _, ok := snap.item("ws:n1:w1"); ok {
		t.Error("the hidden project's workspace is in the snapshot")
	}
}

// An offline session reads as inactive, as on its card.
func TestSessionsSourceMutesOfflineSessions(t *testing.T) {
	m := homeTestModel()
	s := m.sessions["n1:s1"]
	s.Status, s.Offline = session.StatusWorking, true
	m.sessions["n1:s1"] = s
	it, _ := itemByID(sessionsSource{}.items(m), "session:n1:s1")
	if want := lipgloss.NewStyle().Foreground(ColorBorder).Render("○"); it.marker != want {
		t.Errorf("marker = %q, want the muted offline glyph %q", it.marker, want)
	}
}

// A workspace that the tree no longer shows, such as one that turned gone
// while show-gone is off, flashes instead of failing silently.
func TestOpenPlaceFlashesWhenTheTreeHidesTheRow(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.data[0].Workspaces[1].IsGone = true
	m = runAction(m, openPlace(rowWorkspace, "n1:w2"))
	if m.flash != "workspace no longer exists" || m.focused != leftSidebar {
		t.Errorf("flash = %q, focus = %v; want the flash and the focus kept", m.flash, m.focused)
	}
}
