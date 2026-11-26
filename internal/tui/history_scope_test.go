package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func histFolder(node, cwd string) session.HistoryProject {
	return session.HistoryProject{NodeID: node, ProjectDir: "pd" + cwd, Cwd: cwd, Label: filepath.Base(cwd)}
}

func folderCwds(ps []session.HistoryProject) (out []string) {
	for _, p := range ps {
		out = append(out, p.Cwd)
	}
	return out
}

func TestScopedHistoryFoldersMatchExactly(t *testing.T) {
	m := projectsTestModel()
	all := []session.HistoryProject{
		histFolder("n1", "/repo"), histFolder("n1", "/repo/sub"), histFolder("n1", "/repo-feat/"),
		histFolder("n2", "/repo"), histFolder("n1", "/elsewhere"),
	}
	if got := folderCwds(m.scopeFolders(historyComp{ws: "n1:w1"}, all)); !slices.Equal(got, []string{"/repo"}) {
		t.Errorf("workspace n1:w1 folders = %v, want [/repo]", got)
	}
	if got := folderCwds(m.scopeFolders(historyComp{projectID: "n1:p1"}, all)); !slices.Equal(got, []string{"/repo", "/repo-feat/"}) {
		t.Errorf("project folders = %v, want [/repo /repo-feat/]", got)
	}
	if got := m.scopeFolders(historyComp{}, all); len(got) != len(all) {
		t.Errorf("Home folders = %d, want all %d", len(got), len(all))
	}
}

func TestScopedHistoryProjectSkipsGoneWorkspaces(t *testing.T) {
	m := projectsTestModel()
	m.left.tree.data[0].Workspaces[1].IsGone = true
	got := m.scopeFolders(historyComp{projectID: "n1:p1"}, []session.HistoryProject{histFolder("n1", "/repo-feat")})
	if len(got) != 0 {
		t.Errorf("a gone workspace's folder showed: %v", got)
	}
}

func TestHistoryScopeLine(t *testing.T) {
	m := projectsTestModel()
	c := &ctx{m: &m}
	home := historyComp{projects: []session.HistoryProject{histFolder("n1", "/a"), histFolder("n1", "/b")}}
	for _, tc := range []struct {
		h    historyComp
		want string
	}{
		{home, "all projects · 2 folders"},
		{historyComp{projectID: "n1:p1"}, "project · argus  /repo/.git"},
		{historyComp{ws: "n1:w2"}, "workspace · argus › repo-feat  /repo-feat"},
	} {
		if got := ansi.Strip(tc.h.scopeLine(c)); got != tc.want {
			t.Errorf("scope line = %q, want %q", got, tc.want)
		}
	}
}

func wsHistory(t *testing.T, folders ...session.HistoryProject) model {
	t.Helper()
	m := typeKeys(workspacePane(homeTestModel()), "gt")
	if h, ok := m.baseComp().(historyComp); !ok || h.ws != "n1:w1" {
		t.Fatalf("gt on the workspace: base = %T, want n1:w1's History", m.baseComp())
	}
	mm, _ := m.updateHistory(histProjectsMsg{projects: folders})
	return mm.(model)
}

func clickTab(t *testing.T, m model, i int) model {
	t.Helper()
	c := &ctx{m: &m}
	comp, cmd := m.baseComp().(clicker).click(c, hitTarget{kind: hitTab, index: i}, true)
	m.main = m.main.replaceAt(m.baseTop()-1, comp)
	_ = cmd
	m.apply(c)
	return m
}

