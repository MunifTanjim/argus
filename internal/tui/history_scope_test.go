package tui

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"

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