func TestWorkspaceHistoryOpensItsOnlyFolder(t *testing.T) {
	m := wsHistory(t, histFolder("n1", "/repo"), histFolder("n1", "/repo-feat"))
	h := m.baseComp().(historyComp)
	if !h.inProject || h.project.Cwd != "/repo" || !h.loading {
		t.Fatalf("History of n1:w1: inProject=%v folder=%q loading=%v, want /repo's sessions loading", h.inProject, h.project.Cwd, h.loading)
	}
	if got := m.mainRow(); got != "n1:w1" {
		t.Errorf("tree row = %q, want n1:w1", got)
	}
	out := ansi.Strip(mainView(m))
	for _, want := range []string{"Sessions   History   Terminals", "workspace · argus › repo  /repo"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}

func TestWorkspaceHistoryEmpty(t *testing.T) {
	m := wsHistory(t, histFolder("n1", "/repo-feat"))
	if out := ansi.Strip(mainView(m)); !strings.Contains(out, "no past sessions in this workspace") {
		t.Errorf("empty workspace History:\n%s", out)
	}
}

func TestWorkspaceHistoryBackAndTabs(t *testing.T) {
	m := wsHistory(t, histFolder("n1", "/repo"))
	m, _ = upd(m, histSessionsMsg{projectDir: "pd/repo", page: historyPage()})
	m = typeKeys(m, "/first")
	m = pressKeys(m, keyMsg("enter"), keyMsg("esc"))
	if h, ok := m.baseComp().(historyComp); !ok || h.sessQuery != "" {
		t.Fatalf("<Esc> with a filter: base = %T, want History with the filter cleared", m.baseComp())
	}
	m = pressKeys(m, keyMsg("esc"))
	if w, ok := m.baseComp().(workspaceComp); !ok || w.ws != "n1:w1" {
		t.Fatalf("<Esc>: base = %T, want n1:w1's Sessions", m.baseComp())
	}
	if m.memory.home.view == homeHistory {
		t.Error("workspace History must not become Home's remembered view")
	}
	m = typeKeys(typeKeys(m, "gt"), "gt")
	if tc, ok := m.baseComp().(terminalsComp); !ok || tc.ws != "n1:w1" {
		t.Errorf("gt gt: base = %T, want n1:w1's Terminals", m.baseComp())
	}
	m = typeKeys(m, "gT")
	if h, ok := m.baseComp().(historyComp); !ok || h.ws != "n1:w1" {
		t.Errorf("gT from Terminals: base = %T, want n1:w1's History", m.baseComp())
	}
}

func TestWorkspaceHistoryTabWithoutTerminals(t *testing.T) {
	m := homeTestModel()
	m.nodeInfo = []api.NodeInfo{{ID: "n1"}}
	m = typeKeys(typeKeys(workspacePane(m), "gt"), "gt")
	if _, ok := m.baseComp().(workspaceComp); !ok {
		t.Errorf("gt gt without Terminals: base = %T, want back on Sessions", m.baseComp())
	}
}

func TestWorkspaceHistoryTabClicks(t *testing.T) {
	m := homeTestModel()
	m.nodeInfo = []api.NodeInfo{{ID: "n1"}}
	if b, ok := clickTab(t, workspacePane(m), 1).baseComp().(historyComp); !ok || b.ws != "n1:w1" {
		t.Errorf("click 1 without Terminals: base = %T, want n1:w1's History", b)
	}
	if b, ok := clickTab(t, workspacePane(homeTestModel()), 2).baseComp().(terminalsComp); !ok || b.ws != "n1:w1" {
		t.Errorf("click 2: base = %T, want n1:w1's Terminals", b)
	}
	if b, ok := clickTab(t, wsHistory(t, histFolder("n1", "/repo")), 0).baseComp().(workspaceComp); !ok || b.ws != "n1:w1" {
		t.Errorf("click 0 on History: base = %T, want n1:w1's Sessions", b)
	}
}

func TestWorkspaceHistoryFollowsTheTreeAndItsRemoval(t *testing.T) {
	m := wsHistory(t, histFolder("n1", "/repo"))
	m.left.tree.data[0].Workspaces = m.left.tree.data[0].Workspaces[1:]
	_ = mainView(m)
	mm, _ := m.updateHistory(histProjectsMsg{projects: []session.HistoryProject{histFolder("n1", "/repo")}})
	if h := mm.(model).baseComp().(historyComp); len(h.projects) != 0 {
		t.Errorf("a removed workspace still lists folders: %v", h.projects)
	}
}

func projectHistory(t *testing.T, folders ...session.HistoryProject) model {
	t.Helper()
	m := homeTestModel()
	m.openRow("n1:p1")
	m = typeKeys(m, "gt")
	if h, ok := m.baseComp().(historyComp); !ok || h.projectID != "n1:p1" {
		t.Fatalf("gt on the project: base = %T, want n1:p1's History", m.baseComp())
	}
	mm, _ := m.updateHistory(histProjectsMsg{projects: folders})
	return mm.(model)
}

func TestProjectHistoryListsItsWorkspaceFolders(t *testing.T) {
	m := projectHistory(t, histFolder("n1", "/repo-feat"), histFolder("n1", "/other"), histFolder("n1", "/repo"))
	h := m.baseComp().(historyComp)
	if h.inProject || len(h.projects) != 2 {
		t.Fatalf("project History: inProject=%v folders=%v, want the 2 workspace folders", h.inProject, folderCwds(h.projects))
	}
	out := ansi.Strip(mainView(m))
	for _, want := range []string{"Workspaces   History", "project · argus  /repo/.git"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "home") {
		t.Errorf("project History should not show node headers:\n%s", out)
	}
	if got := m.mainRow(); got != "n1:p1" {
		t.Errorf("tree row = %q, want n1:p1", got)
	}
	if m.memory.home.view == homeHistory {
		t.Error("project History must not become Home's remembered view")
	}
}

func TestProjectHistoryDrillAndBack(t *testing.T) {
	m := projectHistory(t, histFolder("n1", "/repo"))
	m = pressKeys(m, keyMsg("enter"))
	if h := m.baseComp().(historyComp); !h.inProject || h.project.Cwd != "/repo" {
		t.Fatalf("<CR>: want /repo's sessions, got inProject=%v %q", h.inProject, h.project.Cwd)
	}
	m = pressKeys(m, keyMsg("esc"))
	if h := m.baseComp().(historyComp); h.inProject {
		t.Fatal("<Esc> from a folder's sessions should return to the folder list")
	}
	m = pressKeys(m, keyMsg("esc"))
	if s, ok := m.baseComp().(summaryComp); !ok || s.id != "n1:p1" {
		t.Fatalf("<Esc> from the folder list: base = %T, want n1:p1's summary", m.baseComp())
	}
}

func TestProjectHistoryEmpty(t *testing.T) {
	m := projectHistory(t, histFolder("n1", "/other"))
	if out := ansi.Strip(mainView(m)); !strings.Contains(out, "no past sessions in this project") {
		t.Errorf("empty project History:\n%s", out)
	}
}

func TestProjectHistoryTabs(t *testing.T) {
	m := homeTestModel()
	m.openRow("n1:p1")
	if out := ansi.Strip(mainView(m)); !strings.Contains(out, "Workspaces   History") {
		t.Errorf("project summary lacks its tabs:\n%s", out)
	}
	if b, ok := clickTab(t, m, 1).baseComp().(historyComp); !ok || b.projectID != "n1:p1" {
		t.Errorf("click 1 on the summary: base = %T, want n1:p1's History", b)
	}
	m = projectHistory(t, histFolder("n1", "/repo"))
	if b, ok := clickTab(t, m, 0).baseComp().(summaryComp); !ok || b.id != "n1:p1" {
		t.Errorf("click 0 on History: base = %T, want n1:p1's summary", b)
	}
	if b, ok := typeKeys(m, "gT").baseComp().(summaryComp); !ok || b.id != "n1:p1" {
		t.Errorf("gT on History: base = %T, want n1:p1's summary", b)
	}
}
